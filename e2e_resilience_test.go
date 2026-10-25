package pingtunnel

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Gap-filling scenarios for paths that had implementation but no positive e2e
// verification: unreachable targets (KICK + 5s blacklist window), idle-conn
// reaping by the inactivity timeout, server-restart recovery, multi-client
// isolation on one server, and the inline-processing / congestion-off
// parameter variants.

// socks5ConnectMustFail opens a SOCKS5 CONNECT to target and requires the conn
// to die soon after (the tunnel refused it). Returns the elapsed time from the
// completed local handshake to the failure — the observable stand-in for how
// long the server spent on the target dial.
func socks5ConnectMustFail(t *testing.T, proxyAddr, targetAddr string, failWithin time.Duration) time.Duration {
	t.Helper()
	conn := socks5Connect(t, proxyAddr, targetAddr, 10*time.Second)
	defer conn.Close()

	start := time.Now()
	_ = conn.SetDeadline(time.Now().Add(failWithin))
	probe := []byte("unreachable-probe")
	if _, err := conn.Write(probe); err != nil {
		// Rejected before the tunnel was set up; the write itself failed.
		return time.Since(start)
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatalf("conn to unreachable target %s was served", targetAddr)
	}
	return time.Since(start)
}

// silentProxyDouble accepts TCP conns but never answers the proxy handshake:
// the server's DialThroughProxy then fails deterministically on the conntt
// deadline — an "unreachable target" that needs no external network (a
// TEST-NET address is dialable on hosts with transparent proxies/VPNs, which
// makes 192.0.2.x unusable for this scenario).
type silentProxyDouble struct {
	cfg     *ForwardConfig
	accepts atomic.Int64
}

func startSilentProxyDouble(t *testing.T) *silentProxyDouble {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("silent proxy listen: %v", err)
	}
	pd := &silentProxyDouble{
		cfg: &ForwardConfig{Scheme: "socks5", Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port},
	}
	done := make(chan struct{})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case <-done:
				c.Close()
				return
			default:
			}
			pd.accepts.Add(1)
			// Swallow bytes, never write: handshakes stall until the deadline.
			go func(c net.Conn) {
				_, _ = io.Copy(io.Discard, c)
				c.Close()
			}(c)
		}
	}()
	t.Cleanup(func() { close(done); ln.Close() })
	return pd
}

// TestE2E_Resilience_TargetUnreachableBlacklist: a target the server cannot
// reach must KICK the conn back promptly (no hang, no zombie local conn). The
// failed target is blacklisted for 5s: an immediate retry fails without
// re-dialing (observed directly on the proxy's accept counter), other targets
// are unaffected, and once the TTL lapses the dial is attempted again.
func TestE2E_Resilience_TargetUnreachableBlacklist(t *testing.T) {
	const (
		dialTimeoutMs  = 1500
		blackholeFloor = 1200 * time.Millisecond // a real dial waits out the full conntt
	)
	proxy := startSilentProxyDouble(t)

	opts := basePairOpts()
	opts.direct = false // SOCKS5: per-conn targets on one pair
	opts.forward = proxy.cfg
	opts.conntt = dialTimeoutMs
	p := startPair(t, opts)
	defer p.Close()

	targetA := p.echoAddr
	targetBAddr, stopEchoB := startTCPEchoServer(t)
	defer stopEchoB()

	// 1. First attempt: a real dial through the proxy that times out, KICK.
	elapsed1 := socks5ConnectMustFail(t, p.clientAddr, targetA, 15*time.Second)
	accepts1 := proxy.accepts.Load()
	t.Logf("first attempt failed in %v, proxy accepts=%d", elapsed1, accepts1)
	if accepts1 != 1 {
		t.Fatalf("expected exactly 1 proxy dial on first attempt, got %d", accepts1)
	}
	if elapsed1 < blackholeFloor {
		t.Fatalf("dial through silent proxy failed in %v, want it to wait out the %dms deadline", elapsed1, dialTimeoutMs)
	}

	// 2. Immediate retry inside the 5s blacklist window: KICK, no re-dial.
	elapsed2 := socks5ConnectMustFail(t, p.clientAddr, targetA, 15*time.Second)
	accepts2 := proxy.accepts.Load()
	t.Logf("retry during blacklist window failed in %v, proxy accepts=%d", elapsed2, accepts2)
	if accepts2 != accepts1 {
		t.Fatalf("blacklisted retry re-dialed the target (proxy accepts %d -> %d)", accepts1, accepts2)
	}
	if elapsed2 >= blackholeFloor {
		t.Fatalf("blacklisted retry took %v, want a fast KICK without a dial", elapsed2)
	}

	// 3. A different target is NOT blacklisted: the server dials for it.
	elapsedB := socks5ConnectMustFail(t, p.clientAddr, targetBAddr, 15*time.Second)
	accepts3 := proxy.accepts.Load()
	t.Logf("other-target attempt failed in %v, proxy accepts=%d", elapsedB, accepts3)
	if accepts3 != accepts2+1 {
		t.Fatalf("other target did not get its own dial (proxy accepts %d -> %d)", accepts2, accepts3)
	}

	// 4. Wait past the 5s blacklist TTL: the dial is attempted again.
	time.Sleep(6 * time.Second)
	elapsed3 := socks5ConnectMustFail(t, p.clientAddr, targetA, 15*time.Second)
	accepts4 := proxy.accepts.Load()
	t.Logf("retry after blacklist TTL failed in %v, proxy accepts=%d", elapsed3, accepts4)
	if accepts4 != accepts3+1 {
		t.Fatalf("blacklist never expired: post-TTL retry did not re-dial (proxy accepts %d -> %d)", accepts3, accepts4)
	}
	if elapsed3 < blackholeFloor {
		t.Fatalf("post-TTL retry failed in %v without waiting out the dial deadline", elapsed3)
	}

	// 5. The server's packet path is still alive after the whole cycle.
	if p.client != nil && p.client.RTT() <= 0 {
		t.Fatalf("server looks dead after the failure cycle (no fresh RTT)")
	}
}

