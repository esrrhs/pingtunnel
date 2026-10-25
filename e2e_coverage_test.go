package pingtunnel

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/esrrhs/gohome/network"
)

// Coverage matrix and lifecycle/resilience scenarios, mirrored from spp's
// e2e_coverage_test.go and adapted to pingtunnel's config space: mode
// (direct/SOCKS5) × transport (tcpmode/UDP) × zstd compress × encryption.

// matrixParallel caps how many proxy pairs run at once. All raw ICMP sockets
// on the host see all loopback traffic, so every pair's recv loop parses every
// packet; too many concurrent pairs waste CPU and can push unlucky echoes past
// their deadline. PT_MATRIX_PARALLEL overrides for local tuning.
func matrixParallel() int {
	if v := strings.TrimSpace(os.Getenv("PT_MATRIX_PARALLEL")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if p := runtime.GOMAXPROCS(0); p < 4 {
		return p
	}
	return 4
}

var matrixGlobalSlots = make(chan struct{}, matrixParallel())

func acquireMatrixSlot() { matrixGlobalSlots <- struct{}{} }
func releaseMatrixSlot() { <-matrixGlobalSlots }

type matrixCodec struct {
	name   string
	crypto EncryptionMode
	key    string
}

func matrixCodecs() []matrixCodec {
	return []matrixCodec{
		{"none", NoEncryption, ""},
		{"aes128", AES128, "matrix-aes128-secret-ok"},
		{"aes256", AES256, "matrix-aes256-secret-ok"},
		{"chacha20", CHACHA20, "matrix-chacha-secret-ok"},
	}
}

func TestE2E_FullMatrix(t *testing.T) {
	codecs := matrixCodecs()
	payload := make([]byte, 2*1024)
	fillPattern(payload, 7)

	type leaf struct {
		name string
		opts pairOpts
	}
	var leaves []leaf

	for _, gz := range []int{0, 1024} {
		for _, codec := range codecs {
			opts := basePairOpts()
			opts.tcpGZ = gz
			opts.crypto = codec.crypto
			opts.cryptoKey = codec.key
			opts.timeout = 30
			leaves = append(leaves, leaf{
				name: fmt.Sprintf("DIRECT/tcp/gz%d/%s", gz, codec.name), opts: opts})

			socksOpts := opts
			socksOpts.direct = false
			leaves = append(leaves, leaf{
				name: fmt.Sprintf("SOCKS5/tcp/gz%d/%s", gz, codec.name), opts: socksOpts})
		}

		for _, codec := range codecs {
			opts := basePairOpts()
			opts.udp = true
			opts.crypto = codec.crypto
			opts.cryptoKey = codec.key
			opts.timeout = 30
			leaves = append(leaves, leaf{
				name: fmt.Sprintf("DIRECT/udp/gz%d/%s", gz, codec.name), opts: opts})
		}
	}

	for _, l := range leaves {
		l := l
		t.Run(l.name, func(t *testing.T) {
			t.Parallel()
			acquireMatrixSlot()
			defer releaseMatrixSlot()

			p := startPair(t, l.opts)
			defer p.Close()

			if l.opts.direct && !l.opts.udp {
				conn, err := waitForPort(p.clientAddr, 10*time.Second)
				if err != nil {
					t.Fatalf("dial: %v", err)
				}
				defer conn.Close()
				echoRoundTrip(t, conn, payload, 30*time.Second)
				return
			}
			if l.opts.udp {
				rConn, err := net.Dial("udp", p.clientAddr)
				if err != nil {
					t.Fatalf("dial udp: %v", err)
				}
				defer rConn.Close()
				deadline := time.Now().Add(15 * time.Second)
				reply := make([]byte, 4096)
				var n int
				var rerr error
				for time.Now().Before(deadline) {
					_ = rConn.SetDeadline(time.Now().Add(300 * time.Millisecond))
					_, _ = rConn.Write(payload)
					n, rerr = rConn.Read(reply)
					if rerr == nil && n > 0 {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
				if rerr != nil {
					t.Fatalf("udp echo failed: %v", rerr)
				}
				if sha256.Sum256(reply[:n]) != sha256.Sum256(payload) {
					t.Fatalf("udp echo integrity mismatch")
				}
				return
			}
			// SOCKS5
			conn := socks5Connect(t, p.clientAddr, p.echoAddr, 10*time.Second)
			defer conn.Close()
			echoRoundTrip(t, conn, payload, 30*time.Second)
		})
	}
}

// TestE2E_Lifecycle_Bidirectional: two conns pushing opposite directions at
// once must both stay intact.
func TestE2E_Lifecycle_Bidirectional(t *testing.T) {
	opts := basePairOpts()
	opts.timeout = 30
	p := startPair(t, opts)
	defer p.Close()

	up := make([]byte, 48*1024)
	down := make([]byte, 48*1024)
	fillPattern(up, 9)
	fillPattern(down, 11)
	wantUp := sha256.Sum256(up)
	wantDown := sha256.Sum256(down)

	var upErr, downErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		c, err := net.DialTimeout("tcp", p.clientAddr, 5*time.Second)
		if err != nil {
			upErr = err
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(30 * time.Second))
		if _, err := c.Write(up); err != nil {
			upErr = err
			return
		}
		got := make([]byte, len(up))
		if _, err := io.ReadFull(c, got); err != nil {
			upErr = err
			return
		}
		if sha256.Sum256(got) != wantUp {
			upErr = fmt.Errorf("uplink mismatch")
		}
	}()
	go func() {
		defer wg.Done()
		c, err := net.DialTimeout("tcp", p.clientAddr, 5*time.Second)
		if err != nil {
			downErr = err
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(30 * time.Second))
		if _, err := c.Write(down); err != nil {
			downErr = err
			return
		}
		got := make([]byte, len(down))
		if _, err := io.ReadFull(c, got); err != nil {
			downErr = err
			return
		}
		if sha256.Sum256(got) != wantDown {
			downErr = fmt.Errorf("downlink mismatch")
		}
	}()
	wg.Wait()
	if upErr != nil {
		t.Fatalf("stream A: %v", upErr)
	}
	if downErr != nil {
		t.Fatalf("stream B: %v", downErr)
	}
}

// TestE2E_Lifecycle_ReconnectAfterClientRestart: a fresh client (new local
// port, same key) must reconnect to the running server and serve again.
func TestE2E_Lifecycle_ReconnectAfterClientRestart(t *testing.T) {
	opts := basePairOpts()
	opts.timeout = 30
	p := startPair(t, opts)
	defer p.Close()

	conn, err := waitForPort(p.clientAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("wait1: %v", err)
	}
	echoRoundTrip(t, conn, []byte("before-restart"), 15*time.Second)
	conn.Close()

	p.StopClient()
	time.Sleep(200 * time.Millisecond)

	newAddr := fmt.Sprintf("127.0.0.1:%d", getFreePort(t))
	c2, err := NewClient(newAddr, "127.0.0.1", p.echoAddr, opts.timeout, p.key, "127.0.0.1",
		false, false, 0, nil, "", "", opts.congestion)
	if err != nil {
		t.Fatalf("NewClient after restart: %v", err)
	}
	if err := c2.Run(); err != nil {
		t.Fatalf("client Run after restart: %v", err)
	}
	defer c2.Stop()

	conn2, err := waitForPort(newAddr, 10*time.Second)
	if err != nil {
		t.Fatalf("wait2: %v", err)
	}
	defer conn2.Close()
	echoRoundTrip(t, conn2, []byte("after-restart"), 15*time.Second)
}

// TestE2E_Lifecycle_ClosePropagationToTarget: closing the local conn must
// propagate through the tunnel and close the server-side target conn —
// otherwise target conns leak on every client disconnect.
func TestE2E_Lifecycle_ClosePropagationToTarget(t *testing.T) {
	opts := basePairOpts()
	opts.timeout = 30
	p := startPair(t, opts)
	defer p.Close()

	echoAddr, stopEcho, liveConns := startCountedTCPEchoServer(t)
	defer stopEcho()

	// Point the pair's fixed target at the counted echo server.
	p.StopClient()
	c, err := NewClient(p.clientAddr, "127.0.0.1", echoAddr, opts.timeout, p.key, "127.0.0.1",
		false, false, 0, nil, "", "", opts.congestion)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Run(); err != nil {
		t.Fatalf("client Run: %v", err)
	}

	conn, err := waitForPort(p.clientAddr, 10*time.Second)
	if err != nil {
		c.Stop()
		t.Fatalf("wait: %v", err)
	}
	echoRoundTrip(t, conn, []byte("close-propagation-probe"), 15*time.Second)
	if n := liveConns(); n != 1 {
		c.Stop()
		t.Fatalf("expected 1 live target conn, got %d", n)
	}
	conn.Close()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if liveConns() == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if n := liveConns(); n != 0 {
		c.Stop()
		t.Fatalf("target conn not closed after local conn close: %d still live", n)
	}
	c.Stop()
}

// TestE2E_Lifecycle_Socks5IdleHandshakeClosed pins the local SOCKS5 handshake
// watchdog: a conn that connects and goes silent (or stalls mid-greeting) must
// be closed by the listener itself. Without a watchdog such conns pin a
// goroutine and fd forever (slowloris) and even block Client.Stop.
func TestE2E_Lifecycle_Socks5IdleHandshakeClosed(t *testing.T) {
	opts := basePairOpts()
	opts.direct = false
	p := startPair(t, opts)
	defer p.Close()

	origTimeout := socks5HandshakeTimeout
	socks5HandshakeTimeout = time.Second
	t.Cleanup(func() { socks5HandshakeTimeout = origTimeout })

	assertIdleConnClosedByServer(t, p.clientAddr, "socks5/no-bytes", nil)
	assertIdleConnClosedByServer(t, p.clientAddr, "socks5/partial-greeting", []byte{0x05})

	// A healthy handshake after the stalled conns must still work.
	conn := socks5Connect(t, p.clientAddr, p.echoAddr, 5*time.Second)
	defer conn.Close()
	echoRoundTrip(t, conn, []byte("post-stall-socks5"), 10*time.Second)
}

// assertIdleConnClosedByServer connects and goes silent (optionally sending a
// partial handshake first). The listener must close the conn on its own; a
// read error caused by OUR OWN deadline is distinguished from a server-side
// close and fails the assertion.
func assertIdleConnClosedByServer(t *testing.T, addr, label string, preSend []byte) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("%s: dial: %v", label, err)
	}
	defer conn.Close()

	if len(preSend) > 0 {
		if _, err := conn.Write(preSend); err != nil {
			t.Fatalf("%s: pre-send: %v", label, err)
		}
	}

	const testBudget = 6 * time.Second
	_ = conn.SetDeadline(time.Now().Add(testBudget))
	start := time.Now()
	_, rerr := conn.Read(make([]byte, 1))
	elapsed := time.Since(start)
	if rerr == nil {
		t.Fatalf("%s: idle conn was not closed", label)
	}
	if ne, ok := rerr.(net.Error); ok && ne.Timeout() {
		t.Fatalf("%s: hit our own %v read deadline — server never closed the stalled conn: %v",
			label, testBudget, rerr)
	}
	if elapsed > testBudget-time.Second {
		t.Fatalf("%s: close took too long (%v) — watchdog not bounded", label, elapsed)
	}
	t.Logf("%s: server closed stalled conn after %v (%v)", label, elapsed.Round(time.Millisecond), rerr)
}

