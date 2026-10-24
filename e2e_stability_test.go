package pingtunnel

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Stability and pressure scenarios mirrored from spp's e2e_stability_test.go:
// mixed chunk integrity under bulk load, short-connection churn, narrow-pipe
// backpressure across the inactivity-timeout window, and interactive traffic
// while a bulk download saturates the tunnel.

func startBackgroundBulk(t *testing.T, addr string, chunkSize int, seed byte, stop chan struct{}) {
	t.Helper()
	go func() {
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			return
		}
		defer conn.Close()
		chunk := make([]byte, chunkSize)
		fillPattern(chunk, seed)
		sink := make([]byte, chunkSize)
		go func() {
			for {
				if _, err := conn.Read(sink); err != nil {
					return
				}
			}
		}()
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := conn.Write(chunk); err != nil {
					return
				}
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()
}

// TestE2E_MixedChunkIntegrity: mixed large/small chunks on one stream while a
// bulk connection builds queue pressure — catches receive-side reordering or
// frame-window accounting bugs (mirrors spp's scenario of the same name).
func TestE2E_MixedChunkIntegrity(t *testing.T) {
	opts := basePairOpts()
	opts.timeout = 30
	p := startPair(t, opts)
	defer p.Close()

	stop := make(chan struct{})
	defer close(stop)
	startBackgroundBulk(t, p.clientAddr, 32*1024, 7, stop)
	time.Sleep(200 * time.Millisecond)

	conn, exhausted, dialErr := dialResourceRetry(p.clientAddr, 30*time.Second)
	if dialErr != nil {
		if exhausted {
			t.Skip("local resource exhaustion before mixed-chunk dial")
		}
		t.Fatalf("mixed-chunk dial: %v", dialErr)
	}
	defer conn.Close()

	// Alternating sizes force frame fragmentation and interleaved ACK/REQ
	// frames on the tunnel path.
	var payload []byte
	for i := 0; i < 8; i++ {
		large := make([]byte, 64*1024+512)
		fillPattern(large, byte(i+1))
		payload = append(payload, large...)
		small := make([]byte, 1024)
		fillPattern(small, byte(100+i))
		payload = append(payload, small...)
	}

	echoAndHash(t, conn, payload, 90*time.Second)
}

