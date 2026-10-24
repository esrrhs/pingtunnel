package pingtunnel

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/esrrhs/gohome/loggo"
	"github.com/esrrhs/gohome/network"
)

// E2E harness for pingtunnel: boots a real Server+Client pair in-process over
// ICMP on loopback and drives traffic through the local TCP/UDP/SOCKS5
// listener to echo and stream-verify targets. Mirrors the spp proxy E2E suite.
//
// ICMP raw sockets need root/CAP_NET_RAW; without it every test skips (the
// gate mirrors spp's skipIfUnderlayUnavailable). Run with:
//
//	go test -c -o /tmp/pingtunnel-e2e.test . && sudo /tmp/pingtunnel-e2e.test -test.run 'TestE2E' -test.v

func init() {
	level := loggo.LEVEL_WARN
	if v := strings.TrimSpace(os.Getenv("PINGTUNNEL_E2E_LOGLEVEL")); v != "" {
		if l := loggo.NameToLevel(v); l >= 0 {
			level = l
		}
	}
	loggo.Ini(loggo.Config{
		Level:     level,
		Prefix:    "pingtunnel-e2e",
		MaxDay:    1,
		NoLogFile: true,
		NoPrint:   false,
	})
}

// --- ICMP underlay ------------------------------------------------------------
//
// With root the suite runs over real raw ICMP sockets on loopback. Without it,
// the whole suite still runs over an in-process fake underlay that speaks the
// same wire format (a marshaled icmp.Message per datagram) over UDP loopback:
// the full client/server/frame/socks5 path is exercised, minus the kernel's
// echo-reply behavior. Set PINGTUNNEL_E2E_REQUIRE_REAL_ICMP=1 to skip instead.

var (
	icmpGateOnce sync.Once
	icmpRealOK   bool
	icmpFakeOK   bool
)

func ensureICMPUnderlay(t *testing.T) {
	t.Helper()
	icmpGateOnce.Do(func() {
		if c, err := listenICMP("127.0.0.1"); err == nil {
			c.Close()
			icmpRealOK = true
			return
		}
		if os.Getenv("PINGTUNNEL_E2E_REQUIRE_REAL_ICMP") == "1" {
			return
		}
		icmpListen = func(addr string) (icmpPacketConn, error) { return fakeICMPHub.listen(addr) }
		icmpFakeOK = true
	})
	switch {
	case icmpRealOK:
		t.Log("ICMP underlay: real raw socket (privileged)")
	case icmpFakeOK:
		t.Log("ICMP underlay: in-process fake (no root available)")
	default:
		t.Skip("ICMP e2e requires root/CAP_NET_RAW for real raw sockets")
	}
}

// fakeICMPHubT resolves the ICMP "send to an IP with no port" model: every
// endpoint receives every packet except its own, mirroring how all raw ICMP
// sockets on one host see all loopback traffic. Per-pair keys provide isolation.
type fakeICMPHubT struct {
	mu  sync.Mutex
	eps []*fakeICMPEndpoint
}

var fakeICMPHub = newFakeICMPHub()

func newFakeICMPHub() *fakeICMPHubT { return &fakeICMPHubT{} }

type fakeICMPEndpoint struct {
	hub *fakeICMPHubT
	udp *net.UDPConn
}

func (h *fakeICMPHubT) listen(addr string) (icmpPacketConn, error) {
	_ = addr
	pc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	ep := &fakeICMPEndpoint{hub: h, udp: pc}
	h.mu.Lock()
	h.eps = append(h.eps, ep)
	h.mu.Unlock()
	return ep, nil
}

func (h *fakeICMPHubT) unregister(ep *fakeICMPEndpoint) {
	h.mu.Lock()
	for i, e := range h.eps {
		if e == ep {
			h.eps = append(h.eps[:i], h.eps[i+1:]...)
			break
		}
	}
	h.mu.Unlock()
}

