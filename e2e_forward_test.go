package pingtunnel

import (
	"bufio"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// E2E coverage for the server's upstream-proxy mode (-forward): all target
// dials — TCP CONNECT and UDP datagrams — must go through the configured
// socks5:// or http:// proxy instead of direct sockets. The doubles below are
// minimal RFC 1928 / HTTP CONNECT proxies with counters, so each test can
// prove the traffic actually transited the proxy.

// --- SOCKS5 proxy double -------------------------------------------------------

type socks5ProxyDouble struct {
	cfg       *ForwardConfig
	connects  atomic.Int64 // TCP CONNECT commands relayed
	udpFlows  atomic.Int64 // client->target UDP datagrams relayed
	udpAssocs atomic.Int64 // UDP ASSOCIATE commands served
}

func startSocks5ProxyDouble(t *testing.T) *socks5ProxyDouble {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("socks5 double listen: %v", err)
	}
	pd := &socks5ProxyDouble{
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
			go pd.serve(c)
		}
	}()
	t.Cleanup(func() { close(done); ln.Close() })
	return pd
}

func (pd *socks5ProxyDouble) serve(c net.Conn) {
	defer c.Close()

	// Server side of the RFC 1928 greeting: offer no-auth only.
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil || hdr[0] != socks5Version {
		return
	}
	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(c, methods); err != nil {
		return
	}
	if _, err := c.Write([]byte{socks5Version, 0x00}); err != nil {
		return
	}

	req, err := readSocks5Request(c)
	if err != nil {
		return
	}

	switch req.Command {
	case socks5CmdConnect:
		pd.connects.Add(1)
		target, err := net.DialTimeout("tcp", req.Address, 5*time.Second)
		if err != nil {
			_ = writeSocks5Reply(c, socks5ReplyGeneralFailure, "0.0.0.0:0")
			return
		}
		defer target.Close()
		if err := writeSocks5Reply(c, socks5ReplySucceeded, "0.0.0.0:0"); err != nil {
			return
		}
		pipeBothAndClose(c, target)
	case socks5CmdUDPAssociate:
		pd.udpAssocs.Add(1)
		relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			_ = writeSocks5Reply(c, socks5ReplyGeneralFailure, "0.0.0.0:0")
			return
		}
		defer relay.Close()
		bind := relay.LocalAddr().(*net.UDPAddr).String()
		if err := writeSocks5Reply(c, socks5ReplySucceeded, bind); err != nil {
			return
		}
		// The association lives as long as the control conn stays open; drain
		// it so closure is detected and the relay socket is released.
		controlClosed := make(chan struct{})
		go func() {
			_, _ = io.Copy(io.Discard, c)
			close(controlClosed)
		}()
		pd.relayUDP(relay, controlClosed)
	default:
		_ = writeSocks5Reply(c, socks5ReplyCommandNotSupported, "0.0.0.0:0")
	}
}

// relayUDP forwards between the tunnel server (socks5-wrapped datagrams, from
// the peer learned on first packet) and the real targets (raw UDP replies).
func (pd *socks5ProxyDouble) relayUDP(relay *net.UDPConn, stop <-chan struct{}) {
	buf := make([]byte, 65535)
	var peer *net.UDPAddr
	for {
		select {
		case <-stop:
			return
		default:
		}
		_ = relay.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, src, err := relay.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		if peer == nil || (src.IP.Equal(peer.IP) && src.Port == peer.Port) {
			// Wrapped datagram from the tunnel server.
			target, payload, perr := parseSocks5UDPDatagram(buf[:n])
			if perr != nil {
				continue
			}
			dst, rerr := net.ResolveUDPAddr("udp", target)
			if rerr != nil {
				continue
			}
			if peer == nil {
				peer = copyUDPAddr(src)
			}
			pd.udpFlows.Add(1)
			_, _ = relay.WriteToUDP(payload, dst)
		} else {
			// Raw reply from a target: wrap the source address and send it
			// back to the tunnel server.
			out, werr := buildSocks5UDPDatagram(src.String(), buf[:n])
			if werr != nil {
				continue
			}
			_, _ = relay.WriteToUDP(out, peer)
		}
	}
}

// --- HTTP CONNECT proxy double ---------------------------------------------------

type httpConnectProxyDouble struct {
	cfg      *ForwardConfig
	connects atomic.Int64
}

func startHTTPConnectProxyDouble(t *testing.T) *httpConnectProxyDouble {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("http double listen: %v", err)
	}
	pd := &httpConnectProxyDouble{
		cfg: &ForwardConfig{Scheme: "http", Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port},
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
			go pd.serve(c)
		}
	}()
	t.Cleanup(func() { close(done); ln.Close() })
	return pd
}