// TestE2E_ShortConnChurnIntegrity: short-connection churn with byte-exact
// verification — the CI gate for "stable proxy".
func TestE2E_ShortConnChurnIntegrity(t *testing.T) {
	opts := basePairOpts()
	opts.timeout = 30
	p := startPair(t, opts)
	defer p.Close()

	const (
		workers  = 16
		duration = 8 * time.Second
		msgSize  = 4096
		// iterPause caps the conn rate (~500/s): each churned conn leaves a
		// couple of TIME_WAIT sockets behind, and exceeding the host's
		// ephemeral-port range turns into dial failures that pollute every
		// test running after this one. The gates below (integrity, fail rate,
		// liveness) do not depend on the conn rate.
		iterPause = 25 * time.Millisecond
		// minOK is a liveness gate ("proxy still serving"), NOT a throughput
		// SLO: frame-mode conn setup takes several ICMP round trips, so on a
		// contended host the same workload completes fewer echoes. A wedged
		// proxy converges to 0 and trips the failure-rate gate as well.
		minOK = 16
	)

	deadline := time.Now().Add(duration)
	var okN, failN, exhaustedN, integrityN atomic.Int64
	firstErr := make(chan string, 1)
	recordErr := func(s string) {
		select {
		case firstErr <- s:
		default:
		}
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			payload := make([]byte, msgSize)
			fillPattern(payload, byte(id))
			want := sha256.Sum256(payload)
			for time.Now().Before(deadline) {
				time.Sleep(iterPause)
				conn, exhausted, err := dialResourceRetry(p.clientAddr, 30*time.Second)
				if err != nil {
					if exhausted {
						exhaustedN.Add(1)
						return
					}
					failN.Add(1)
					recordErr(fmt.Sprintf("dial: %v", err))
					time.Sleep(20 * time.Millisecond)
					continue
				}
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				_, werr := conn.Write(payload)
				got := make([]byte, msgSize)
				_, rerr := io.ReadFull(conn, got)
				conn.Close()
				if werr != nil {
					failN.Add(1)
					recordErr(fmt.Sprintf("w=%d write=%v", id, werr))
					continue
				}
				if rerr != nil {
					failN.Add(1)
					recordErr(fmt.Sprintf("w=%d read=%v", id, rerr))
					continue
				}
				if sha256.Sum256(got) != want {
					// Corrupted bytes are never environmental: hard fail.
					failN.Add(1)
					integrityN.Add(1)
					recordErr(fmt.Sprintf("w=%d payload hash mismatch", id))
					continue
				}
				okN.Add(1)
			}
		}(w)
	}
	wg.Wait()

	ok, fail, exhausted, integrity := okN.Load(), failN.Load(), exhaustedN.Load(), integrityN.Load()
	first := "<none>"
	select {
	case first = <-firstErr:
	default:
	}
	t.Logf("short churn: ok=%d fail=%d integrity=%d resourceExhausted=%d firstErr=%q",
		ok, fail, integrity, exhausted, first)
	if integrity > 0 {
		t.Fatalf("payload integrity corrupted during churn: %d of %d echoes", integrity, ok+fail)
	}
	if exhausted > 0 && ok < 50 {
		t.Skipf("local ephemeral port/socket exhaustion prevented the churn run: ok=%d exhausted=%d", ok, exhausted)
	}
	if ok < minOK {
		t.Fatalf("too few successful short conns: ok=%d (min %d), firstErr=%q", ok, minOK, first)
	}
	if fail*10 > ok {
		t.Fatalf("short conn failure rate too high: ok=%d fail=%d sample=%s", ok, fail, first)
	}
}

// TestE2E_ConcurrentBulkAndInteractiveIntegrity: concurrent bulk + interactive
// on the same tunnel; interactive echoes must stay intact under load.
func TestE2E_ConcurrentBulkAndInteractiveIntegrity(t *testing.T) {
	opts := basePairOpts()
	opts.timeout = 30
	p := startPair(t, opts)
	defer p.Close()

	stop := make(chan struct{})
	defer close(stop)
	startBackgroundBulk(t, p.clientAddr, 16*1024, 9, stop)
	time.Sleep(200 * time.Millisecond)

	for i := 0; i < 8; i++ {
		conn, exhausted, err := dialResourceRetry(p.clientAddr, 30*time.Second)
		if err != nil {
			if exhausted {
				t.Skipf("local resource exhaustion before interactive dial %d", i)
			}
			t.Fatalf("interactive dial %d: %v", i, err)
		}
		msg := []byte(fmt.Sprintf("interactive-page-%d-payload", i))
		echoRoundTrip(t, conn, msg, 45*time.Second)
		conn.Close()
	}
}

