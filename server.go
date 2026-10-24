package pingtunnel

import (
	"errors"
	"github.com/esrrhs/gohome/common"
	"github.com/esrrhs/gohome/loggo"
	"github.com/esrrhs/gohome/network"
	"github.com/esrrhs/gohome/thread"
	"google.golang.org/protobuf/proto"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

func NewServer(icmpAddr string, key int, maxconn int, maxprocessthread int, maxprocessbuffer int, connecttmeout int, cryptoConfig *CryptoConfig, forwardConfig *ForwardConfig, congestion string) (*Server, error) {
	s := &Server{
		icmpAddr:         icmpAddr,
		exit:             false,
		key:              key,
		maxconn:          maxconn,
		maxprocessthread: maxprocessthread,
		maxprocessbuffer: maxprocessbuffer,
		connecttmeout:    connecttmeout,
		cryptoConfig:     cryptoConfig,
		forwardConfig:    forwardConfig,
		congestion:       congestion,
	}

	if maxprocessthread > 0 {
		s.processtp = thread.NewThreadPool(maxprocessthread, maxprocessbuffer, func(v interface{}) {
			packet := v.(*Packet)
			s.processDataPacket(packet)
		})
	}

	return s, nil
}

type Server struct {
	exit             bool
	key              int
	workResultLock   sync.WaitGroup
	maxconn          int
	maxprocessthread int
	maxprocessbuffer int
	connecttmeout    int
	cryptoConfig     *CryptoConfig
	forwardConfig    *ForwardConfig
	congestion       string

	icmpAddr string

	conn icmpPacketConn

	localConnMap sync.Map
	connErrorMap sync.Map

	sendPacket       uint64
	recvPacket       uint64
	sendPacketSize   uint64
	recvPacketSize   uint64
	localConnMapSize int

	// connCount tracks localConnMap exactly (insert/delete); the maxconn
	// gate reads it live, unlike the once-per-second showNet snapshot.
	connCount atomic.Int64

	// recentlyClosed remembers conn ids deleted in the last few seconds:
	// late retransmits for them must be dropped silently instead of
	// answering with a KICK, which would tear down the client's tail
	// delivery.
	recentlyClosed sync.Map

	processtp   *thread.ThreadPool
	recvcontrol chan int
}

type ServerConn struct {
	exit           bool
	timeout        int
	ipaddrTarget   *net.UDPAddr
	conn           *net.UDPConn
	udpTargetAddr  string
	udpRelayAddr   *net.UDPAddr
	udpViaProxy    bool
	tcpaddrTarget  *net.TCPAddr
	tcpconn        net.Conn // Changed from *net.TCPConn to support proxy connections
	id             string
	activeRecvTime time.Time
	activeSendTime time.Time
	close          bool
	rproto         int
	fm             *network.FrameMgr
	tcpmode        int
	echoId         int
	echoSeq        int
	activity       chan struct{}
}

func (p *Server) Run() error {

	conn, err := icmpListen(p.icmpAddr)
	if err != nil {
		loggo.Error("Error listening for ICMP packets: %s", err.Error())
		return err
	}
	p.conn = conn

	recv := make(chan *Packet, 10000)
	p.recvcontrol = make(chan int, 1)
	go recvICMP(&p.workResultLock, &p.exit, p.conn, recv, p.cryptoConfig)

	go func() {
		defer common.CrashLog()

		p.workResultLock.Add(1)
		defer p.workResultLock.Done()

		for !p.exit {
			p.checkTimeoutConn()
			p.showNet()
			p.updateConnError()
			time.Sleep(time.Second)
		}
	}()

	go func() {
		defer common.CrashLog()

		p.workResultLock.Add(1)
		defer p.workResultLock.Done()

		for !p.exit {
			select {
			case <-p.recvcontrol:
				return
			case r := <-recv:
				p.processPacket(r)
			}
		}
	}()

	return nil
}

func (p *Server) Stop() {
	p.exit = true
	p.recvcontrol <- 1
	p.workResultLock.Wait()
	p.processtp.Stop()
	p.conn.Close()
}

func (p *Server) processPacket(packet *Packet) {

	// The kernel echoes every client echo request back as a reply to the
	// client; a raw socket on this host sees that reply too. Only genuine
	// requests (ICMP echo request) are processed — handling the kernel's
	// reply would double-process payloads and hit nil conns.
	if packet.icmpType != SEND_PROTO {
		return
	}

	if packet.my.Key != (int32)(p.key) {
		return
	}

	if packet.my.Type == (int32)(MyMsg_PING) {
		t := time.Time{}
		t.UnmarshalBinary(packet.my.Data)
		loggo.Info("ping from %s %s %d %d %d", packet.src.String(), t.String(), packet.my.Rproto, packet.echoId, packet.echoSeq)
		sendICMP(packet.echoId, packet.echoSeq, p.conn, packet.src, "", "", (uint32)(MyMsg_PING), packet.my.Data,
			(int)(packet.my.Rproto), -1, p.key,
			0, 0, 0, 0, 0, 0,
			0, p.cryptoConfig)
		return
	}

	if packet.my.Type == (int32)(MyMsg_KICK) {
		localConn := p.getServerConnById(packet.my.Id)
		if localConn != nil {
			p.close(localConn)
			loggo.Info("remote kick local %s", packet.my.Id)
		}
		return
	}

	if p.maxprocessthread > 0 {
		p.processtp.AddJob((int)(common.HashString(packet.my.Id)), packet)
	} else {
		p.processDataPacket(packet)
	}
}

func (p *Server) processDataPacketNewConn(id string, packet *Packet) *ServerConn {

	now := common.GetNowUpdateInSecond()

	if t, ok := p.recentlyClosed.Load(id); ok {
		if now.Sub(t.(time.Time)) < 5*time.Second {
			// A late retransmit for a conn this server already closed: the
			// client is still draining its tail. Stay silent — a KICK here
			// would abort that delivery.
			return nil
		}
		p.recentlyClosed.Delete(id)
	}

	loggo.Info("start add new connect  %s %s", id, packet.my.Target)

	if p.maxconn > 0 && p.connCount.Load() >= int64(p.maxconn) {
		loggo.Info("too many connections %d, server connected target fail %s", p.connCount.Load(), packet.my.Target)
		p.remoteError(packet.echoId, packet.echoSeq, id, (int)(packet.my.Rproto), packet.src)
		return nil
	}

	addr := packet.my.Target
	if p.isConnError(addr) {
		loggo.Info("addr connect Error before: %s %s", id, addr)
		p.remoteError(packet.echoId, packet.echoSeq, id, (int)(packet.my.Rproto), packet.src)
		return nil
	}

	if packet.my.Tcpmode > 0 && ((int)(packet.my.TcpmodeMaxwin) <= 0 || (int)(packet.my.TcpmodeMaxwin) >= FRAME_MAX_ID) {
		loggo.Info("invalid tcpmode window %d in packet, drop conn %s %s", packet.my.TcpmodeMaxwin, id, addr)
		p.remoteError(packet.echoId, packet.echoSeq, id, (int)(packet.my.Rproto), packet.src)
		return nil
	}

	if packet.my.Tcpmode > 0 {

		var c net.Conn
		var err error
		if p.forwardConfig != nil {
			c, err = DialThroughProxy(p.forwardConfig, addr, time.Millisecond*time.Duration(p.connecttmeout))
		} else {
			c, err = dialTCPTarget(addr, p.connecttmeout)
		}
		if err != nil {
			loggo.Error("Error listening for tcp packets: %s %s", id, err.Error())
			p.remoteError(packet.echoId, packet.echoSeq, id, (int)(packet.my.Rproto), packet.src)
			// Blacklist the target only when the target itself looks down;
			// local resource pressure (ephemeral-port/TIME_WAIT exhaustion on
			// a busy host) is transient and not the target's fault, so it
			// must not take the target offline for the blacklist window.
			if !isLocalDialErr(err) {
				p.addConnError(addr)
			}
			return nil
		}
		// For proxy connections, parse target address; for direct connections, get from remote addr
		var ipaddrTarget *net.TCPAddr
		if p.forwardConfig != nil {
			// When using proxy, resolve the original target address
			ipaddrTarget, _ = net.ResolveTCPAddr("tcp", addr)
		} else {
			ipaddrTarget = c.RemoteAddr().(*net.TCPAddr)
		}

		fm := network.NewFrameMgr(FRAME_MAX_SIZE, FRAME_MAX_ID, (int)(packet.my.TcpmodeBuffersize), (int)(packet.my.TcpmodeMaxwin), (int)(packet.my.TcpmodeResendTimems), (int)(packet.my.TcpmodeCompress),
			(int)(packet.my.TcpmodeStat))
		if p.congestion == "bb" {
			fm.SetCongestion(&network.BBCongestion{})
		}

		localConn := &ServerConn{exit: false, timeout: (int)(packet.my.Timeout), tcpconn: c, tcpaddrTarget: ipaddrTarget, id: id, activeRecvTime: now, activeSendTime: now, close: false,
			rproto: (int)(packet.my.Rproto), fm: fm, tcpmode: (int)(packet.my.Tcpmode), activity: make(chan struct{}, 1)}

		p.addServerConn(id, localConn)

		go p.RecvTCP(localConn, id, packet.src)
		return localConn

	} else {
		if p.forwardConfig != nil {
			if p.forwardConfig.Scheme != "socks5" {
				loggo.Error("UDP forwarding requires SOCKS5 proxy, got %s", p.forwardConfig.Scheme)
				p.remoteError(packet.echoId, packet.echoSeq, id, (int)(packet.my.Rproto), packet.src)
				p.addConnError(addr)
				return nil
			}

			association, err := DialUDPThroughProxy(p.forwardConfig, time.Millisecond*time.Duration(p.connecttmeout))
			if err != nil {
				loggo.Error("Error creating udp forward association: %s %s", id, err.Error())
				p.remoteError(packet.echoId, packet.echoSeq, id, (int)(packet.my.Rproto), packet.src)
				p.addConnError(addr)
				return nil
			}

			localConn := &ServerConn{
				exit:           false,
				timeout:        (int)(packet.my.Timeout),
				conn:           association.UDPConn,
				udpTargetAddr:  addr,
				udpRelayAddr:   association.RelayAddr,
				udpViaProxy:    true,
				tcpconn:        association.ControlConn,
				id:             id,
				activeRecvTime: now,
				activeSendTime: now,
				close:          false,
				rproto:         (int)(packet.my.Rproto),
				tcpmode:        (int)(packet.my.Tcpmode),
			}

			p.addServerConn(id, localConn)

			go p.Recv(localConn, id, packet.src)

			return localConn
		}

		c, err := net.DialTimeout("udp", addr, time.Millisecond*time.Duration(p.connecttmeout))
		if err != nil {
			loggo.Error("Error listening for udp packets: %s %s", id, err.Error())
			p.remoteError(packet.echoId, packet.echoSeq, id, (int)(packet.my.Rproto), packet.src)
			p.addConnError(addr)
			return nil
		}
		targetConn := c.(*net.UDPConn)
		ipaddrTarget := targetConn.RemoteAddr().(*net.UDPAddr)

		localConn := &ServerConn{exit: false, timeout: (int)(packet.my.Timeout), conn: targetConn, ipaddrTarget: ipaddrTarget, id: id, activeRecvTime: now, activeSendTime: now, close: false,
			rproto: (int)(packet.my.Rproto), tcpmode: (int)(packet.my.Tcpmode), udpTargetAddr: addr}

		p.addServerConn(id, localConn)

		go p.Recv(localConn, id, packet.src)

		return localConn
	}
}

// isLocalDialErr reports dial failures caused by local host resource
// pressure (ephemeral-port/TIME_WAIT exhaustion, fd limits) rather than by
// the target. These are transient and retryable.
func isLocalDialErr(err error) bool {
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

// dialTCPTarget dials the tunnel target, retrying briefly on local resource
// errors: each tunnel conn gets exactly one server-side dial, so a transient
// local failure would otherwise tear the conn down even though the target is
// healthy.
func dialTCPTarget(addr string, timeoutms int) (net.Conn, error) {
	c, err := net.DialTimeout("tcp", addr, time.Millisecond*time.Duration(timeoutms))
	for attempt := 0; err != nil && attempt < 2 && isLocalDialErr(err); attempt++ {
		time.Sleep(100 * time.Millisecond)
		c, err = net.DialTimeout("tcp", addr, time.Millisecond*time.Duration(timeoutms))
	}
	return c, err
}

func (p *Server) processDataPacket(packet *Packet) {

	loggo.Debug("processPacket %s %s %d", packet.my.Id, packet.src.String(), len(packet.my.Data))

	now := common.GetNowUpdateInSecond()

	id := packet.my.Id
	localConn := p.getServerConnById(id)
	if localConn == nil {
		localConn = p.processDataPacketNewConn(id, packet)
		if localConn == nil {
			return
		}
	}

	localConn.echoId = packet.echoId
	localConn.echoSeq = packet.echoSeq

	if packet.my.Type == (int32)(MyMsg_DATA) {

		if packet.my.Tcpmode > 0 {
			f := &network.Frame{}
			err := proto.Unmarshal(packet.my.Data, f)
			if err != nil {
				loggo.Error("Unmarshal tcp Error %s", err)
				return
			}
			if f.Data != nil && f.Data.Type == (int32)(network.FrameData_USER_DATA) {
				// Only payload frames count as conn activity; ACK/HB frames
				// must not keep an idle conn alive.
				localConn.activeRecvTime = now
			}

			localConn.fm.OnRecvFrame(f)
			notifyActivity(localConn.activity)

		} else {
			localConn.activeRecvTime = now
			if packet.my.Data == nil {
				return
			}

			var err error
			if localConn.udpViaProxy {
				targetAddr := localConn.udpTargetAddr
				if packet.my.Target != "" {
					targetAddr = packet.my.Target
				}
				if targetAddr == "" {
					loggo.Info("missing udp target for proxied udp conn %s", id)
					localConn.close = true
					return
				}
				udpPacket, packetErr := buildSocks5UDPDatagram(targetAddr, packet.my.Data)
				if packetErr != nil {
					loggo.Info("build socks5 udp datagram error %s", packetErr)
					localConn.close = true
					return
				}
				if localConn.udpRelayAddr == nil {
					loggo.Info("missing udp relay addr for proxied udp conn %s", id)
					localConn.close = true
					return
				}
				_, err = localConn.conn.WriteToUDP(udpPacket, localConn.udpRelayAddr)
			} else {
				_, err = localConn.conn.Write(packet.my.Data)
			}
			if err != nil {
				loggo.Info("WriteToUDP Error %s", err)
				localConn.close = true
				return
			}
		}

		p.recvPacket++
		p.recvPacketSize += (uint64)(len(packet.my.Data))
	}
}

func (p *Server) RecvTCP(conn *ServerConn, id string, src *net.IPAddr) {

	defer common.CrashLog()

	p.workResultLock.Add(1)
	defer p.workResultLock.Done()

	loggo.Info("server waiting target response %s -> %s %s", conn.tcpaddrTarget.String(), conn.id, conn.tcpconn.LocalAddr().String())

	loggo.Info("start wait remote connect tcp %s %s", conn.id, conn.tcpaddrTarget.String())
	startConnectTime := common.GetNowUpdateInSecond()
	connectWait := newAdaptiveLoopWait(2*time.Millisecond, 80*time.Millisecond)
	for !p.exit && !conn.exit {
		if conn.fm.IsConnected() {
			break
		}
		conn.fm.Update()
		sendlist := conn.fm.GetSendList()
		hadWork := sendlist.Len() > 0
		for e := sendlist.Front(); e != nil; e = e.Next() {
			f := e.Value.(*network.Frame)
			mb, _ := conn.fm.MarshalFrame(f)
			sendICMP(conn.echoId, conn.echoSeq, p.conn, src, "", id, (uint32)(MyMsg_DATA), mb,
				conn.rproto, -1, p.key, 0,
				0, 0, 0, 0, 0,
				0, p.cryptoConfig)
			p.sendPacket++
			p.sendPacketSize += (uint64)(len(mb))
		}
		now := common.GetNowUpdateInSecond()
		diffclose := now.Sub(startConnectTime)
		if diffclose > time.Second*5 {
			loggo.Info("can not connect remote tcp %s %s", conn.id, conn.tcpaddrTarget.String())
			p.close(conn)
			p.remoteError(conn.echoId, conn.echoSeq, id, conn.rproto, src)
			return
		}
		if hadWork {
			connectWait.hit()
			continue
		}
		wait := connectWait.miss()
		select {
		case <-conn.activity:
			connectWait.hit()
		case <-time.After(wait):
		}
	}

	if !conn.exit {
		loggo.Info("remote connected tcp %s %s", conn.id, conn.tcpaddrTarget.String())
	}

	bytes := make([]byte, 10240)

	readErr := make(chan error, 1)
	stopRead := make(chan struct{})

	go func() {
		defer common.CrashLog()

		readWait := newAdaptiveLoopWait(2*time.Millisecond, 80*time.Millisecond)
		for !p.exit && !conn.exit {
			left := common.MinOfInt(conn.fm.GetSendBufferLeft(), len(bytes))
			if left <= 0 {
				wait := readWait.miss()
				select {
				case <-stopRead:
					return
				case <-conn.activity:
					readWait.hit()
					continue
				case <-time.After(wait):
					continue
				}
			}
			readWait.hit()

			conn.tcpconn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, err := conn.tcpconn.Read(bytes[0:left])
			if err != nil {
				nerr, ok := err.(net.Error)
				if ok && nerr.Timeout() {
					continue
				}
				select {
				case readErr <- err:
				default:
				}
				return
			}
			if n <= 0 {
				continue
			}

			conn.fm.WriteSendBuffer(bytes[:n])
			notifyActivity(conn.activity)
		}
	}()

	loopWait := newAdaptiveLoopWait(2*time.Millisecond, 250*time.Millisecond)

	halfClosedPropagated := false

mainLoop:
	for !p.exit && !conn.exit {
		now := common.GetNowUpdateInSecond()
		hadWork := false

		conn.fm.Update()

		if !halfClosedPropagated && conn.fm.IsRemoteSendClosed() && conn.fm.GetRecvBufferSize() == 0 {
			// The client half-closed its side and every buffered byte of it
			// has been written to the target: propagate the EOF now — a
			// CloseWrite issued earlier would put the FIN ahead of data
			// still sitting in the recv buffer.
			halfClosedPropagated = true
			if tc, ok := conn.tcpconn.(*net.TCPConn); ok {
				_ = tc.CloseWrite()
			}
			loggo.Info("client half-closed conn %s %s", conn.id, conn.tcpaddrTarget.String())
		}

		sendlist := conn.fm.GetSendList()
		if sendlist.Len() > 0 {
			hadWork = true
			for e := sendlist.Front(); e != nil; e = e.Next() {
				f := e.Value.(*network.Frame)
				if f.Data != nil && f.Data.Type == (int32)(network.FrameData_USER_DATA) {
					// Only real payload counts as conn activity: heartbeat
					// and ACK frames keep the tunnel alive without keeping
					// an idle conn alive.
					conn.activeSendTime = now
				}
				mb, err := conn.fm.MarshalFrame(f)
				if err != nil {
					loggo.Error("Error tcp Marshal %s %s %s", conn.id, conn.tcpaddrTarget.String(), err)
					continue
				}
				sendICMP(conn.echoId, conn.echoSeq, p.conn, src, "", id, (uint32)(MyMsg_DATA), mb,
					conn.rproto, -1, p.key, 0,
					0, 0, 0, 0, 0,
					0, p.cryptoConfig)
				p.sendPacket++
				p.sendPacketSize += (uint64)(len(mb))
			}
		}

		if conn.fm.GetRecvBufferSize() > 0 {
			hadWork = true
			rr := conn.fm.GetRecvReadLineBuffer()
			conn.tcpconn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
			n, err := conn.tcpconn.Write(rr)
			if err != nil {
				nerr, ok := err.(net.Error)
				if !ok || !nerr.Timeout() {
					loggo.Info("Error write tcp %s %s %s", conn.id, conn.tcpaddrTarget.String(), err)
					conn.fm.Close()
					break mainLoop
				}
			}
			if n > 0 {
				conn.fm.SkipRecvBuffer(n)
			}
		}

		select {
		case err := <-readErr:
			if err != nil {
				loggo.Info("Error read tcp %s %s %s", conn.id, conn.tcpaddrTarget.String(), err)
				conn.fm.Close()
				break mainLoop
			}
		default:
		}

		// The conn is dead only when no real payload flowed in either
		// direction for the whole timeout window and nothing is waiting to be
		// delivered: a slow reader (on either side) is backpressure, not
		// idleness.
		idleRecv := now.Sub(conn.activeRecvTime) > time.Second*(time.Duration(conn.timeout))
		idleSend := now.Sub(conn.activeSendTime) > time.Second*(time.Duration(conn.timeout))
		// A conn with unacknowledged frames in flight or undelivered bytes is
		// under backpressure, not idle: the peer (or the target) is slow, and
		// closing here would cut the transfer short.
		// The peer's heartbeat proves the tunnel is stalled on backpressure
		// (its send window is full), not dead: never reap while it beats.
		hbFresh := time.Since(time.Unix(0, conn.fm.GetLastRecvHBTime())) < time.Second*(time.Duration(conn.timeout))
		if idleRecv && idleSend && !hbFresh && conn.fm.GetRecvBufferSize() == 0 && conn.fm.GetSendWinSize() == 0 {
			loggo.Info("close inactive conn %s %s", conn.id, conn.tcpaddrTarget.String())
			conn.fm.Close()
			break
		}

		if conn.fm.IsRemoteClosed() {
			loggo.Info("closed by remote conn %s %s", conn.id, conn.tcpaddrTarget.String())
			conn.fm.Close()
			break
		}

		if !hadWork {
			wait := loopWait.miss()
			select {
			case <-conn.activity:
				loopWait.hit()
			case err := <-readErr:
				if err != nil {
					loggo.Info("Error read tcp %s %s %s", conn.id, conn.tcpaddrTarget.String(), err)
					conn.fm.Close()
					break mainLoop
				}
			case <-time.After(wait):
			}
		} else {
			loopWait.hit()
		}
	}
	close(stopRead)

	conn.fm.Close()

	startCloseTime := common.GetNowUpdateInSecond()
	for !p.exit && !conn.exit {
		now := common.GetNowUpdateInSecond()

		conn.fm.Update()

		sendlist := conn.fm.GetSendList()
		for e := sendlist.Front(); e != nil; e = e.Next() {
			f := e.Value.(*network.Frame)
			mb, _ := conn.fm.MarshalFrame(f)
			sendICMP(conn.echoId, conn.echoSeq, p.conn, src, "", id, (uint32)(MyMsg_DATA), mb,
				conn.rproto, -1, p.key, 0,
				0, 0, 0, 0, 0,
				0, p.cryptoConfig)
			p.sendPacket++
			p.sendPacketSize += (uint64)(len(mb))
		}

		if conn.fm.GetRecvBufferSize() > 0 {
			rr := conn.fm.GetRecvReadLineBuffer()
			conn.tcpconn.SetWriteDeadline(time.Now().Add(time.Millisecond * 100))
			n, _ := conn.tcpconn.Write(rr)
			if n > 0 {
				conn.fm.SkipRecvBuffer(n)
			}
		}

		diffclose := now.Sub(startCloseTime)
		if diffclose > time.Second*60 {
			loggo.Info("close conn had timeout %s %s", conn.id, conn.tcpaddrTarget.String())
			break
		}

		remoteclosed := conn.fm.IsRemoteClosed()
		if remoteclosed && conn.fm.GetRecvBufferSize() == 0 && conn.fm.CloseFlushed() {
			loggo.Info("remote conn had closed %s %s", conn.id, conn.tcpaddrTarget.String())
			break
		}
		if conn.fm.IsRemoteSendClosed() && conn.fm.GetRecvBufferSize() == 0 && conn.fm.CloseFlushed() {
			loggo.Info("client half-closed and flushed %s %s", conn.id, conn.tcpaddrTarget.String())
			break
		}

		time.Sleep(time.Millisecond * 100)
	}

	time.Sleep(time.Second)

	loggo.Info("close tcp conn %s %s", conn.id, conn.tcpaddrTarget.String())
	p.close(conn)
}

func (p *Server) Recv(conn *ServerConn, id string, src *net.IPAddr) {

	defer common.CrashLog()

	p.workResultLock.Add(1)
	defer p.workResultLock.Done()

	loggo.Info("server waiting target response %s -> %s %s", conn.udpTargetString(), conn.id, conn.conn.LocalAddr().String())

	bytes := make([]byte, 65535)

	for !p.exit {

		conn.conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
		n, srcAddr, err := conn.conn.ReadFromUDP(bytes)
		if err != nil {
			nerr, ok := err.(net.Error)
			if !ok || !nerr.Timeout() {
				loggo.Info("ReadFromUDP Error read udp %s", err)
				conn.close = true
				return
			}
		}
		if n <= 0 {
			continue
		}

		now := common.GetNowUpdateInSecond()
		conn.activeSendTime = now

		targetAddr := conn.udpTargetString()
		payload := bytes[:n]

		if conn.udpViaProxy {
			if conn.udpRelayAddr != nil && !sameUDPAddr(srcAddr, conn.udpRelayAddr) {
				continue
			}
			parsedTarget, parsedPayload, parseErr := parseSocks5UDPDatagram(bytes[:n])
			if parseErr != nil {
				loggo.Debug("parse udp datagram from socks5 relay failed: %s", parseErr)
				continue
			}
			targetAddr = parsedTarget
			payload = parsedPayload
		}

		sendICMP(conn.echoId, conn.echoSeq, p.conn, src, targetAddr, id, (uint32)(MyMsg_DATA), payload,
			conn.rproto, -1, p.key, 0,
			0, 0, 0, 0, 0,
			0, p.cryptoConfig)

		p.sendPacket++
		p.sendPacketSize += (uint64)(len(payload))
	}
}

func (p *Server) close(conn *ServerConn) {
	if p.getServerConnById(conn.id) != nil {
		conn.exit = true
		if conn.conn != nil {
			conn.conn.Close()
		}
		if conn.tcpconn != nil {
			conn.tcpconn.Close()
		}
		p.deleteServerConn(conn.id)
	}
}

func (p *Server) checkTimeoutConn() {

	tmp := make(map[string]*ServerConn)
	p.localConnMap.Range(func(key, value interface{}) bool {
		id := key.(string)
		serverConn := value.(*ServerConn)
		tmp[id] = serverConn
		return true
	})

	now := common.GetNowUpdateInSecond()
	for _, conn := range tmp {
		if conn.tcpmode > 0 {
			continue
		}
		diffrecv := now.Sub(conn.activeRecvTime)
		diffsend := now.Sub(conn.activeSendTime)
		if diffrecv > time.Second*(time.Duration(conn.timeout)) && diffsend > time.Second*(time.Duration(conn.timeout)) {
			conn.close = true
		}
	}

	for id, conn := range tmp {
		if conn.tcpmode > 0 {
			continue
		}
		if conn.close {
			loggo.Info("close inactive conn %s %s", id, conn.udpTargetString())
			p.close(conn)
		}
	}
}

func (conn *ServerConn) udpTargetString() string {
	if conn.udpTargetAddr != "" {
		return conn.udpTargetAddr
	}
	if conn.ipaddrTarget != nil {
		return conn.ipaddrTarget.String()
	}
	return "unknown"
}

func sameUDPAddr(a *net.UDPAddr, b *net.UDPAddr) bool {
	if a == nil || b == nil {
		return false
	}
	if b.Port != 0 && a.Port != b.Port {
		return false
	}
	if b.IP == nil || b.IP.IsUnspecified() {
		return true
	}
	if a.IP == nil {
		return false
	}
	return a.IP.Equal(b.IP)
}

func (p *Server) showNet() {
	p.localConnMapSize = int(p.connCount.Load())
	loggo.Info("send %dPacket/s %dKB/s recv %dPacket/s %dKB/s %dConnections",
		p.sendPacket, p.sendPacketSize/1024, p.recvPacket, p.recvPacketSize/1024, p.localConnMapSize)
	p.sendPacket = 0
	p.recvPacket = 0
	p.sendPacketSize = 0
	p.recvPacketSize = 0
}

func (p *Server) addServerConn(uuid string, serverConn *ServerConn) {
	p.localConnMap.Store(uuid, serverConn)
	p.connCount.Add(1)
}

func (p *Server) getServerConnById(uuid string) *ServerConn {
	ret, ok := p.localConnMap.Load(uuid)
	if !ok {
		return nil
	}
	return ret.(*ServerConn)
}

func (p *Server) deleteServerConn(uuid string) {
	if _, ok := p.localConnMap.LoadAndDelete(uuid); ok {
		p.connCount.Add(-1)
		p.recentlyClosed.Store(uuid, time.Now())
	}
}

func (p *Server) remoteError(echoId int, echoSeq int, uuid string, rprpto int, src *net.IPAddr) {
	sendICMP(echoId, echoSeq, p.conn, src, "", uuid, (uint32)(MyMsg_KICK), []byte{},
		rprpto, -1, p.key,
		0, 0, 0, 0, 0, 0, 0,
		p.cryptoConfig)
}

func (p *Server) addConnError(addr string) {
	_, ok := p.connErrorMap.Load(addr)
	if !ok {
		now := common.GetNowUpdateInSecond()
		p.connErrorMap.Store(addr, now)
	}
}

func (p *Server) isConnError(addr string) bool {
	_, ok := p.connErrorMap.Load(addr)
	return ok
}

func (p *Server) updateConnError() {

	tmp := make(map[string]time.Time)
	p.connErrorMap.Range(func(key, value interface{}) bool {
		id := key.(string)
		t := value.(time.Time)
		tmp[id] = t
		return true
	})

	now := common.GetNowUpdateInSecond()
	for id, t := range tmp {
		diff := now.Sub(t)
		if diff > time.Second*5 {
			p.connErrorMap.Delete(id)
		}
	}

	p.recentlyClosed.Range(func(key, value interface{}) bool {
		if now.Sub(value.(time.Time)) > time.Second*10 {
			p.recentlyClosed.Delete(key)
		}
		return true
	})
}