func (h *fakeICMPHubT) deliver(sender *fakeICMPEndpoint, b []byte) {
	h.mu.Lock()
	targets := make([]*net.UDPConn, 0, len(h.eps))
	for _, e := range h.eps {
		if e != sender {
			targets = append(targets, e.udp)
		}
	}
	h.mu.Unlock()
	for _, t := range targets {
		_, _ = sender.udp.WriteTo(b, t.LocalAddr())
	}

	// Simulate the kernel's automatic echo reply: for every outgoing echo
	// request, all raw ICMP sockets on the host (the sender included) see the
	// type=0 reply with the original payload. Real-ICMP behavior the fake
	// must reproduce — servers see their clients' packets twice.
	if len(b) > 0 && b[0] == 8 {
		echo := append([]byte(nil), b...)
		echo[0] = 0
		h.mu.Lock()
		all := make([]*net.UDPConn, 0, len(h.eps))
		for _, e := range h.eps {
			all = append(all, e.udp)
		}
		h.mu.Unlock()
		for _, t := range all {
			_, _ = sender.udp.WriteTo(echo, t.LocalAddr())
		}
	}
}

func (ep *fakeICMPEndpoint) WriteTo(b []byte, dst net.Addr) (int, error) {
	ep.hub.deliver(ep, b)
	return len(b), nil
}

func (ep *fakeICMPEndpoint) ReadFrom(b []byte) (int, net.Addr, error) {
	return ep.udp.ReadFrom(b)
}

func (ep *fakeICMPEndpoint) SetReadDeadline(t time.Time) error {
	return ep.udp.SetReadDeadline(t)
}

func (ep *fakeICMPEndpoint) Close() error {
	ep.hub.unregister(ep)
	return ep.udp.Close()
}

// nextE2EKey hands every pair a unique auth key: all raw ICMP sockets on the
// host see all loopback traffic, and the key is what keeps concurrent pairs
// from cross-processing each other's packets.
var e2eKeyCounter atomic.Int64

func nextE2EKey() int {
	return int(1000000 + e2eKeyCounter.Add(1))
}

// --- pair bootstrap ----------------------------------------------------------

type pairOpts struct {
	direct        bool // false => open_sock5 SOCKS5 listener (implies tcpmode)
	udp           bool // tcpmode=0: UDP datagram listener (direct mode only)
	tcpBS         int  // tcpmode_buffersize
	tcpMW         int  // tcpmode_maxwin
	tcpRST        int  // tcpmode_resend_timems
	tcpGZ         int  // tcpmode_compress (zstd frames above this many bytes)
	crypto        EncryptionMode
	cryptoKey     string
	s5user        string
	s5pass        string
	timeout       int // per-conn inactivity timeout in seconds, both sides
	serverMaxConn int
	clientMaxConn int
	congestion    string
}

func basePairOpts() pairOpts {
	return pairOpts{
		direct:     true,
		tcpBS:      1024 * 1024,
		tcpMW:      20000,
		tcpRST:     400,
		timeout:    60,
		congestion: "bb",
	}
}

type e2ePair struct {
	t          *testing.T
	opts       pairOpts
	key        int
	server     *Server
	client     *Client
	clientAddr string // local listener (TCP for tcpmode/socks5, UDP for udp mode)
	echoAddr   string
	stopEcho   func()

	closeOnce sync.Once
}

