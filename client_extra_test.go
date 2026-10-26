package pingtunnel

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

// --- fake ICMP underlay used by unit tests ------------------------------------

type fakeTimeoutError struct{}

func (fakeTimeoutError) Error() string   { return "i/o timeout" }
func (fakeTimeoutError) Timeout() bool   { return true }
func (fakeTimeoutError) Temporary() bool { return true }

// fakeICMPConn replays scripted responses to ReadFrom, then reports read
// timeouts forever; writes are recorded. One-shot non-timeout errors can be
// queued ahead of the responses.
type fakeICMPConn struct {
	mu        sync.Mutex
	responses [][]byte
	readErrs  []error
	respIdx   int
	errIdx    int
	written   [][]byte
	dst       []net.Addr
	closed    bool
}

func (c *fakeICMPConn) ReadFrom(b []byte) (int, net.Addr, error) {
	c.mu.Lock()
	if c.errIdx < len(c.readErrs) {
		e := c.readErrs[c.errIdx]
		c.errIdx++
		c.mu.Unlock()
		return 0, nil, e
	}
	if c.respIdx < len(c.responses) {
		r := c.responses[c.respIdx]
		c.respIdx++
		n := copy(b, r)
		c.mu.Unlock()
		return n, &net.IPAddr{IP: net.IPv4(9, 9, 9, 9)}, nil
	}
	c.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	return 0, nil, fakeTimeoutError{}
}

func (c *fakeICMPConn) WriteTo(b []byte, dst net.Addr) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, errors.New("closed")
	}
	c.written = append(c.written, append([]byte(nil), b...))
	c.dst = append(c.dst, dst)
	return len(b), nil
}

func (c *fakeICMPConn) SetReadDeadline(time.Time) error { return nil }
func (c *fakeICMPConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *fakeICMPConn) writeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.written)
}

// swapICMPListen replaces the package-level underlay factory for the test.
func swapICMPListen(t *testing.T, fake func(addr string) (icmpPacketConn, error)) {
	t.Helper()
	old := icmpListen
	icmpListen = fake
	t.Cleanup(func() { icmpListen = old })
}

// --- constructor error paths ---------------------------------------------------

func TestNewClientErrorPaths(t *testing.T) {
	crypto, err := NewCryptoConfig(CHACHA20, "test-secret-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient("bad addr", "127.0.0.1", "127.0.0.1:80", 60, 1, "127.0.0.1", false, false, 0, crypto, "", "", "bb"); err == nil {
		t.Error("bad tcp listen must fail")
	}
	if _, err := NewClient("bad addr", "127.0.0.1", "127.0.0.1:80", 60, 1, "127.0.0.1", true, false, 0, crypto, "", "", "bb"); err == nil {
		t.Error("bad udp listen must fail")
	}
	if _, err := NewClient("127.0.0.1:0", "nosuch-host.invalid", "127.0.0.1:80", 60, 1, "127.0.0.1", false, false, 0, crypto, "", "", "bb"); err == nil {
		t.Error("unresolvable server must fail")
	}
}

// --- getters and stats ----------------------------------------------------------

func TestClientGetters(t *testing.T) {
	c := &Client{
		addr:                   "127.0.0.1:4455",
		ipaddr:                 &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4455},
		targetAddr:             "10.0.0.1:80",
		icmpAddr:               "0.0.0.0",
		ipaddrServer:           &net.IPAddr{IP: net.IPv4(1, 2, 3, 4)},
		addrServer:             "example.com",
		sendPacket:             1,
		recvPacket:             2,
		sendPacketSize:         3,
		recvPacketSize:         4,
		localIdToConnMapSize:   5,
		localAddrToConnMapSize: 6,
		rtt:                    7 * time.Millisecond,
	}
	if c.Addr() != "127.0.0.1:4455" {
		t.Errorf("Addr = %q", c.Addr())
	}
	if c.IPAddr() == nil || c.IPAddr().Port != 4455 {
		t.Errorf("IPAddr = %v", c.IPAddr())
	}
	if c.TargetAddr() != "10.0.0.1:80" {
		t.Errorf("TargetAddr = %q", c.TargetAddr())
	}
	if c.ICMPAddr() != "0.0.0.0" {
		t.Errorf("ICMPAddr = %q", c.ICMPAddr())
	}
	if c.ServerIPAddr() == nil {
		t.Errorf("ServerIPAddr = nil")
	}
	if c.ServerAddr() != "example.com" {
		t.Errorf("ServerAddr = %q", c.ServerAddr())
	}
	if c.RTT() != 7*time.Millisecond {
		t.Errorf("RTT = %v", c.RTT())
	}
	if c.SendPacket() != 1 || c.RecvPacket() != 2 || c.SendPacketSize() != 3 || c.RecvPacketSize() != 4 {
		t.Errorf("packet counters wrong")
	}
	if c.LocalIdToConnMapSize() != 5 || c.LocalAddrToConnMapSize() != 6 {
		t.Errorf("map sizes wrong")
	}
}

