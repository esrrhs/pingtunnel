package pingtunnel

import (
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/esrrhs/gohome/common"
)

func testServer(t *testing.T) (*Server, *fakeICMPConn) {
	t.Helper()
	rec := &fakeICMPConn{}
	s := &Server{
		key:              12345,
		conn:             rec,
		maxprocessthread: 0,
		connecttmeout:    500,
		congestion:       "bb",
		share:            newUnderlayShare(),
	}
	return s, rec
}

func testPacket(id string, msgType MyMsg_TYPE, target string) *Packet {
	return &Packet{
		my:       &MyMsg{Id: id, Type: int32(msgType), Target: target, Magic: int32(MyMsg_MAGIC)},
		src:      &net.IPAddr{IP: net.IPv4(9, 9, 9, 9)},
		echoId:   1,
		echoSeq:  2,
		icmpType: 8,
	}
}

// testPacketTCP is a tcpmode DATA packet with a valid window, matching what
// a real client sends to open a TCP-mode tunnel conn.
func testPacketTCP(id string, target string) *Packet {
	p := testPacket(id, MyMsg_DATA, target)
	p.my.Tcpmode = 1
	p.my.TcpmodeMaxwin = int32(DEFAULT_TCP_WIN)
	return p
}

func TestServerProcessPacketFilters(t *testing.T) {
	s, rec := testServer(t)

	wrongType := testPacket("c1", MyMsg_DATA, "127.0.0.1:80")
	wrongType.icmpType = 0 // kernel echo reply, not a request
	s.processPacket(wrongType)
	if rec.writeCount() != 0 {
		t.Error("reply-type packets must be ignored")
	}

	wrongKey := testPacket("c1", MyMsg_PING, "")
	wrongKey.my.Key = 999
	s.processPacket(wrongKey)
	if rec.writeCount() != 0 {
		t.Error("wrong key must be ignored")
	}
}

func TestServerProcessPacketPing(t *testing.T) {
	s, rec := testServer(t)

	// PING without a conn id: echo only.
	p := testPacket("", MyMsg_PING, "")
	p.my.Key = int32(s.key)
	tb, _ := time.Now().MarshalBinary()
	p.my.Data = tb
	s.processPacket(p)
	if rec.writeCount() != 1 {
		t.Fatalf("ping must be echoed, wrote %d", rec.writeCount())
	}

	// PING with a conn id: also marks the conn congested.
	cc := &ServerConn{id: "pc"}
	s.addServerConn("pc", cc)
	p = testPacket("pc", MyMsg_PING, "")
	p.my.Key = int32(s.key)
	p.my.Data = tb
	s.processPacket(p)
	if rec.writeCount() != 2 {
		t.Fatalf("second ping must be echoed, wrote %d", rec.writeCount())
	}
	if cc.peerCongestionTime.IsZero() {
		t.Error("congestion ping must mark the conn")
	}
}

func TestServerProcessPacketKick(t *testing.T) {
	s, _ := testServer(t)

	// Unknown conn id: silent.
	ghost := testPacket("ghost", MyMsg_KICK, "")
	ghost.my.Key = int32(s.key)
	s.processPacket(ghost)
	if _, ok := s.recentlyClosed.Load("ghost"); ok {
		t.Error("kick for unknown conn must not register recentlyClosed")
	}

	// Known conn: closed and remembered.
	cc := &ServerConn{id: "kc"}
	s.addServerConn("kc", cc)
	kick := testPacket("kc", MyMsg_KICK, "")
	kick.my.Key = int32(s.key)
	s.processPacket(kick)
	if s.getServerConnById("kc") != nil {
		t.Error("kicked conn must be removed")
	}
	if _, ok := s.recentlyClosed.Load("kc"); !ok {
		t.Error("kicked conn must be remembered as recently closed")
	}
}

func TestServerProcessDataPacketDirectDispatch(t *testing.T) {
	s, _ := testServer(t) // maxprocessthread == 0
	// A DATA packet for an unknown conn lands in processDataPacketNewConn,
	// which fails to resolve the target and reports a remote error. A
	// host-name failure (not a refused dial) keeps the blacklist assertion
	// deterministic even on hosts with a transparent proxy.
	pkt := testPacketTCP("direct-1", "no-such-tcp-host.invalid:80")
	pkt.my.Key = int32(s.key)
	s.processPacket(pkt)
	if !s.isConnError("no-such-tcp-host.invalid:80") {
		t.Error("failed target dial must blacklist the addr")
	}
}