func startPair(t *testing.T, opts pairOpts) *e2ePair {
	t.Helper()
	ensureICMPUnderlay(t)

	if !opts.direct && opts.udp {
		t.Fatal("socks5 mode implies tcpmode; udp listener is direct-mode only")
	}

	p := &e2ePair{t: t, opts: opts, key: nextE2EKey()}
	t.Cleanup(p.Close)

	if opts.udp {
		p.echoAddr, p.stopEcho = startUDPEchoServer(t)
	} else {
		p.echoAddr, p.stopEcho = startTCPEchoServer(t)
	}

	var cryptoCfg *CryptoConfig
	if opts.crypto != NoEncryption {
		var err error
		cryptoCfg, err = NewCryptoConfig(opts.crypto, opts.cryptoKey)
		if err != nil {
			t.Fatalf("NewCryptoConfig: %v", err)
		}
	}

	srv, err := NewServer("127.0.0.1", p.key, opts.serverMaxConn, 16, 1000, 5000, cryptoCfg, nil, opts.congestion)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	p.server = srv
	if err := srv.Run(); err != nil {
		t.Fatalf("server Run: %v", err)
	}

	if opts.udp {
		p.clientAddr = fmt.Sprintf("127.0.0.1:%d", getFreeUDPPort(t))
		p.client, err = NewClient(p.clientAddr, "127.0.0.1", p.echoAddr, opts.timeout, p.key, "127.0.0.1",
			0, 0, 0, 0, 0, 0, 0, opts.clientMaxConn, cryptoCfg, "", "", opts.congestion)
	} else {
		p.clientAddr = fmt.Sprintf("127.0.0.1:%d", getFreePort(t))
		sock5 := 0
		if !opts.direct {
			sock5 = 1
		}
		p.client, err = NewClient(p.clientAddr, "127.0.0.1", p.echoAddr, opts.timeout, p.key, "127.0.0.1",
			1, opts.tcpBS, opts.tcpMW, opts.tcpRST, opts.tcpGZ, 0, sock5, opts.clientMaxConn, cryptoCfg,
			opts.s5user, opts.s5pass, opts.congestion)
	}
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := p.client.Run(); err != nil {
		t.Fatalf("client Run: %v", err)
	}

	if opts.udp {
		// UDP listeners accept immediately; give the ICMP ping a moment so the
		// tunnel path is warm before the test sends datagrams.
		time.Sleep(500 * time.Millisecond)
	} else {
		conn, err := waitForPort(p.clientAddr, 10*time.Second)
		if err != nil {
			t.Fatalf("wait for local listener %s: %v", p.clientAddr, err)
		}
		conn.Close()
	}
	return p
}

// StopClient stops only the client half (used by shutdown/leak tests that
// assert on client-side behavior while the server keeps running).
func (p *e2ePair) StopClient() {
	if p.client != nil {
		p.client.Stop()
		p.client = nil
	}
}

// Close tears the pair down. Client.Stop joins every conn goroutine, so a hang
// here means the tunnel leaked goroutines that ignore the exit flag.
func (p *e2ePair) Close() {
	p.closeOnce.Do(func() {
		if p.client != nil {
			p.client.Stop()
			p.client = nil
		}
		if p.server != nil {
			p.server.Stop()
			p.server = nil
		}
		if p.stopEcho != nil {
			p.stopEcho()
		}
	})
}

// --- echo servers --------------------------------------------------------------

func getFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func getFreeUDPPort(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free udp port: %v", err)
	}
	defer pc.Close()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

func startTCPEchoServer(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start echo server: %v", err)
	}
	stop := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-stop:
					return
				default:
					return
				}
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 8192)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					if _, err := c.Write(buf[:n]); err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	addr := ln.Addr().String()
	cleanup := func() {
		close(stop)
		ln.Close()
	}
	return addr, cleanup
}

// startCountedTCPEchoServer also reports how many conns are currently open,
// used to verify that closing the local proxy conn propagates to the target.
func startCountedTCPEchoServer(t *testing.T) (addr string, stop func(), liveConns func() int) {
	t.Helper()
	var live atomic.Int64
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start counted echo server: %v", err)
	}
	done := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-done:
					return
				default:
					return
				}
			}
			live.Add(1)
			go func(c net.Conn) {
				defer live.Add(-1)
				defer c.Close()
				buf := make([]byte, 8192)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					if _, err := c.Write(buf[:n]); err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	cleanup := func() {
		close(done)
		ln.Close()
	}
	return ln.Addr().String(), cleanup, func() int { return int(live.Load()) }
}

func startUDPEchoServer(t *testing.T) (string, func()) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start UDP echo server: %v", err)
	}
	stop := make(chan struct{})
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				select {
				case <-stop:
					return
				default:
					return
				}
			}
			_, _ = pc.WriteTo(buf[:n], addr)
		}
	}()
	return pc.LocalAddr().String(), func() {
		close(stop)
		pc.Close()
	}
}

func waitForPort(addr string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			return conn, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("timeout waiting for %s", addr)
}

func waitGoroutinesNear(base int, tolerance int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= base+tolerance {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return runtime.NumGoroutine() <= base+tolerance
}

// --- payload helpers -------------------------------------------------------------

func fillPattern(b []byte, seed byte) {
	for i := range b {
		b[i] = byte(i) + seed
	}
}

func echoRoundTrip(t *testing.T, conn net.Conn, payload []byte, timeout time.Duration) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if sha256.Sum256(got) != sha256.Sum256(payload) {
		t.Fatalf("echo integrity mismatch (len=%d)", len(payload))
	}
}

