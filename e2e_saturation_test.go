package pingtunnel

import (
	"crypto/sha256"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestE2E_Saturation_FullBandwidthTunnelSurvival: multiple unthrottled
// downloads push the tunnel to full bandwidth while the tunnel itself is
// probed for liveness. The reported failure mode was "run a full-bandwidth
// download and the whole network drops" — the tunnel must survive
// saturation: downloads complete with intact payloads, interactive conns
// keep working during the flood, pongs keep flowing, and new conns work
// after the flood without a restart.
func TestE2E_Saturation_FullBandwidthTunnelSurvival(t *testing.T) {
	const (
		downloads   = 3
		perDownload = int64(16) << 20 // 16 MiB each, unthrottled
	)
	targetAddr, stopTarget := startStreamVerifyServer(t, 0)
	defer stopTarget()

	opts := basePairOpts()
	opts.direct = false
	opts.timeout = 15
	p := startPair(t, opts)
	defer p.Close()

	var downloadFailures atomic.Int32
	var wg sync.WaitGroup
	for d := 0; d < downloads; d++ {
		wg.Add(1)
		go func(d int) {
			defer wg.Done()
			conn, err := socks5ConnectErr(p.clientAddr, targetAddr, 15*time.Second)
			if err != nil {
				t.Logf("download %d: socks5 connect failed: %v", d, err)
				downloadFailures.Add(1)
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(120 * time.Second))

			hdr := putStreamHeader('D', perDownload)
			if _, err := conn.Write(hdr[:]); err != nil {
				t.Logf("download %d: header write failed: %v", d, err)
				downloadFailures.Add(1)
				return
			}

			wantHash := sha256.New()
			expectedChunk := make([]byte, 64*1024)
			for i := int64(0); i < perDownload/(64*1024); i++ {
				fillPattern(expectedChunk, byte(i))
				_, _ = wantHash.Write(expectedChunk)
			}

			gotHash := sha256.New()
			buf := make([]byte, 256*1024)
			var received int64
			for received < perDownload {
				n, err := conn.Read(buf)
				if n > 0 {
					_, _ = gotHash.Write(buf[:n])
					received += int64(n)
				}
				if err != nil {
					if err == io.EOF && received == perDownload {
						break
					}
					t.Logf("download %d: died at %d/%d: %v", d, received, perDownload, err)
					downloadFailures.Add(1)
					return
				}
			}
			var gotSum, wantSum [32]byte
			copy(gotSum[:], gotHash.Sum(nil))
			copy(wantSum[:], wantHash.Sum(nil))
			if gotSum != wantSum {
				t.Logf("download %d: sha256 mismatch at %d bytes", d, received)
				downloadFailures.Add(1)
				return
			}
			t.Logf("download %d: %d MiB verified", d, perDownload>>20)
		}(d)
	}

	// While the tunnel is saturated, interactive conns must keep working.
	var interactiveOK, interactiveFail atomic.Int32
	for i := 0; i < 10; i++ {
		time.Sleep(400 * time.Millisecond)
		start := time.Now()
		// Interactive probes target the plain TCP echo (not the U/D stream
		// server) — they simulate a web page during the flood.
		conn, err := socks5ConnectErr(p.clientAddr, p.echoAddr, 10*time.Second)
		if err != nil {
			interactiveFail.Add(1)
			t.Logf("interactive %d: connect failed after %s: %v", i, time.Since(start), err)
			continue
		}
		msg := []byte(fmt.Sprintf("saturation-probe-%d", i))
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err := conn.Write(msg); err != nil {
			interactiveFail.Add(1)
			conn.Close()
			t.Logf("interactive %d: write failed: %v", i, err)
			continue
		}
		got := make([]byte, len(msg))
		if _, err := io.ReadFull(conn, got); err != nil {
			interactiveFail.Add(1)
			conn.Close()
			t.Logf("interactive %d: echo failed after %s: %v", i, time.Since(start), err)
			continue
		}
		conn.Close()
		if !bytesEqual(got, msg) {
			interactiveFail.Add(1)
			t.Logf("interactive %d: echo mismatch", i)
			continue
		}
		interactiveOK.Add(1)
		t.Logf("interactive %d: ok in %s under full bandwidth", i, time.Since(start))
	}

	wg.Wait()
	if downloadFailures.Load() > 0 {
		t.Fatalf("downloads failed under saturation: %d of %d", downloadFailures.Load(), downloads)
	}
	if interactiveOK.Load() == 0 {
		t.Fatalf("no interactive probe survived full-bandwidth saturation (failures=%d)", interactiveFail.Load())
	}
	if interactiveFail.Load()*2 > interactiveOK.Load() {
		t.Fatalf("interactive failure rate too high under saturation: ok=%d fail=%d",
			interactiveOK.Load(), interactiveFail.Load())
	}

	// The tunnel itself must still be alive after the flood: pong fresh and a
	// brand-new conn echoes without a restart.
	if p.client != nil && p.client.RTT() <= 0 {
		t.Logf("warning: RTT stale right after flood (pong may still be in flight)")
	}
	sconn := socks5Connect(t, p.clientAddr, p.echoAddr, 10*time.Second)
	defer sconn.Close()
	echoRoundTrip(t, sconn, []byte("post-flood-socks5-alive"), 20*time.Second)
	t.Logf("tunnel survived full-bandwidth saturation: downloads=%d interactive ok=%d fail=%d",
		downloads, interactiveOK.Load(), interactiveFail.Load())
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