func (pd *httpConnectProxyDouble) serve(c net.Conn) {
	defer c.Close()

	br := bufio.NewReader(c)
	reqLine, err := br.ReadString('\n')
	if err != nil {
		return
	}
	fields := strings.Fields(strings.TrimSpace(reqLine))
	if len(fields) != 3 || fields[0] != "CONNECT" {
		return
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	pd.connects.Add(1)
	target, err := net.DialTimeout("tcp", fields[1], 5*time.Second)
	if err != nil {
		_, _ = c.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer target.Close()
	if _, err := c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		return
	}
	pipeBothAndClose(c, target)
}

// --- shared helpers -------------------------------------------------------------

func pipeBothAndClose(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	<-done
	_ = a.Close()
	_ = b.Close()
}

// --- tests ------------------------------------------------------------------------

// TestE2E_Forward_Socks5TCPTunnel: with a socks5 upstream configured, the
// server must dial the target through the proxy (observed via the double's
// CONNECT counter) and relay intact payload both ways.
func TestE2E_Forward_Socks5TCPTunnel(t *testing.T) {
	proxy := startSocks5ProxyDouble(t)

	opts := basePairOpts()
	opts.forward = proxy.cfg
	p := startPair(t, opts)
	defer p.Close()

	conn, err := waitForPort(p.clientAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial local proxy port: %v", err)
	}
	defer conn.Close()

	echoRoundTrip(t, conn, []byte("hello via socks5 upstream"), 15*time.Second)

	payload := make([]byte, 256*1024)
	fillPattern(payload, 21)
	echoRoundTrip(t, conn, payload, 30*time.Second)

	if n := proxy.connects.Load(); n < 1 {
		t.Fatalf("target was not dialed through the socks5 upstream (CONNECT count=%d)", n)
	}
	t.Logf("socks5 upstream CONNECTs=%d", proxy.connects.Load())
}

// TestE2E_Forward_HttpConnectTunnel: same as above through an HTTP CONNECT
// upstream.
func TestE2E_Forward_HttpConnectTunnel(t *testing.T) {
	proxy := startHTTPConnectProxyDouble(t)

	opts := basePairOpts()
	opts.forward = proxy.cfg
	p := startPair(t, opts)
	defer p.Close()

	conn, err := waitForPort(p.clientAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial local proxy port: %v", err)
	}
	defer conn.Close()

	echoRoundTrip(t, conn, []byte("hello via http connect upstream"), 15*time.Second)

	payload := make([]byte, 128*1024)
	fillPattern(payload, 33)
	echoRoundTrip(t, conn, payload, 30*time.Second)

	if n := proxy.connects.Load(); n < 1 {
		t.Fatalf("target was not dialed through the http upstream (CONNECT count=%d)", n)
	}
	t.Logf("http upstream CONNECTs=%d", proxy.connects.Load())
}

// TestE2E_Forward_Socks5UDPTunnel: direct UDP forwarding through a socks5
// upstream must use UDP ASSOCIATE and the wrapped datagram relay.
func TestE2E_Forward_Socks5UDPTunnel(t *testing.T) {
	proxy := startSocks5ProxyDouble(t)

	opts := basePairOpts()
	opts.udp = true
	opts.forward = proxy.cfg
	p := startPair(t, opts)
	defer p.Close()

	rConn, err := net.Dial("udp", p.clientAddr)
	if err != nil {
		t.Fatalf("failed to dial UDP proxy: %v", err)
	}
	defer rConn.Close()

	msg := []byte("hello udp via socks5 upstream")
	deadline := time.Now().Add(15 * time.Second)
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
	if string(reply[:n]) != string(msg) {
		t.Fatalf("UDP reply mismatch: got %q, want %q", reply[:n], msg)
	}

	if a := proxy.udpAssocs.Load(); a < 1 {
		t.Fatalf("udp forwarding did not use UDP ASSOCIATE (count=%d)", a)
	}
	if f := proxy.udpFlows.Load(); f < 1 {
		t.Fatalf("no datagram transited the socks5 udp relay (flows=%d)", f)
	}
	t.Logf("socks5 upstream UDP ASSOCIATEs=%d flows=%d", proxy.udpAssocs.Load(), proxy.udpFlows.Load())
}

// TestE2E_Forward_HttpUDPRejected: UDP forwarding over an http:// upstream is
// unsupported and must be rejected server-side — no datagram may be echoed and
// the http proxy must never see a TCP CONNECT for it.
func TestE2E_Forward_HttpUDPRejected(t *testing.T) {
	proxy := startHTTPConnectProxyDouble(t)

	opts := basePairOpts()
	opts.udp = true
	opts.forward = proxy.cfg
	p := startPair(t, opts)
	defer p.Close()

	rConn, err := net.Dial("udp", p.clientAddr)
	if err != nil {
		t.Fatalf("failed to dial UDP proxy: %v", err)
	}
	defer rConn.Close()

	msg := []byte("this udp must never be echoed")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_ = rConn.SetDeadline(time.Now().Add(300 * time.Millisecond))
		if _, err := rConn.Write(msg); err != nil {
			t.Fatalf("udp write: %v", err)
		}
		buf := make([]byte, 2048)
		if n, err := rConn.Read(buf); err == nil && n > 0 {
			t.Fatalf("udp datagram was echoed through an http upstream: %q", buf[:n])
		}
		time.Sleep(100 * time.Millisecond)
	}

	if n := proxy.connects.Load(); n != 0 {
		t.Fatalf("http upstream received %d CONNECTs for a udp-mode pair, want 0", n)
	}
	t.Logf("http+udp correctly rejected: %s CONNECTs=%d",
		net.JoinHostPort(proxy.cfg.Host, strconv.Itoa(proxy.cfg.Port)), proxy.connects.Load())
}