// TestE2E_NarrowPipe_HeavyBackpressure: large burst + narrow pipe. The frame
// send buffer and window are kept tiny and the target digests at 2MiB/s so the
// transfer stays under sustained backpressure for longer than the per-conn
// inactivity timeout — no false timeout, no dropped tail bytes (mirrors spp's
// 大流量 + 小水管 scenario).
func TestE2E_NarrowPipe_HeavyBackpressure(t *testing.T) {
	const (
		totalBytes        = 8 * 1024 * 1024 // 8 MiB
		narrowPipeRateBps = 2 * 1024 * 1024 // 2 MiB/s -> ~4s transfer (>2x the 2s timeout)
	)
	targetAddr, stopTarget := startStreamVerifyServer(t, narrowPipeRateBps)
	defer stopTarget()

	opts := basePairOpts()
	opts.direct = false // SOCKS5 so the test can dial the stream target per conn
	opts.tcpBS = 8192   // 8 frames in flight max
	opts.tcpMW = 100
	opts.tcpRST = 200
	opts.timeout = 2 // 2s inactivity window; a 4s one-way transfer must stay alive
	p := startPair(t, opts)
	defer p.Close()

	// 1. One-Way Upload (client blasts 8 MiB, target digests at 2 MiB/s)
	t.Run("Upload", func(t *testing.T) {
		conn := socks5Connect(t, p.clientAddr, targetAddr, 10*time.Second)
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(60 * time.Second))

		hdr := putStreamHeader('U', totalBytes)
		if _, err := conn.Write(hdr[:]); err != nil {
			t.Fatalf("write upload header: %v", err)
		}

		wantHash := sha256.New()
		chunk := make([]byte, 64*1024)
		var sent int64
		var seq byte
		for sent < totalBytes {
			fillPattern(chunk, seq)
			seq++
			_, _ = wantHash.Write(chunk)
			if _, err := conn.Write(chunk); err != nil {
				t.Fatalf("upload write at %d/%d: %v", sent, totalBytes, err)
			}
			sent += int64(len(chunk))
		}

		var gotSum [32]byte
		if _, err := io.ReadFull(conn, gotSum[:]); err != nil {
			t.Fatalf("read upload sha256 response after narrow-pipe drain: %v", err)
		}
		var wantSum [32]byte
		copy(wantSum[:], wantHash.Sum(nil))
		if gotSum != wantSum {
			t.Fatalf("upload sha256 mismatch: got %x, want %x", gotSum, wantSum)
		}
	})

	// 2. One-Way Download (target blasts 8 MiB and closes; client digests at 2 MiB/s)
	t.Run("Download", func(t *testing.T) {
		conn := socks5Connect(t, p.clientAddr, targetAddr, 10*time.Second)
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(60 * time.Second))

		hdr := putStreamHeader('D', totalBytes)
		if _, err := conn.Write(hdr[:]); err != nil {
			t.Fatalf("write download header: %v", err)
		}

		wantHash := sha256.New()
		expectedChunk := make([]byte, 64*1024)
		for i := int64(0); i < totalBytes/(64*1024); i++ {
			fillPattern(expectedChunk, byte(i))
			_, _ = wantHash.Write(expectedChunk)
		}

		gotHash := sha256.New()
		received, err := slowReaderHash(conn, totalBytes, narrowPipeRateBps, gotHash, time.Now().Add(60*time.Second))
		if err != nil {
			t.Fatalf("download read at %d/%d: %v", received, totalBytes, err)
		}

		var gotSum, wantSum [32]byte
		copy(gotSum[:], gotHash.Sum(nil))
		copy(wantSum[:], wantHash.Sum(nil))
		if gotSum != wantSum {
			t.Fatalf("download sha256 mismatch: got %x, want %x", gotSum, wantSum)
		}
	})
}

// TestE2E_InteractiveWebWhileBulkDownloadSaturatesPipe: one connection
// downloads a large stream slowly (saturating the tunnel), while a second
// connection opens a small "web page" request that must complete promptly.
// bulkEnvError marks a bulk-download failure that happened before the stream
// got going (host socket pressure), distinct from mid-stream tunnel failures.
type bulkEnvError struct{ err error }

func (e *bulkEnvError) Error() string { return e.err.Error() }
func (e *bulkEnvError) Unwrap() error { return e.err }