func TestServerProcessDataPacketNewConn(t *testing.T) {
	t.Run("recently closed silence", func(t *testing.T) {
		s, _ := testServer(t)
		s.recentlyClosed.Store("c1", common.GetNowUpdateInSecond())
		if got := s.processDataPacketNewConn("c1", testPacket("c1", MyMsg_DATA, "127.0.0.1:80")); got != nil {
			t.Error("recently closed conn must stay silent")
		}
		// A stale entry is deleted and processing continues (dial then fails).
		s.recentlyClosed.Store("c1", common.GetNowUpdateInSecond().Add(-10*time.Second))
		if got := s.processDataPacketNewConn("c1", testPacketTCP("c1", "no-such-tcp-host.invalid:80")); got != nil {
			t.Error("stale recently-closed must be purged and dial-fail to nil")
		}
		if _, ok := s.recentlyClosed.Load("c1"); ok {
			t.Error("stale entry must be deleted")
		}
	})

	t.Run("maxconn", func(t *testing.T) {
		s, _ := testServer(t)
		s.maxconn = 1
		s.addServerConn("occupied", &ServerConn{id: "occupied"})
		if got := s.processDataPacketNewConn("c2", testPacket("c2", MyMsg_DATA, "127.0.0.1:80")); got != nil {
			t.Error("over-limit conn must be refused")
		}
	})

	t.Run("conn error blacklist", func(t *testing.T) {
		s, _ := testServer(t)
		s.addConnError("127.0.0.1:80")
		if got := s.processDataPacketNewConn("c3", testPacket("c3", MyMsg_DATA, "127.0.0.1:80")); got != nil {
			t.Error("blacklisted target must be refused")
		}
	})

	t.Run("invalid tcpmode window", func(t *testing.T) {
		s, _ := testServer(t)
		pkt := testPacket("c4", MyMsg_DATA, "127.0.0.1:80")
		pkt.my.Tcpmode = 1
		pkt.my.TcpmodeMaxwin = 0
		if got := s.processDataPacketNewConn("c4", pkt); got != nil {
			t.Error("zero window must be refused")
		}
		pkt.my.TcpmodeMaxwin = int32(FRAME_MAX_ID)
		if got := s.processDataPacketNewConn("c4", pkt); got != nil {
			t.Error("oversized window must be refused")
		}
	})

	t.Run("tcp dial fail blacklists", func(t *testing.T) {
		s, rec := testServer(t)
		if got := s.processDataPacketNewConn("c5", testPacketTCP("c5", "no-such-tcp-host.invalid:80")); got != nil {
			t.Error("unresolvable target must return nil")
		}
		if rec.writeCount() == 0 {
			t.Error("dial failure must send a remote error")
		}
		if !s.isConnError("no-such-tcp-host.invalid:80") {
			t.Error("target must be blacklisted")
		}
	})

	t.Run("udp forward wrong scheme", func(t *testing.T) {
		s, _ := testServer(t)
		s.forwardConfig = &ForwardConfig{Scheme: "http", Host: "127.0.0.1", Port: 1}
		if got := s.processDataPacketNewConn("c6", testPacket("c6", MyMsg_DATA, "127.0.0.1:53")); got != nil {
			t.Error("udp via http proxy must be refused")
		}
	})

	t.Run("udp forward dial fail", func(t *testing.T) {
		s, _ := testServer(t)
		s.forwardConfig = &ForwardConfig{Scheme: "socks5", Host: "127.0.0.1", Port: 1}
		if got := s.processDataPacketNewConn("c7", testPacket("c7", MyMsg_DATA, "127.0.0.1:53")); got != nil {
			t.Error("dead proxy must refuse the conn")
		}
	})

	t.Run("udp direct dial fail", func(t *testing.T) {
		s, _ := testServer(t)
		if got := s.processDataPacketNewConn("c8", testPacket("c8", MyMsg_DATA, "no-such-host.invalid:53")); got != nil {
			t.Error("unresolvable udp target must refuse the conn")
		}
	})
}