// countSyncMap entries for idle-reap assertions (client conn map).
func countSyncMap(m *sync.Map) int {
	n := 0
	m.Range(func(_, _ interface{}) bool { n++; return true })
	return n
}

// TestE2E_Lifecycle_IdleConnReaped: a TCP conn that goes fully silent must be
// reaped by the inactivity timeout on BOTH ends — the local app sees the conn
// close (not hang forever) and the target-side conn is released too.
func TestE2E_Lifecycle_IdleConnReaped(t *testing.T) {
	opts := basePairOpts()
	opts.timeout = 3
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
	p.client = c

	conn, err := waitForPort(p.clientAddr, 10*time.Second)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	defer conn.Close()

	// Prove the conn is alive and tracked, then go fully silent.
	echoRoundTrip(t, conn, []byte("before-idle"), 15*time.Second)
	if n := liveConns(); n != 1 {
		t.Fatalf("expected 1 live target conn, got %d", n)
	}

	start := time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("idle conn was still readable after the inactivity window")
	}
	elapsed := time.Since(start)
	if elapsed > 15*time.Second {
		t.Fatalf("idle conn took %s to be reaped (timeout=3s) — likely never reaped until our deadline", elapsed)
	}
	t.Logf("idle conn reaped after %s (timeout=3s)", elapsed)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if liveConns() == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if n := liveConns(); n != 0 {
		t.Fatalf("target conn not released after idle reap: %d still live", n)
	}
}

