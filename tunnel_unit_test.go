package pingtunnel

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/esrrhs/gohome/common"
)

// --- client: AcceptSock5Conn request-level error branches ----------------------

// socks5Greet performs the method-negotiation part of a SOCKS5 client handshake.
func socks5Greet(t *testing.T, conn net.Conn) {
	t.Helper()
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("greeting: %v", err)
	}
	sel := make([]byte, 2)
	if _, err := io.ReadFull(conn, sel); err != nil {
		t.Fatalf("method selection: %v", err)
	}
	if sel[0] != 0x05 || sel[1] != 0x00 {
		t.Fatalf("method selection = %v", sel)
	}
}

func TestAcceptSock5ConnRequestErrors(t *testing.T) {
	c := &Client{}

	t.Run("garbage request", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		serverConn, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			c.AcceptSock5Conn(serverConn.(*net.TCPConn))
		}()

		socks5Greet(t, conn)
		if _, err := conn.Write([]byte{0x05, 0x01, 0x00, 0x09, 1, 2, 3}); err != nil {
			t.Fatal(err)
		}
		rep := make([]byte, 10)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := io.ReadFull(conn, rep); err != nil {
			t.Fatalf("expected failure reply: %v", err)
		}
		if rep[1] != socks5ReplyGeneralFailure {
			t.Fatalf("reply code = %d, want general failure", rep[1])
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := conn.Read(make([]byte, 1)); err == nil {
			t.Fatal("conn must be closed after failure reply")
		}
		<-done
		conn.Close()
	})

	t.Run("unsupported command", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		serverConn, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			c.AcceptSock5Conn(serverConn.(*net.TCPConn))
		}()

		socks5Greet(t, conn)
		req := []byte{0x05, 0x02, 0x00, 0x01, 127, 0, 0, 1, 0x00, 0x50}
		if _, err := conn.Write(req); err != nil {
			t.Fatal(err)
		}
		rep := make([]byte, 10)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := io.ReadFull(conn, rep); err != nil {
			t.Fatalf("expected command-not-supported reply: %v", err)
		}
		if rep[1] != socks5ReplyCommandNotSupported {
			t.Fatalf("reply code = %d, want command not supported", rep[1])
		}
		<-done
		conn.Close()
	})
}

// --- client: SOCKS5 UDP associate lifecycle ------------------------------------

// socks5UDPDatagramFor builds a datagram in the wire format the client relay expects.
func socks5UDPDatagramFor(t *testing.T, target string, payload []byte) []byte {
	t.Helper()
	pkt, err := buildSocks5UDPDatagram(target, payload)
	if err != nil {
		t.Fatal(err)
	}
	return pkt
}

func TestAcceptSock5UDPConnLifecycle(t *testing.T) {
	rec := &fakeICMPConn{}
	c := &Client{conn: rec}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	serverConn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.AcceptSock5UDPConn(serverConn.(*net.TCPConn), "127.0.0.1:0")
	}()

	// Read the associate reply and learn the relay bind address.
	rep := make([]byte, 32)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := io.ReadFull(conn, rep[:4])
	if err != nil || n != 4 {
		t.Fatalf("reply header: %v", err)
	}
	if rep[1] != socks5ReplySucceeded {
		t.Fatalf("associate refused: %d", rep[1])
	}
	var relayStr string
	switch rep[3] {
	case socks5AddrIPv4:
		rest := make([]byte, 6)
		if _, err := io.ReadFull(conn, rest); err != nil {
			t.Fatalf("relay v4: %v", err)
		}
		relayStr = net.JoinHostPort(net.IP(rest[0:4]).String(), strconv.Itoa(int(binary.BigEndian.Uint16(rest[4:6]))))
	default:
		t.Fatalf("unexpected relay atyp %d", rep[3])
	}

	relayAddr, err := net.ResolveUDPAddr("udp", relayStr)
	if err != nil {
		t.Fatalf("relay addr %q: %v", relayStr, err)
	}
	udp, err := net.DialUDP("udp", nil, relayAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()

	// Deliver one well-formed datagram: it must open a tunnel conn entry and
	// be forwarded to the (fake) underlay.
	if _, err := udp.Write(socks5UDPDatagramFor(t, "10.0.0.1:53", []byte("query"))); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && rec.writeCount() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if rec.writeCount() == 0 {
		t.Fatal("datagram never reached the underlay")
	}
	if n := c.activeConnCount(); n != 1 {
		t.Fatalf("expected 1 tunnel conn, got %d", n)
	}

	// Garbage and wrong-source datagrams must be dropped without a trace.
	before := rec.writeCount()
	if _, err := udp.Write([]byte{0x00, 0x00, 0x00, 0x09}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if rec.writeCount() != before {
		t.Error("garbage datagram must not reach the underlay")
	}

	// Closing the control conn tears the associate down.
	conn.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("AcceptSock5UDPConn did not return after ctrl close")
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && c.activeConnCount() != 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if n := c.activeConnCount(); n != 0 {
		t.Fatalf("tunnel conns must be closed with the relay, %d left", n)
	}
}

// --- client: recvSock5UDP maxconn guard ----------------------------------------

func TestRecvSock5UDPMaxConnDrop(t *testing.T) {
	c := &Client{maxconn: 1}
	c.addClientConn("occupied", "occupied", &ClientConn{})

	relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	c.conn = &fakeICMPConn{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.recvSock5UDP(relay, nil, 0)
	}()

	src, err := net.DialUDP("udp", nil, relay.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	pkt := socks5UDPDatagramFor(t, "10.0.0.2:53", []byte("x"))
	if _, err := src.Write(pkt); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	c.exit = true
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("recvSock5UDP did not exit")
	}
	if c.activeConnCount() != 1 {
		t.Errorf("over-limit datagram must not open a conn, count=%d", c.activeConnCount())
	}
}

// --- server: proxied-UDP DATA path ----------------------------------------------

func serverWithUDPConn(t *testing.T, s *Server) *ServerConn {
	t.Helper()
	uc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { uc.Close() })
	conn := &ServerConn{
		id:             "u1",
		tcpmode:        0,
		conn:           uc,
		udpViaProxy:    true,
		udpRelayAddr:   &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5353},
		udpTargetAddr:  "10.0.0.1:53",
		timeout:        60,
		activeRecvTime: common.GetNowUpdateInSecond(),
		activeSendTime: common.GetNowUpdateInSecond(),
	}
	s.addServerConn("u1", conn)
	return conn
}