func runBulkDownload(p *e2ePair, targetAddr string, bulkBytes int64, rateBps int64, saturatedCh chan struct{}, saturatedOnce *sync.Once) error {
	// Non-fatal dial: this runs on a goroutine, where t.Fatalf would Goexit
	// and leave the test hanging on bulkDoneCh.
	conn, err := socks5ConnectErr(p.clientAddr, targetAddr, 10*time.Second)
	if err != nil {
		return &bulkEnvError{err: fmt.Errorf("bulk socks5 dial: %w", err)}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))

	hdr := putStreamHeader('D', bulkBytes)
	if _, err := conn.Write(hdr[:]); err != nil {
		return err
	}

	buf := make([]byte, 64*1024)
	var received int64
	var signaled bool
	start := time.Now()
	for received < bulkBytes {
		n, err := conn.Read(buf)
		if n > 0 {
			received += int64(n)
			if !signaled && received >= 512*1024 {
				signaled = true
				saturatedOnce.Do(func() { close(saturatedCh) })
			}
			expectedElapsed := time.Duration(float64(received) / float64(rateBps) * float64(time.Second))
			if sleep := expectedElapsed - time.Since(start); sleep > 0 {
				time.Sleep(sleep)
			}
		}
		if err != nil {
			if err == io.EOF && received == bulkBytes {
				return nil
			}
			if received < 1024*1024 {
				return &bulkEnvError{err: fmt.Errorf("bulk setup failed at %d bytes: %w", received, err)}
			}
			return fmt.Errorf("bulk read at %d/%d: %w", received, bulkBytes, err)
		}
	}
	return nil
}

func TestE2E_InteractiveWebWhileBulkDownloadSaturatesPipe(t *testing.T) {
	const (
		bulkBytes         = 8 * 1024 * 1024 // 8 MiB bulk download
		narrowPipeRateBps = 1 * 1024 * 1024 // 1 MiB/s -> ~8s total download
		webPageBytes      = 32 * 1024       // 32 KiB interactive webpage response
	)
	targetAddr, stopTarget := startStreamVerifyServer(t, narrowPipeRateBps)
	defer stopTarget()

	opts := basePairOpts()
	opts.direct = false
	opts.tcpBS = 8192
	opts.tcpMW = 100
	opts.tcpRST = 200
	opts.timeout = 5
	p := startPair(t, opts)
	defer p.Close()

	saturatedCh := make(chan struct{})
	bulkDoneCh := make(chan error, 1)

	go func() {
		// Heavy churn tests run before this one can leave the host out of
		// ephemeral ports; a server-side target dial then fails and the conn
		// is reset before any data flowed. Retry such never-started setups
		// (only while <1MiB was received) instead of failing on the
		// environment; genuine mid-stream breaks still propagate.
		var saturatedOnce sync.Once
		const maxAttempts = 3
		var lastErr error
		for attempt := 0; attempt < maxAttempts; attempt++ {
			if attempt > 0 {
				p.t.Logf("bulk download attempt %d failed with %v, retrying", attempt, lastErr)
				time.Sleep(3 * time.Second)
			}
			lastErr = runBulkDownload(p, targetAddr, bulkBytes, narrowPipeRateBps, saturatedCh, &saturatedOnce)
			if lastErr == nil {
				bulkDoneCh <- nil
				return
			}
			var envErr *bulkEnvError
			if !errors.As(lastErr, &envErr) {
				bulkDoneCh <- lastErr
				return
			}
		}
		bulkDoneCh <- lastErr
	}()

	// Wait until the bulk flow is actually streaming, then let queues fill.
	<-saturatedCh
	time.Sleep(300 * time.Millisecond)

	webStart := time.Now()
	webConn := socks5Connect(t, p.clientAddr, targetAddr, 5*time.Second)
	defer webConn.Close()
	_ = webConn.SetDeadline(time.Now().Add(10 * time.Second))

	hdr := putStreamHeader('D', webPageBytes)
	if _, err := webConn.Write(hdr[:]); err != nil {
		t.Fatalf("write web request failed after %s: %v", time.Since(webStart), err)
	}

	webBuf := make([]byte, webPageBytes)
	if _, err := io.ReadFull(webConn, webBuf); err != nil {
		t.Fatalf("read web page failed/timed out after %s while bulk download active: %v",
			time.Since(webStart), err)
	}
	webElapsed := time.Since(webStart)
	t.Logf("interactive web request completed in %s while bulk download saturated pipe", webElapsed)
	if webElapsed > 5*time.Second {
		t.Fatalf("interactive web request wedged behind bulk download (%s)", webElapsed)
	}

	if err := <-bulkDoneCh; err != nil {
		t.Fatalf("bulk download failed: %v", err)
	}
}