// TestE2E_Lifecycle_UDPIdleConnReaped: UDP flows idle for longer than the
// timeout must be reaped on both sides, and the same client socket must get a
// fresh flow afterwards (the reap must not wedge the tunnel).
func TestE2E_Lifecycle_UDPIdleConnReaped(t *testing.T) {
	opts := basePairOpts()
	opts.udp = true
	opts.timeout = 3
	p := startPair(t, opts)
	defer p.Close()

	rConn, err := net.Dial("udp", p.clientAddr)
	if err != nil {
		t.Fatalf("failed to dial UDP proxy: %v", err)
	}
	defer rConn.Close()

	udpEchoOnce := func(label string, timeout time.Duration) {
		t.Helper()
		msg := []byte("udp-idle-" + label)
		deadline := time.Now().Add(timeout)
		reply := make([]byte, 2048)
		for time.Now().Before(deadline) {
			_ = rConn.SetDeadline(time.Now().Add(300 * time.Millisecond))
			if _, err := rConn.Write(msg); err != nil {
				t.Fatalf("udp write: %v", err)
			}
			n, rerr := rConn.Read(reply)
			if rerr == nil && n > 0 {
				if string(reply[:n]) != string(msg) {
					t.Fatalf("udp echo mismatch: got %q want %q", reply[:n], msg)
				}
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("udp echo %s timed out", label)
	}

	udpEchoOnce("before", 10*time.Second)

	// Wait until both ends have dropped the idle flow.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		clientConns := countSyncMap(&p.client.localIdToConnMap)
		serverConns := int(p.server.connCount.Load())
		if clientConns == 0 && serverConns == 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if got := countSyncMap(&p.client.localIdToConnMap); got != 0 {
		t.Fatalf("client still holds %d idle udp conns after the timeout window", got)
	}
	if got := int(p.server.connCount.Load()); got != 0 {
		t.Fatalf("server still holds %d idle udp conns after the timeout window", got)
	}

	// A fresh flow from the same socket must still be served.
	udpEchoOnce("after-reap", 10*time.Second)
}

// TestE2E_Lifecycle_ServerRestartRecovery: after the server process is
// replaced, the still-running client must transparently serve new conns
// through the fresh server without a restart.
func TestE2E_Lifecycle_ServerRestartRecovery(t *testing.T) {
	opts := basePairOpts()
	opts.timeout = 30
	p := startPair(t, opts)
	defer p.Close()

	conn, err := waitForPort(p.clientAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("wait1: %v", err)
	}
	echoRoundTrip(t, conn, []byte("before-server-restart"), 15*time.Second)
	conn.Close()

	p.server.Stop()
	p.server = nil
	time.Sleep(300 * time.Millisecond)

	conntt := opts.conntt
	if conntt <= 0 {
		conntt = 5000
	}
	s2, err := NewServer("127.0.0.1", p.key, opts.serverMaxConn, conntt, nil, nil, opts.congestion)
	if err != nil {
		t.Fatalf("NewServer after restart: %v", err)
	}
	p.server = s2
	if err := s2.Run(); err != nil {
		t.Fatalf("server Run after restart: %v", err)
	}

	// New conns through the old client must echo again. Generous deadline:
	// recovery depends on the client's ping/traffic cycle noticing the server.
	conn2, err := waitForPort(p.clientAddr, 10*time.Second)
	if err != nil {
		t.Fatalf("wait2: %v", err)
	}
	defer conn2.Close()
	echoRoundTrip(t, conn2, []byte("after-server-restart"), 30*time.Second)

	conn3, err := waitForPort(p.clientAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("wait3: %v", err)
	}
	defer conn3.Close()
	payload := make([]byte, 128*1024)
	fillPattern(payload, 17)
	echoRoundTrip(t, conn3, payload, 30*time.Second)
}

// TestE2E_Lifecycle_TwoClientsOneServer: two clients sharing one server (same
// key, distinct random ICMP echo ids) must relay concurrently without
// cross-talk — a misrouted frame would fail the other client's conn.
func TestE2E_Lifecycle_TwoClientsOneServer(t *testing.T) {
	opts := basePairOpts()
	opts.timeout = 30
	p := startPair(t, opts)
	defer p.Close()

	addr2 := fmt.Sprintf("127.0.0.1:%d", getFreePort(t))
	c2, err := NewClient(addr2, "127.0.0.1", p.echoAddr, opts.timeout, p.key, "127.0.0.1",
		false, false, 0, nil, "", "", opts.congestion)
	if err != nil {
		t.Fatalf("NewClient second client: %v", err)
	}
	if c2.id == p.client.id {
		c2.Stop()
		t.Skipf("random echo id collision (%d); isolation not observable this run", c2.id)
	}
	if err := c2.Run(); err != nil {
		t.Fatalf("second client Run: %v", err)
	}
	defer c2.Stop()

	connA, err := waitForPort(p.clientAddr, 10*time.Second)
	if err != nil {
		t.Fatalf("client A dial: %v", err)
	}
	defer connA.Close()
	connB, err := waitForPort(addr2, 10*time.Second)
	if err != nil {
		t.Fatalf("client B dial: %v", err)
	}
	defer connB.Close()

	// Interleaved small echoes with distinct payloads.
	for i := 0; i < 10; i++ {
		echoRoundTrip(t, connA, []byte(fmt.Sprintf("clientA-echo-%d", i)), 15*time.Second)
		echoRoundTrip(t, connB, []byte(fmt.Sprintf("clientB-echo-%d", i)), 15*time.Second)
	}

	// And a moderate payload on each, concurrently.
	payloadA := make([]byte, 96*1024)
	fillPattern(payloadA, 41)
	payloadB := make([]byte, 96*1024)
	fillPattern(payloadB, 77)
	wantB := sha256.Sum256(payloadB)

	errCh := make(chan error, 2)
	go func() {
		echoRoundTrip(t, connA, payloadA, 30*time.Second)
		errCh <- nil
	}()
	go func() {
		_ = connB.SetDeadline(time.Now().Add(30 * time.Second))
		if _, err := connB.Write(payloadB); err != nil {
			errCh <- fmt.Errorf("client B write: %w", err)
			return
		}
		got := make([]byte, len(payloadB))
		if _, err := io.ReadFull(connB, got); err != nil {
			errCh <- fmt.Errorf("client B read: %w", err)
			return
		}
		if sha256.Sum256(got) != wantB {
			errCh <- fmt.Errorf("client B payload mismatch (cross-talk?)")
			return
		}
		errCh <- nil
	}()
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("concurrent transfer failed: %v", err)
		}
	}
}

// TestE2E_Server_CongestionDisabled: congestion="" runs the FrameMgr without
// the BB controller — the plain retransmit pacing must still deliver a
// multi-MiB stream intact.
func TestE2E_Server_CongestionDisabled(t *testing.T) {
	const totalBytes = int64(4) << 20
	targetAddr, stopTarget := startStreamVerifyServer(t, 0)
	defer stopTarget()

	opts := basePairOpts()
	opts.direct = false
	opts.congestion = ""
	opts.timeout = 15
	p := startPair(t, opts)
	defer p.Close()

	conn := socks5Connect(t, p.clientAddr, targetAddr, 10*time.Second)
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	start := time.Now()

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
			t.Fatalf("download died at %d/%d: %v", received, totalBytes, err)
		}
	}
	var gotSum, wantSum [32]byte
	copy(gotSum[:], gotHash.Sum(nil))
	copy(wantSum[:], wantHash.Sum(nil))
	if gotSum != wantSum {
		t.Fatalf("congestion-off download sha256 mismatch")
	}
	t.Logf("%d MiB download verified with congestion disabled in %s", totalBytes>>20, time.Since(start))
}