func TestServerRunICMPListenError(t *testing.T) {
	swapICMPListen(t, func(addr string) (icmpPacketConn, error) {
		return nil, errors.New("no icmp for you")
	})
	s := &Server{icmpAddr: "127.0.0.1", share: newUnderlayShare()}
	if err := s.Run(); err == nil || !strings.Contains(err.Error(), "no icmp") {
		t.Fatalf("Run err = %v", err)
	}
}

func TestServerStopWithoutConn(t *testing.T) {
	s, rec := testServer(t)
	s.recvcontrol = make(chan int, 1)
	s.Stop()
	if !s.exit {
		t.Error("Stop must set exit")
	}
	_ = rec
}

func TestIsLocalDialErr(t *testing.T) {
	if isLocalDialErr(nil) {
		t.Error("nil must be false")
	}
	if !isLocalDialErr(syscall.EADDRINUSE) {
		t.Error("EADDRINUSE must be local")
	}
	if !isLocalDialErr(errors.New("too many open files")) {
		t.Error("fd exhaustion must be local")
	}
	if isLocalDialErr(errors.New("connection refused")) {
		t.Error("refused target is not a local error")
	}
}

func TestServerUDPAddrHelpers(t *testing.T) {
	c := &ServerConn{udpTargetAddr: "10.0.0.1:53"}
	if got := c.udpTargetString(); got != "10.0.0.1:53" {
		t.Errorf("udpTargetString = %q", got)
	}
	c.udpTargetAddr = ""
	c.ipaddrTarget = &net.UDPAddr{IP: net.IPv4(10, 0, 0, 2), Port: 54}
	if got := c.udpTargetString(); got != "10.0.0.2:54" {
		t.Errorf("udpTargetString = %q", got)
	}
	c.ipaddrTarget = nil
	if got := c.udpTargetString(); got != "unknown" {
		t.Errorf("udpTargetString = %q", got)
	}

	if sameUDPAddr(nil, nil) {
		t.Error("nil addrs are not the same")
	}
	a := &net.UDPAddr{IP: net.IPv4(1, 1, 1, 1), Port: 10}
	if sameUDPAddr(a, &net.UDPAddr{IP: net.IPv4(1, 1, 1, 1), Port: 11}) {
		t.Error("different ports must differ")
	}
	if !sameUDPAddr(a, &net.UDPAddr{Port: 0}) {
		t.Error("unspecified relay addr accepts any source")
	}
	if sameUDPAddr(&net.UDPAddr{Port: 10}, &net.UDPAddr{IP: net.IPv4(1, 1, 1, 1), Port: 10}) {
		t.Error("nil source IP must differ from specified relay IP")
	}
	if !sameUDPAddr(a, &net.UDPAddr{IP: net.IPv4(1, 1, 1, 1), Port: 10}) {
		t.Error("equal addrs must match")
	}
	if sameUDPAddr(a, &net.UDPAddr{IP: net.IPv4(2, 2, 2, 2), Port: 10}) {
		t.Error("different IPs must differ")
	}
}

func TestServerConnErrorExpiry(t *testing.T) {
	s, _ := testServer(t)
	s.connErrorMap.Store("old:1", common.GetNowUpdateInSecond().Add(-10*time.Second))
	s.connErrorMap.Store("new:1", common.GetNowUpdateInSecond())
	s.recentlyClosed.Store("gone", common.GetNowUpdateInSecond().Add(-20*time.Second))
	s.recentlyClosed.Store("kept", common.GetNowUpdateInSecond())

	s.updateConnError()

	if s.isConnError("old:1") {
		t.Error("expired blacklist entry must be removed")
	}
	if !s.isConnError("new:1") {
		t.Error("fresh blacklist entry must stay")
	}
	if _, ok := s.recentlyClosed.Load("gone"); ok {
		t.Error("expired recentlyClosed entry must be removed")
	}
	if _, ok := s.recentlyClosed.Load("kept"); !ok {
		t.Error("fresh recentlyClosed entry must stay")
	}
}