// TestE2E_LargeStream_UploadAndDownload streams a large transfer both ways
// through the tunnel verifying byte-for-byte SHA-256 integrity. Sized via
// PINGTUNNEL_E2E_LARGE_MB (default 64; e.g. 1024 for the full 1 GiB CI gate).
func TestE2E_LargeStream_UploadAndDownload(t *testing.T) {
	totalBytes := int64(64) << 20
	if v := os.Getenv("PINGTUNNEL_E2E_LARGE_MB"); v != "" {
		n := 0
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			totalBytes = int64(n) << 20
		}
	}
	targetAddr, stopTarget := startStreamVerifyServer(t, 0)
	defer stopTarget()

	opts := basePairOpts()
	opts.direct = false
	opts.timeout = 15
	p := startPair(t, opts)
	defer p.Close()

	// Precompute the deterministic pattern digest (256 distinct 64 KiB chunks repeated).
	var patternChunks [256][]byte
	for i := 0; i < 256; i++ {
		b := make([]byte, 64*1024)
		fillPattern(b, byte(i))
		patternChunks[i] = b
	}
	wantHash := sha256.New()
	totalChunks := totalBytes / (64 * 1024)
	for i := int64(0); i < totalChunks; i++ {
		_, _ = wantHash.Write(patternChunks[byte(i)])
	}
	var wantSum [32]byte
	copy(wantSum[:], wantHash.Sum(nil))

	t.Run("Upload", func(t *testing.T) {
		start := time.Now()
		conn := socks5Connect(t, p.clientAddr, targetAddr, 10*time.Second)
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))

		hdr := putStreamHeader('U', totalBytes)
		if _, err := conn.Write(hdr[:]); err != nil {
			t.Fatalf("write upload header: %v", err)
		}
		for i := int64(0); i < totalChunks; i++ {
			if _, err := conn.Write(patternChunks[byte(i)]); err != nil {
				t.Fatalf("upload write at chunk %d/%d: %v", i, totalChunks, err)
			}
		}

		var gotSum [32]byte
		if _, err := io.ReadFull(conn, gotSum[:]); err != nil {
			t.Fatalf("read upload sha256 response: %v", err)
		}
		if gotSum != wantSum {
			t.Fatalf("upload sha256 mismatch: got %x, want %x", gotSum, wantSum)
		}
		t.Logf("%d MiB upload verified in %s", totalBytes>>20, time.Since(start))
	})

	t.Run("Download", func(t *testing.T) {
		start := time.Now()
		conn := socks5Connect(t, p.clientAddr, targetAddr, 10*time.Second)
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))

		hdr := putStreamHeader('D', totalBytes)
		if _, err := conn.Write(hdr[:]); err != nil {
			t.Fatalf("write download header: %v", err)
		}

		gotHash := sha256.New()
		buf := make([]byte, 64*1024)
		var received int64
		for received < totalBytes {
			n, err := conn.Read(buf)
			if n > 0 {
				_, _ = gotHash.Write(buf[:n])
				received += int64(n)
			}
			if err != nil {
				if err == io.EOF && received == totalBytes {
					break
				}
				t.Fatalf("download read at %d/%d: %v", received, totalBytes, err)
			}
		}

		var gotSum [32]byte
		copy(gotSum[:], gotHash.Sum(nil))
		if gotSum != wantSum {
			t.Fatalf("download sha256 mismatch: got %x, want %x", gotSum, wantSum)
		}
		t.Logf("%d MiB download verified in %s", totalBytes>>20, time.Since(start))
	})
}