// TestE2E_SOCKS5_ConnectUserPassAuth covers RFC 1929 username/password auth on
// the SOCKS5 TCP CONNECT path: valid creds proxy data, wrong creds are
// rejected and the conn closes, a no-auth-only client gets X'FF', and the
// listener stays healthy afterwards.
func TestE2E_SOCKS5_ConnectUserPassAuth(t *testing.T) {
	opts := basePairOpts()
	opts.direct = false
	opts.s5user = "ptuser"
	opts.s5pass = "ptpass"
	p := startPair(t, opts)
	defer p.Close()

	// 1. Valid credentials: full handshake + CONNECT + echo.
	good := socks5ConnectAuth(t, p.clientAddr, p.echoAddr, opts.s5user, opts.s5pass, 5*time.Second)
	echoRoundTrip(t, good, []byte("auth-connect-ok"), 10*time.Second)
	good.Close()

	// 2. Wrong password: handshake fails and the server closes the conn.
	raw, err := net.DialTimeout("tcp", p.clientAddr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial for bad-auth: %v", err)
	}
	if err := network.Sock5Handshake(raw.(*net.TCPConn), 3000, opts.s5user, "wrong-password"); err == nil {
		raw.Close()
		t.Fatal("expected RFC1929 auth failure for wrong password")
	}
	_ = raw.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, rerr := raw.Read(make([]byte, 1)); rerr == nil {
		t.Fatal("server kept conn open after failed auth")
	}
	raw.Close()

	// 3. Client offering only NO-AUTH against an auth-required proxy must
	// receive method selection X'FF' (no acceptable methods).
	noAuth, err := net.DialTimeout("tcp", p.clientAddr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial for no-auth offer: %v", err)
	}
	if _, err := noAuth.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("greeting write: %v", err)
	}
	sel := make([]byte, 2)
	if _, err := io.ReadFull(noAuth, sel); err != nil {
		t.Fatalf("method selection read: %v", err)
	}
	if sel[0] != 0x05 || sel[1] != 0xFF {
		t.Fatalf("want method selection {0x05, 0xFF}, got %v", sel)
	}
	noAuth.Close()

	// 4. Rejected attempts must not disturb the listener.
	after := socks5ConnectAuth(t, p.clientAddr, p.echoAddr, opts.s5user, opts.s5pass, 5*time.Second)
	defer after.Close()
	echoRoundTrip(t, after, []byte("auth-still-ok"), 10*time.Second)
}

