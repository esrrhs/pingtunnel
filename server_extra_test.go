package pingtunnel

import (
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/esrrhs/gohome/common"
	"github.com/esrrhs/gohome/network"
	"google.golang.org/protobuf/proto"
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
	if cc.peerCongestionTime.Load() == 0 {
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
	if !s.exit.Load() {
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

func TestServerConnPeerGone(t *testing.T) {
	now := time.Now()
	cc := &ServerConn{timeout: 5}
	if cc.peerGone(now) {
		t.Fatal("zero lastPeerPacket must not be reported gone")
	}

	cc.lastPeerPacket.Store(now.Add(-10 * time.Second).UnixNano())
	cc.timeout = 0
	if cc.peerGone(now) {
		t.Fatal("non-positive timeout must not report peer gone")
	}

	cc.timeout = 5
	cc.lastPeerPacket.Store(now.Add(-4 * time.Second).UnixNano())
	if cc.peerGone(now) {
		t.Fatal("peer within timeout must not be reported gone")
	}

	cc.lastPeerPacket.Store(now.Add(-6 * time.Second).UnixNano())
	if !cc.peerGone(now) {
		t.Fatal("peer silent past timeout must be reported gone")
	}
}

func TestServerProcessPacketUpdatesLastPeerPacket(t *testing.T) {
	s, _ := testServer(t)
	fm := network.NewFrameMgr(FRAME_MAX_SIZE, FRAME_MAX_ID, 1024, 32, 100, 0, 0)
	cc := &ServerConn{id: "lp", tcpmode: 1, fm: fm, activity: make(chan struct{}, 1)}
	s.addServerConn("lp", cc)

	// Invalid protobuf must not update lastPeerPacket.
	badProto := testPacketTCP("lp", "127.0.0.1:80")
	badProto.my.Key = int32(s.key)
	badProto.my.Data = []byte{0xff, 0xff, 0xff}
	s.processPacket(badProto)
	if cc.lastPeerPacket.Load() != 0 {
		t.Fatal("invalid protobuf must not update lastPeerPacket")
	}

	// Malformed DATA frame with nil Data must not update lastPeerPacket.
	nilDataBytes, err := proto.Marshal(&network.Frame{Type: int32(network.Frame_DATA), Id: 1})
	if err != nil {
		t.Fatal(err)
	}
	nilDataPkt := testPacketTCP("lp", "127.0.0.1:80")
	nilDataPkt.my.Key = int32(s.key)
	nilDataPkt.my.Data = nilDataBytes
	s.processPacket(nilDataPkt)
	if cc.lastPeerPacket.Load() != 0 {
		t.Fatal("malformed DATA frame must not update lastPeerPacket")
	}

	// Valid HB frame must update lastPeerPacket without touching activeRecvTime.
	hbBytes, err := proto.Marshal(&network.Frame{
		Type: int32(network.Frame_DATA),
		Id:   1,
		Data: &network.FrameData{Type: int32(network.FrameData_HB)},
	})
	if err != nil {
		t.Fatal(err)
	}
	hbPkt := testPacketTCP("lp", "127.0.0.1:80")
	hbPkt.my.Key = int32(s.key)
	hbPkt.my.Data = hbBytes
	s.processPacket(hbPkt)
	first := cc.lastPeerPacket.Load()
	if first == 0 {
		t.Fatal("valid HB frame must update lastPeerPacket")
	}
	if cc.activeRecvTime.Load() != 0 {
		t.Fatal("HB frame must not update activeRecvTime")
	}

	// Congestion PING with conn id must advance lastPeerPacket.
	cc.lastPeerPacket.Store(1)
	pingPkt := testPacket("lp", MyMsg_PING, "")
	pingPkt.my.Key = int32(s.key)
	pingPkt.my.Data, _ = time.Now().MarshalBinary()
	s.processPacket(pingPkt)
	if cc.lastPeerPacket.Load() <= 1 {
		t.Fatal("conn-scoped PING must update lastPeerPacket")
	}
}

func TestServerRecvTCPClosesSilentPeerWithUnackedFrames(t *testing.T) {
	s, _ := testServer(t)
	s.connErrorMap.Store("dummy", time.Now()) // touch map just to ensure init
	fm := network.NewFrameMgr(FRAME_MAX_SIZE, FRAME_MAX_ID, 1024, 32, 100, 0, 0)
	fm.Connect()
	clientEnd, serverEnd := net.Pipe()
	defer clientEnd.Close()

	cc := &ServerConn{
		timeout:       1,
		tcpconn:       serverEnd,
		tcpaddrTarget: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 80},
		id:            "silent-unacked",
		rproto:        0,
		fm:            fm,
		tcpmode:       1,
		activity:      make(chan struct{}, 1),
	}
	now := time.Now()
	cc.activeRecvTime.Store(now.UnixNano())
	cc.activeSendTime.Store(now.UnixNano())
	cc.lastPeerPacket.Store(now.Add(-2 * time.Second).UnixNano())
	s.addServerConn(cc.id, cc)

	// Queue data so SendWin becomes non-empty (unacknowledged frames in flight).
	fm.WriteSendBuffer([]byte("unacked-payload"))
	fm.Update()

	s.workResultLock.Add(1)
	done := make(chan struct{})
	go func() {
		s.RecvTCP(cc, cc.id, &net.IPAddr{IP: net.IPv4(9, 9, 9, 9)})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.exit.Store(true)
		t.Fatal("RecvTCP did not reap silent peer with unacknowledged frames")
	}

	if s.getServerConnById(cc.id) != nil {
		t.Fatal("reaped conn must be removed from serverConnMap")
	}
}