// --- idle/activity helpers ------------------------------------------------------

func TestClientLastActivityAndPingInterval(t *testing.T) {
	c := &Client{}
	if c.lastActivity().IsZero() {
		t.Error("zero lastActivityUnixNano must fall back to now")
	}

	c.touchActivity()
	if c.lastActivity().IsZero() {
		t.Error("touchActivity must record a timestamp")
	}

	now := time.Now()
	if got := c.nextPingInterval(now); got != time.Second {
		t.Errorf("hot client interval = %v, want 1s", got)
	}
	c.localIdToConnMap.Store("x", &ClientConn{})
	if got := c.nextPingInterval(now); got != time.Second {
		t.Errorf("active conn interval = %v, want 1s", got)
	}
	c.localIdToConnMap = *new(sync.Map)
	c.lastActivityUnixNano.Store(now.Add(-10 * time.Second).UnixNano())
	if got := c.nextPingInterval(now); got != 3*time.Second {
		t.Errorf("warm client interval = %v, want 3s", got)
	}
	c.lastActivityUnixNano.Store(now.Add(-40 * time.Second).UnixNano())
	if got := c.nextPingInterval(now); got != 10*time.Second {
		t.Errorf("cold client interval = %v, want 10s", got)
	}
	if n := c.activeConnCount(); n != 0 {
		t.Errorf("activeConnCount = %d, want 0", n)
	}
}

// --- maybeRefreshServerAddr -----------------------------------------------------

func TestMaybeRefreshServerAddr(t *testing.T) {
	c := &Client{
		addrServer:          "127.0.0.1",
		ipaddrServer:        &net.IPAddr{IP: net.IPv4(1, 2, 3, 4)},
		pongTime:            time.Now(),
		resolveRetryBackoff: 2 * time.Second,
	}

	// Fresh pong: success schedules the 30s refresh.
	c.nextResolveAt = time.Time{}
	c.maybeRefreshServerAddr(time.Now())
	if until := time.Until(c.nextResolveAt); until <= 25*time.Second {
		t.Errorf("fresh pong schedule = %v, want ~30s", until)
	}
	if got := c.ipaddrServer.IP.String(); got == "1.2.3.4" {
		t.Errorf("server ip not refreshed: %v", c.ipaddrServer)
	}

	// Stale pong: success schedules the 5s refresh.
	c.nextResolveAt = time.Time{}
	c.pongTime = time.Now().Add(-10 * time.Second)
	c.maybeRefreshServerAddr(time.Now())
	if until := time.Until(c.nextResolveAt); until <= 3*time.Second {
		t.Errorf("stale pong schedule = %v, want ~5s", until)
	}

	// Unresolvable: backoff grows 2s -> 4s, then caps at a minute.
	c.addrServer = "nonexistent-host-for-tests.invalid"
	c.nextResolveAt = time.Time{}
	c.maybeRefreshServerAddr(time.Now())
	if c.resolveRetryBackoff != 4*time.Second {
		t.Errorf("backoff after one failure = %v, want 4s", c.resolveRetryBackoff)
	}
	c.resolveRetryBackoff = time.Minute
	c.nextResolveAt = time.Time{}
	c.maybeRefreshServerAddr(time.Now())
	if c.resolveRetryBackoff != time.Minute {
		t.Errorf("backoff must cap at a minute, got %v", c.resolveRetryBackoff)
	}
	c.resolveRetryBackoff = time.Second
	c.nextResolveAt = time.Time{}
	c.maybeRefreshServerAddr(time.Now())
	if c.resolveRetryBackoff != 2*time.Second {
		t.Errorf("sub-second backoff must snap to 2s, got %v", c.resolveRetryBackoff)
	}

	// Before the scheduled time it is a no-op.
	future := time.Now().Add(time.Hour)
	c.nextResolveAt = future
	c.maybeRefreshServerAddr(time.Now())
	if !c.nextResolveAt.Equal(future) {
		t.Errorf("no-op call must not move nextResolveAt")
	}
}

// --- socks5 UDP helpers ---------------------------------------------------------

