package pingtunnel

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/esrrhs/gohome/network"
)

// UDP-path scenarios: direct UDP datagram forwarding and the SOCKS5 UDP
// ASSOCIATE relay, mirrored from spp's e2e_udp_test.go.

func TestE2E_UDP_ForwardProxy(t *testing.T) {
	opts := basePairOpts()
	opts.udp = true
	p := startPair(t, opts)
	defer p.Close()

	rConn, err := net.Dial("udp", p.clientAddr)
	if err != nil {
		t.Fatalf("failed to dial UDP proxy: %v", err)
	}
	defer rConn.Close()

	msg := []byte("hello pingtunnel udp forward proxy")
	deadline := time.Now().Add(8 * time.Second)
	reply := make([]byte, 2048)
	var n int
	for time.Now().Before(deadline) {
		_ = rConn.SetDeadline(time.Now().Add(300 * time.Millisecond))
		if _, err := rConn.Write(msg); err != nil {
			t.Fatalf("udp write: %v", err)
		}
		n, err = rConn.Read(reply)
		if err == nil && n > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("failed to read UDP reply within deadline: %v", err)
	}
	if !bytes.Equal(reply[:n], msg) {
		t.Fatalf("UDP reply mismatch: got %q, want %q", reply[:n], msg)
	}
}

func socks5UDPAssociate(t *testing.T, socksAddr string, timeout time.Duration) (ctrl net.Conn, relayAddr string) {
	t.Helper()
	c, err := net.Dial("tcp", socksAddr)
	if err != nil {
		t.Fatalf("dial socks5 failed: %v", err)
	}
	tcpConn := c.(*net.TCPConn)
	if err := network.Sock5Handshake(tcpConn, int(timeout/time.Millisecond), "", ""); err != nil {
		tcpConn.Close()
		t.Fatalf("SOCKS5 handshake failed: %v", err)
	}
	relayAddr, err = network.Sock5SetUDPRequest(tcpConn, "0.0.0.0", 0, int(timeout/time.Millisecond))
	if err != nil {
		tcpConn.Close()
		t.Fatalf("Sock5SetUDPRequest failed: %v", err)
	}
	if relayAddr == "" {
		tcpConn.Close()
		t.Fatalf("empty relay address returned")
	}
	return tcpConn, relayAddr
}

func udpAssociateEcho(t *testing.T, relayAddr, echoAddr string, msg []byte, timeout time.Duration) {
	t.Helper()
	echoHost, echoPortStr, err := net.SplitHostPort(echoAddr)
	if err != nil {
		t.Fatalf("SplitHostPort failed: %v", err)
	}
	echoPort, _ := strconv.Atoi(echoPortStr)

	udpConn, err := net.Dial("udp", relayAddr)
	if err != nil {
		t.Fatalf("dial UDP relay failed: %v", err)
	}
	defer udpConn.Close()

	pkt, err := network.Sock5PackUDP(echoHost, echoPort, msg)
	if err != nil {
		t.Fatalf("Sock5PackUDP failed: %v", err)
	}

	replyBuf := make([]byte, 2048)
	deadline := time.Now().Add(timeout)
	var n int
	for time.Now().Before(deadline) {
		_ = udpConn.SetDeadline(time.Now().Add(300 * time.Millisecond))
		if _, err := udpConn.Write(pkt); err != nil {
			t.Fatalf("udpConn write failed: %v", err)
		}
		n, err = udpConn.Read(replyBuf)
		if err == nil && n > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("failed to read UDP relay reply within deadline: %v", err)
	}

	host, port, data, err := network.Sock5UnpackUDP(replyBuf[:n])
	if err != nil {
		t.Fatalf("Sock5UnpackUDP failed: %v", err)
	}
	if host != echoHost || port != echoPort {
		t.Fatalf("UDP reply source mismatch: got %s:%d, want %s:%d", host, port, echoHost, echoPort)
	}
	if !bytes.Equal(data, msg) {
		t.Fatalf("UDP reply payload mismatch: got %q, want %q", data, msg)
	}
}

func TestE2E_SOCKS5_UDPAssociate(t *testing.T) {
	opts := basePairOpts()
	opts.direct = false
	p := startPair(t, opts)
	defer p.Close()

	udpEchoAddr, stopUDPEcho := startUDPEchoServer(t)
	defer stopUDPEcho()

	ctrl, relayAddr := socks5UDPAssociate(t, p.clientAddr, 5*time.Second)
	defer ctrl.Close()

	udpAssociateEcho(t, relayAddr, udpEchoAddr, []byte("hello socks5 udp associate proxy"), 8*time.Second)
}