func TestServerProxiedUDPDataPath(t *testing.T) {
	s, rec := testServer(t)
	conn := serverWithUDPConn(t, s)

	// Normal datagram: wrapped for the relay and forwarded.
	pkt := testPacket("u1", MyMsg_DATA, "")
	pkt.my.Key = int32(s.key)
	pkt.my.Data = []byte("query")
	s.processPacket(pkt)
	if rec.writeCount() != 0 {
		t.Fatal("DATA must go to the target conn, not the underlay")
	}

	// Missing target entirely.
	pkt = testPacket("u1", MyMsg_DATA, "")
	pkt.my.Key = int32(s.key)
	pkt.my.Data = []byte("x")
	conn.udpTargetAddr = ""
	s.processPacket(pkt)
	if !conn.close {
		t.Fatal("missing udp target must mark the conn for close")
	}
	conn.close = false

	// Undialable target string fails datagram encoding.
	conn.udpTargetAddr = "not-an-addr"
	s.processPacket(pkt)
	if !conn.close {
		t.Fatal("bad target must mark the conn for close")
	}
	conn.close = false
	conn.udpTargetAddr = "10.0.0.1:53"

	// Missing relay addr.
	conn.udpRelayAddr = nil
	s.processPacket(pkt)
	if !conn.close {
		t.Fatal("missing relay addr must mark the conn for close")
	}
	conn.close = false
	conn.udpRelayAddr = &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5353}

	// Write error: closed target socket.
	conn.conn.Close()
	s.processPacket(pkt)
	if !conn.close {
		t.Fatal("write failure must mark the conn for close")
	}
}

func TestServerUDPDataNilPayload(t *testing.T) {
	s, rec := testServer(t)
	uc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer uc.Close()
	conn := &ServerConn{
		id:             "n1",
		tcpmode:        0,
		conn:           uc,
		udpTargetAddr:  "10.0.0.1:53",
		timeout:        60,
		activeRecvTime: common.GetNowUpdateInSecond(),
		activeSendTime: common.GetNowUpdateInSecond(),
	}
	s.addServerConn("n1", conn)

	pkt := testPacket("n1", MyMsg_DATA, "10.0.0.1:53")
	pkt.my.Key = int32(s.key)
	pkt.my.Data = nil
	s.processPacket(pkt)
	if rec.writeCount() != 0 {
		t.Error("nil payload must return before any write")
	}
}

func TestServerTCPFrameUnmarshalError(t *testing.T) {
	s, rec := testServer(t)
	conn := &ServerConn{
		id:             "t1",
		tcpmode:        1,
		timeout:        60,
		activeRecvTime: common.GetNowUpdateInSecond(),
		activeSendTime: common.GetNowUpdateInSecond(),
	}
	s.addServerConn("t1", conn)

	pkt := testPacket("t1", MyMsg_DATA, "")
	pkt.my.Key = int32(s.key)
	pkt.my.Tcpmode = 1
	pkt.my.Data = []byte{0xFF, 0xFF, 0xFF, 0xFF}
	s.processPacket(pkt)
	if rec.writeCount() != 0 {
		t.Error("unparseable frame must be dropped before any reply")
	}
}