// TestE2E_Resilience_ShortSoak: sustained short-connection soak with a
// goroutine-leak gate.
func TestE2E_Resilience_ShortSoak(t *testing.T) {
	opts := basePairOpts()
	opts.timeout = 30
	p := startPair(t, opts)
	defer p.Close()

	baseG := runtime.NumGoroutine()
	// 8 workers with a per-iteration pause caps the conn rate (~350/s): each
	// churned conn leaves TIME_WAIT sockets behind and exceeding the host's
	// ephemeral-port range pollutes every test running after this one. The
	// gates below target wedging/corruption/leaks, not conn rate.
	const (
		workers   = 8
		duration  = 10 * time.Second
		msgSize   = 2048
		iterPause = 20 * time.Millisecond
		minOK     = 16
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
			fillPattern(payload, byte(id+3))
			want := sha256.Sum256(payload)
			for time.Now().Before(deadline) {
				time.Sleep(iterPause)
				c, exhausted, err := dialResourceRetry(p.clientAddr, 30*time.Second)
				if err != nil {
					if exhausted {
						exhaustedN.Add(1)
						return
					}
					failN.Add(1)
					recordErr("dial: " + err.Error())
					time.Sleep(10 * time.Millisecond)
					continue
				}
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				_, werr := c.Write(payload)
				got := make([]byte, msgSize)
				_, rerr := io.ReadFull(c, got)
				c.Close()
				if werr != nil || rerr != nil {
					failN.Add(1)
					if werr != nil {
						recordErr("write: " + werr.Error())
					} else {
						recordErr("read: " + rerr.Error())
					}
					c.Close()
					// The server still blacklists a target for 5s when the
					// target itself looks down (refused/unreachable); a hit
					// here KICKs every conn dialled during the window. Back
					// off past the TTL so the soak measures the steady state
					// instead of the blackout.
					time.Sleep(6 * time.Second)
					continue
				}
				if sha256.Sum256(got) != want {
					failN.Add(1)
					integrityN.Add(1)
					recordErr("payload hash mismatch")
					continue
				}
				okN.Add(1)
			}
		}(w)
	}
	wg.Wait()
	// Close-phase goroutines (client + server side) drain asynchronously; on a
	// fast loopback run thousands of short conns finish right at the deadline
	// and their teardown outlives the workers. Give them a bounded window to
	// unwind instead of sampling once.
	waitGoroutinesNear(baseG, 200, 10*time.Second)
	afterG := runtime.NumGoroutine()
	ok, fail := okN.Load(), failN.Load()
	exhausted, integrity := exhaustedN.Load(), integrityN.Load()
	first := "<none>"
	select {
	case first = <-firstErr:
	default:
	}
	t.Logf("soak ok=%d fail=%d integrity=%d resourceExhausted=%d goroutines base=%d after=%d firstErr=%q",
		ok, fail, integrity, exhausted, baseG, afterG, first)

	if integrity > 0 {
		t.Fatalf("payload integrity corrupted during churn: %d of %d echoes", integrity, ok+fail)
	}
	if exhausted > 0 && ok < 50 {
		t.Skipf("local ephemeral port/socket exhaustion prevented the soak run: ok=%d exhausted=%d", ok, exhausted)
	}
	if ok < minOK {
		t.Fatalf("too few successful echoes ok=%d (min %d): wedged proxy or overloaded host, firstErr=%q", ok, minOK, first)
	}
	if fail*10 > ok {
		t.Fatalf("fail rate too high ok=%d fail=%d firstErr=%q", ok, fail, first)
	}
	if afterG > baseG+200 {
		t.Fatalf("goroutine leak? base=%d after=%d", baseG, afterG)
	}
}