func TestE2E_SOCKS5_SimultaneousTCPAndUDP(t *testing.T) {
	opts := basePairOpts()
	opts.direct = false
	p := startPair(t, opts)
	defer p.Close()

	tcpDone := make(chan error, 1)
	go func() {
		conn, err := socks5ConnectErr(p.clientAddr, p.echoAddr, 10*time.Second)
		if err != nil {
			tcpDone <- err
			return
		}
		defer conn.Close()
		msg := []byte("hello simultaneous tcp")
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err := conn.Write(msg); err != nil {
			tcpDone <- err
			return
		}
		buf := make([]byte, len(msg))
		if _, err := io.ReadFull(conn, buf); err != nil {
			tcpDone <- err
			return
		}
		if !bytes.Equal(buf, msg) {
			tcpDone <- fmt.Errorf("tcp echo mismatch: got %q want %q", buf, msg)
			return
		}
		tcpDone <- nil
	}()

	udpEchoAddr, stopUDPEcho := startUDPEchoServer(t)
	defer stopUDPEcho()

	udpDone := make(chan error, 1)
	go func() {
		ctrl, relayAddr := socks5UDPAssociate(t, p.clientAddr, 5*time.Second)
		defer ctrl.Close()
		udpAssociateEcho(t, relayAddr, udpEchoAddr, []byte("hello simultaneous udp"), 8*time.Second)
		udpDone <- nil
	}()

	select {
	case err := <-tcpDone:
		if err != nil {
			t.Fatalf("TCP test failed: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("TCP test timed out")
	}
	select {
	case err := <-udpDone:
		if err != nil {
			t.Fatalf("UDP test failed: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("UDP test timed out")
	}
}

func TestE2E_SOCKS5_UDPAssociateAuth(t *testing.T) {
	opts := basePairOpts()
	opts.direct = false
	opts.s5user = "myuser"
	opts.s5pass = "mypassword"
	p := startPair(t, opts)
	defer p.Close()

	// 1. Wrong credentials must fail the handshake and the server closes the conn.
	cBad, err := net.Dial("tcp", p.clientAddr)
	if err != nil {
		t.Fatalf("dial socks5 failed: %v", err)
	}
	if err := network.Sock5Handshake(cBad.(*net.TCPConn), 3000, "myuser", "wrongpass"); err == nil {
		cBad.Close()
		t.Fatalf("expected SOCKS5 handshake with wrong password to fail")
	}
	_ = cBad.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, rerr := cBad.Read(make([]byte, 1)); rerr == nil {
		t.Fatalf("server kept conn open after failed auth")
	}
	cBad.Close()

	// 2. Correct credentials complete the handshake and relay UDP.
	c, err := net.Dial("tcp", p.clientAddr)
	if err != nil {
		t.Fatalf("dial socks5 failed: %v", err)
	}
	defer c.Close()
	tcpConn := c.(*net.TCPConn)
	if err := network.Sock5Handshake(tcpConn, 3000, "myuser", "mypassword"); err != nil {
		t.Fatalf("SOCKS5 handshake with correct credentials failed: %v", err)
	}
	relayAddr, err := network.Sock5SetUDPRequest(tcpConn, "0.0.0.0", 0, 3000)
	if err != nil {
		t.Fatalf("Sock5SetUDPRequest failed: %v", err)
	}
	udpEchoAddr, stopUDPEcho := startUDPEchoServer(t)
	defer stopUDPEcho()
	udpAssociateEcho(t, relayAddr, udpEchoAddr, []byte("hello authenticated socks5 udp"), 8*time.Second)
}

// TestE2E_SOCKS5_UDPAssociate_CloseReleasesHeldConns: held silent associations
// must be reclaimed when the client stops — the control conns must see EOF
// promptly and the goroutines must be recovered.
func TestE2E_SOCKS5_UDPAssociate_CloseReleasesHeldConns(t *testing.T) {
	const holders = 8

	opts := basePairOpts()
	opts.direct = false
	p := startPair(t, opts)
	defer p.Close()

	baseG := runtime.NumGoroutine()

	conns := make([]net.Conn, 0, holders)
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	for i := 0; i < holders; i++ {
		ctrl, _ := socks5UDPAssociate(t, p.clientAddr, 5*time.Second)
		conns = append(conns, ctrl)
	}

	p.StopClient()

	// The held control conn must be closed by the client side: our side sees
	// EOF promptly instead of blocking until the read deadline.
	start := time.Now()
	_ = conns[0].SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conns[0].Read(make([]byte, 64)); err == nil {
		t.Fatal("control conn still open after client.Stop")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("control conn not closed by client.Stop: read blocked %s", elapsed)
	}

	if !waitGoroutinesNear(baseG, 10, 10*time.Second) {
		t.Fatalf("goroutines not recovered after client.Stop with %d held associations: g=%d base=%d",
			holders, runtime.NumGoroutine(), baseG)
	}
	t.Logf("held associations=%d released; g=%d base=%d", holders, runtime.NumGoroutine(), baseG)
}