func echoAndHash(t *testing.T, conn net.Conn, payload []byte, timeout time.Duration) [32]byte {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	want := sha256.Sum256(payload)
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read full: %v", err)
	}
	sum := sha256.Sum256(got)
	if sum != want {
		t.Fatalf("payload hash mismatch")
	}
	return sum
}

// --- resource-exhaustion-tolerant dialing ------------------------------------------

// isResourceExhaustedErr reports a client-side dial failure caused by the
// local host running out of ephemeral ports / socket resources. Never a proxy
// integrity failure, so it must not fail the test outright.
func isResourceExhaustedErr(err error) bool {
	if err == nil {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.EADDRNOTAVAIL, syscall.EADDRINUSE,
			syscall.EAGAIN, syscall.EMFILE, syscall.ENFILE:
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "can't assign requested address") ||
		strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "too many open files")
}

func dialResourceRetry(addr string, timeout time.Duration) (conn net.Conn, exhausted bool, err error) {
	deadline := time.Now().Add(timeout)
	var last error
	for attempt := 0; ; attempt++ {
		c, derr := net.DialTimeout("tcp", addr, 2*time.Second)
		if derr == nil {
			return c, false, nil
		}
		last = derr
		if !isResourceExhaustedErr(derr) {
			return nil, false, last
		}
		if time.Now().After(deadline) {
			return nil, true, nil
		}
		wait := time.Duration(50*(attempt+1)) * time.Millisecond
		if wait > time.Second {
			wait = time.Second
		}
		time.Sleep(wait)
	}
}

// --- SOCKS5 client helpers -------------------------------------------------------

func socks5Connect(t *testing.T, proxyAddr, targetAddr string, timeout time.Duration) net.Conn {
	t.Helper()
	conn, err := socks5ConnectErr(proxyAddr, targetAddr, timeout)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return conn
}