func TestAllowSock5UDPSource(t *testing.T) {
	c := &Client{}
	var src *net.UDPAddr
	a := &net.UDPAddr{IP: net.IPv4(1, 1, 1, 1), Port: 100}
	b := &net.UDPAddr{IP: net.IPv4(2, 2, 2, 2), Port: 100}

	if !c.allowSock5UDPSource(a, nil, 0, &src) {
		t.Error("first source must be accepted")
	}
	if src == nil || src.Port != 100 {
		t.Errorf("source = %v", src)
	}
	if !c.allowSock5UDPSource(a, nil, 0, &src) {
		t.Error("same source must be accepted")
	}
	if c.allowSock5UDPSource(b, nil, 0, &src) {
		t.Error("different source must be rejected")
	}
	// Hint-based filtering.
	var hintSrc *net.UDPAddr
	if c.allowSock5UDPSource(b, net.IPv4(3, 3, 3, 3), 0, &hintSrc) {
		t.Error("IP mismatch with hint must be rejected")
	}
	if c.allowSock5UDPSource(a, nil, 200, &hintSrc) {
		t.Error("port mismatch with hint must be rejected")
	}
	if !c.allowSock5UDPSource(a, net.IPv4(1, 1, 1, 1), 100, &hintSrc) {
		t.Error("matching hint must be accepted")
	}
}

func TestParseSock5UDPAssociateHint(t *testing.T) {
	ip, port := parseSock5UDPAssociateHint("127.0.0.1:5353")
	if ip == nil || port != 5353 {
		t.Errorf("ip=%v port=%d", ip, port)
	}
	ip, port = parseSock5UDPAssociateHint("example.com:53")
	if ip != nil || port != 53 {
		t.Errorf("domain hint: ip=%v port=%d", ip, port)
	}
	ip, port = parseSock5UDPAssociateHint("1.2.3.4:bad")
	if ip == nil || port != 0 {
		t.Errorf("bad port hint: ip=%v port=%d", ip, port)
	}
	ip, port = parseSock5UDPAssociateHint("no-port")
	if ip != nil || port != 0 {
		t.Errorf("no port: ip=%v port=%d", ip, port)
	}
	ip, port = parseSock5UDPAssociateHint("0.0.0.0:999")
	if ip != nil || port != 999 {
		t.Errorf("unspecified ip must not constrain: ip=%v port=%d", ip, port)
	}
}

func TestCopyUDPAddr(t *testing.T) {
	if copyUDPAddr(nil) != nil {
		t.Error("nil must stay nil")
	}
	a := &net.UDPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 9, Zone: "en0"}
	b := copyUDPAddr(a)
	b.IP[15] = 9
	if a.IP[15] != 4 {
		t.Error("copy must be deep")
	}
	if b.Zone != "en0" || b.Port != 9 {
		t.Errorf("copy lost fields: %v", b)
	}
}

func TestSock5UDPConnKey(t *testing.T) {
	c := &Client{}
	relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	key := c.sock5UDPConnKey(relay, &net.UDPAddr{IP: net.IPv4(1, 1, 1, 1), Port: 2}, "t:3")
	if key == "" || len(key) < 5 {
		t.Errorf("key = %q", key)
	}
}

// --- conn map bookkeeping -------------------------------------------------------

func TestClientConnMaps(t *testing.T) {
	c := &Client{}
	cc := &ClientConn{}
	c.addClientConn("id1", "addr1", cc)
	if c.getClientConnById("id1") != cc {
		t.Error("getClientConnById miss")
	}
	if c.getClientConnByAddr("addr1") != cc {
		t.Error("getClientConnByAddr miss")
	}
	if c.getClientConnById("nope") != nil || c.getClientConnByAddr("nope") != nil {
		t.Error("unknown keys must return nil")
	}
	if c.activeConnCount() != 1 {
		t.Errorf("activeConnCount = %d", c.activeConnCount())
	}
}

// --- accept loop error branch ---------------------------------------------------

func TestClientAcceptTcpListenerError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{tcplistenConn: ln.(*net.TCPListener)}
	done := make(chan struct{})
	go func() {
		_ = c.AcceptTcp()
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	ln.Close() // non-timeout accept error -> logged, loop continues until exit
	time.Sleep(50 * time.Millisecond)
	c.exit = true
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("AcceptTcp did not return after exit")
	}
}

// --- Run error paths (fake underlay, unbindable listeners) ----------------------

func TestClientRunListenErrors(t *testing.T) {
	swapICMPListen(t, func(addr string) (icmpPacketConn, error) {
		return &fakeICMPConn{}, nil
	})

	tcpClient := &Client{
		tcpmode:  1,
		tcpaddr:  &net.TCPAddr{IP: net.ParseIP("203.0.113.9"), Port: 80},
		icmpAddr: "127.0.0.1",
	}
	if err := tcpClient.Run(); err == nil {
		t.Error("unbindable tcp listen must fail")
	}

	udpClient := &Client{
		tcpmode:  0,
		ipaddr:   &net.UDPAddr{IP: net.ParseIP("203.0.113.9"), Port: 80},
		icmpAddr: "127.0.0.1",
	}
	if err := udpClient.Run(); err == nil {
		t.Error("unbindable udp listen must fail")
	}
}