// TestE2E_Resilience_ServerMaxConn: once the server hits maxconn it must
// reject new conns (KICK back to the client) instead of dialing targets.
func TestE2E_Resilience_ServerMaxConn(t *testing.T) {
	opts := basePairOpts()
	opts.timeout = 30
	opts.serverMaxConn = 1
	p := startPair(t, opts)
	defer p.Close()

	time.Sleep(2 * time.Second)

	conn1, err := waitForPort(p.clientAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("conn1 dial: %v", err)
	}
	defer conn1.Close()
	echoRoundTrip(t, conn1, []byte("conn-one-ok"), 15*time.Second)

	// Give the server's close phase (~1s tail after a conn is closed) time to
	// settle so the probe conn from startPair is fully gone before conn1.
	time.Sleep(2 * time.Second)

	conn2, err := waitForPort(p.clientAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("conn2 dial: %v", err)
	}
	defer conn2.Close()

	_ = conn2.SetDeadline(time.Now().Add(15 * time.Second))
	msg := []byte("conn-two-must-fail")
	if _, err := conn2.Write(msg); err != nil {
		// Rejected before tunnel setup; an immediate write failure is fine.
		return
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(conn2, got); err == nil {
		t.Fatalf("over-limit conn was served: %q", got)
	}

	// conn1 keeps working: the rejection must not disturb the healthy conn.
	echoRoundTrip(t, conn1, []byte("conn-one-still-ok"), 15*time.Second)
}

// TestE2E_Resilience_ClientMaxConn: the client must refuse to accept new local
// conns beyond maxconn.
func TestE2E_Resilience_ClientMaxConn(t *testing.T) {
	opts := basePairOpts()
	opts.timeout = 30
	opts.clientMaxConn = 1
	p := startPair(t, opts)
	defer p.Close()

	time.Sleep(2 * time.Second)

	conn1, err := waitForPort(p.clientAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("conn1 dial: %v", err)
	}
	defer conn1.Close()
	echoRoundTrip(t, conn1, []byte("conn-one-ok"), 15*time.Second)

	time.Sleep(2 * time.Second)

	conn2, err := waitForPort(p.clientAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("conn2 dial: %v", err)
	}
	defer conn2.Close()

	_ = conn2.SetDeadline(time.Now().Add(15 * time.Second))
	msg := []byte("conn-two-must-fail")
	if _, err := conn2.Write(msg); err != nil {
		return
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(conn2, got); err == nil {
		t.Fatalf("over-limit local conn was served: %q", got)
	}
}