func socks5ConnectErr(proxyAddr, targetAddr string, timeout time.Duration) (net.Conn, error) {
	conn, exhausted, err := dialResourceRetry(proxyAddr, timeout)
	if err != nil {
		if exhausted {
			return nil, fmt.Errorf("socks5 dial: local resource exhaustion dialing %s", proxyAddr)
		}
		return nil, fmt.Errorf("socks5 dial: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 greet: %w", err)
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil || resp[0] != 0x05 || resp[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 greet resp: %v %v", resp, err)
	}
	tcpAddr, err := net.ResolveTCPAddr("tcp", targetAddr)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("resolve: %w", err)
	}
	var req []byte
	if ip4 := tcpAddr.IP.To4(); ip4 != nil {
		req = []byte{0x05, 0x01, 0x00, 0x01}
		req = append(req, ip4...)
	} else {
		req = []byte{0x05, 0x01, 0x00, 0x04}
		req = append(req, tcpAddr.IP.To16()...)
	}
	pb := make([]byte, 2)
	binary.BigEndian.PutUint16(pb, uint16(tcpAddr.Port))
	req = append(req, pb...)
	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 connect: %w", err)
	}
	// RFC 1928 reply: VER REP RSV ATYP BND.ADDR BND.PORT. BND.ADDR size depends
	// on ATYP, so the reply must be parsed rather than read as a fixed block.
	crh := make([]byte, 4)
	if _, err := io.ReadFull(conn, crh); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 connect resp hdr: %w", err)
	}
	bndLen := 0
	switch crh[3] {
	case 0x01:
		bndLen = 4
	case 0x04:
		bndLen = 16
	case 0x03:
		var dlen [1]byte
		if _, err := io.ReadFull(conn, dlen[:]); err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 connect resp bnd len: %w", err)
		}
		bndLen = int(dlen[0])
	default:
		conn.Close()
		return nil, fmt.Errorf("socks5 connect resp unknown atyp 0x%02x", crh[3])
	}
	if _, err := io.ReadFull(conn, make([]byte, bndLen+2)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 connect resp bnd: %w", err)
	}
	if crh[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 connect rep=0x%02x", crh[1])
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

// socks5ConnectAuth dials a SOCKS5 proxy with RFC 1929 username/password auth
// (empty creds = no-auth) via the gohome client, then CONNECTs to targetAddr.
func socks5ConnectAuth(t *testing.T, proxyAddr, targetAddr, user, pass string, timeout time.Duration) net.Conn {
	t.Helper()
	raw, exhausted, err := dialResourceRetry(proxyAddr, timeout)
	if err != nil {
		if exhausted {
			t.Skipf("socks5 dial: local resource exhaustion dialing %s", proxyAddr)
		}
		t.Fatalf("socks5 dial: %v", err)
	}
	tcpConn := raw.(*net.TCPConn)
	ms := int(timeout / time.Millisecond)
	if err := network.Sock5Handshake(tcpConn, ms, user, pass); err != nil {
		tcpConn.Close()
		t.Fatalf("socks5 handshake: %v", err)
	}
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		tcpConn.Close()
		t.Fatalf("split target addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		tcpConn.Close()
		t.Fatalf("target port: %v", err)
	}
	if err := network.Sock5SetRequest(tcpConn, host, port, ms); err != nil {
		tcpConn.Close()
		t.Fatalf("socks5 connect request: %v", err)
	}
	return tcpConn
}

// --- stream verify target (upload/download with integrity digest) ----------------

// startStreamVerifyServer starts a TCP target supporting two streaming modes:
//   - Header 'U' + uint64(totalBytes): reads totalBytes at rateBytesPerSec
//     (0 = unthrottled), computes streaming SHA-256, writes the 32-byte digest back.
//   - Header 'D' + uint64(totalBytes): streams totalBytes of deterministic
//     pattern bytes to the client as fast as possible and closes the conn.
func startStreamVerifyServer(t *testing.T, uploadReadRateBytesPerSec int64) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("startStreamVerifyServer listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				select {
				case <-done:
					return
				default:
				}
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				var hdr [9]byte
				if _, err := io.ReadFull(conn, hdr[:]); err != nil {
					return
				}
				mode := hdr[0]
				total := int64(binary.BigEndian.Uint64(hdr[1:9]))
				switch mode {
				case 'U':
					h := sha256.New()
					buf := make([]byte, 64*1024)
					var received int64
					start := time.Now()
					for received < total {
						toRead := int64(len(buf))
						if rem := total - received; rem < toRead {
							toRead = rem
						}
						n, err := conn.Read(buf[:toRead])
						if n > 0 {
							_, _ = h.Write(buf[:n])
							received += int64(n)
							if uploadReadRateBytesPerSec > 0 {
								expectedElapsed := time.Duration(float64(received) / float64(uploadReadRateBytesPerSec) * float64(time.Second))
								if sleep := expectedElapsed - time.Since(start); sleep > 0 {
									time.Sleep(sleep)
								}
							}
						}
						if err != nil {
							return
						}
					}
					_, _ = conn.Write(h.Sum(nil))
				case 'D':
					chunk := make([]byte, 64*1024)
					var sent int64
					var seq byte
					for sent < total {
						toWrite := int64(len(chunk))
						if rem := total - sent; rem < toWrite {
							toWrite = rem
						}
						fillPattern(chunk[:toWrite], seq)
						seq++
						if _, err := conn.Write(chunk[:toWrite]); err != nil {
							return
						}
						sent += toWrite
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String(), func() {
		close(done)
		_ = ln.Close()
	}
}

func putStreamHeader(mode byte, total int64) [9]byte {
	var hdr [9]byte
	hdr[0] = mode
	binary.BigEndian.PutUint64(hdr[1:9], uint64(total))
	return hdr
}

// slowReader drains r at rate bytes/sec, hashing as it goes — used to make the
// client side the bottleneck for download-direction narrow-pipe scenarios.
func slowReaderHash(conn net.Conn, total int64, rateBps int64, gotHash io.Writer, deadline time.Time) (int64, error) {
	buf := make([]byte, 64*1024)
	var received int64
	start := time.Now()
	_ = conn.SetReadDeadline(deadline)
	for received < total {
		n, err := conn.Read(buf)
		if n > 0 {
			_, _ = gotHash.Write(buf[:n])
			received += int64(n)
			expectedElapsed := time.Duration(float64(received) / float64(rateBps) * float64(time.Second))
			if sleep := expectedElapsed - time.Since(start); sleep > 0 {
				time.Sleep(sleep)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) && received == total {
				break
			}
			return received, err
		}
	}
	return received, nil
}