// --- remoteError / sendICMP encrypt error path ----------------------------------

func TestClientRemoteError(t *testing.T) {
	rec := &fakeICMPConn{}
	c := &Client{
		id:           1,
		sequence:     2,
		conn:         rec,
		ipaddrServer: &net.IPAddr{IP: net.IPv4(1, 2, 3, 4)},
	}
	c.remoteError("conn-x")
	if rec.writeCount() != 1 {
		t.Fatalf("remoteError must send one packet, wrote %d", rec.writeCount())
	}

	// A broken crypto config makes sendICMP bail at encryption time.
	broken := &CryptoConfig{Mode: CHACHA20, Cipher: nil}
	c.cryptoConfig = broken
	c.remoteError("conn-y")
	if rec.writeCount() != 1 {
		t.Errorf("failed encrypt must not write, wrote %d", rec.writeCount())
	}
}

// --- recvICMP error and dispatch paths ------------------------------------------

func mustMarshalMyMsg(t *testing.T, m *MyMsg) []byte {
	t.Helper()
	mb, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return mb
}

func TestRecvICMPDispatchAndErrors(t *testing.T) {
	good := mustMarshalMyMsg(t, &MyMsg{Id: "conn-1", Type: int32(MyMsg_DATA), Magic: int32(MyMsg_MAGIC), Data: []byte("x")})
	badMagic := mustMarshalMyMsg(t, &MyMsg{Id: "conn-2", Type: int32(MyMsg_DATA), Magic: 999})

	echoHeader := func(payload []byte) []byte {
		pkt := make([]byte, 8+len(payload))
		pkt[0] = 8 // echo request
		pkt[4], pkt[5] = 0x12, 0x34
		pkt[6], pkt[7] = 0x00, 0x01
		copy(pkt[8:], payload)
		return pkt
	}

	conn := &fakeICMPConn{
		readErrs: []error{errors.New("permanent read failure")},
		responses: [][]byte{
			{}, // n <= 0
			echoHeader([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF}), // unmarshal error
			echoHeader(badMagic),                             // magic mismatch
			echoHeader(good),                                 // delivered
		},
	}

	exit := false
	var loopWG sync.WaitGroup
	recv := make(chan *Packet, 10)
	loopWG.Add(1)
	go func() {
		defer loopWG.Done()
		recvICMP(&loopWG, &exit, conn, recv, nil)
	}()

	select {
	case p := <-recv:
		if p.my.Id != "conn-1" || p.icmpType != 8 || p.echoId != 0x1234 {
			t.Fatalf("delivered packet = %+v", p)
		}
		if p.src.IP == nil {
			t.Fatalf("src = %v", p.src)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("valid packet not delivered")
	}

	exit = true
	done := make(chan struct{})
	go func() { loopWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("recvICMP did not exit")
	}
}

func TestRecvICMPDecryptErrorSkipped(t *testing.T) {
	// A config with a mode but no cipher makes every payload undecryptable.
	broken := &CryptoConfig{Mode: AES256, Cipher: nil}
	conn := &fakeICMPConn{
		responses: [][]byte{make([]byte, 32)},
	}
	exit := false
	var loopWG sync.WaitGroup
	recv := make(chan *Packet, 10)
	loopWG.Add(1)
	go func() {
		defer loopWG.Done()
		recvICMP(&loopWG, &exit, conn, recv, broken)
	}()

	select {
	case p := <-recv:
		t.Fatalf("undecryptable payload must be dropped, got %+v", p)
	case <-time.After(300 * time.Millisecond):
	}
	exit = true
	done := make(chan struct{})
	go func() { loopWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("recvICMP did not exit")
	}
}

// --- Run/Stop lifecycle over the fake underlay ----------------------------------

func TestClientRunStopLifecycle(t *testing.T) {
	rec := &fakeICMPConn{}
	swapICMPListen(t, func(addr string) (icmpPacketConn, error) { return rec, nil })

	c, err := NewClient("127.0.0.1:0", "127.0.0.1", "127.0.0.1:9", 60, 7, "127.0.0.1",
		false, false, 0, nil, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	time.Sleep(1200 * time.Millisecond) // one maintenance tick: ping + showNet + refresh
	c.Stop()
	if rec.writeCount() == 0 {
		t.Error("client should have pinged the server at least once")
	}
}
