package pingtunnel

import (
	"github.com/esrrhs/go-engine/src/common"
	"github.com/esrrhs/go-engine/src/loggo"
	"github.com/golang/protobuf/proto"
	"golang.org/x/net/icmp"
	"net"
	"sync"
	"time"
)

func NewServer(key int, maxconn int) (*Server, error) {
	return &Server{
		exit:    false,
		key:     key,
		maxconn: maxconn,
	}, nil
}

type Server struct {
	exit           bool
	key            int
	interval       *time.Ticker
	workResultLock sync.WaitGroup
	maxconn        int

	conn *icmp.PacketConn

	localConnMap       sync.Map
	remoteConnErrorMap sync.Map

	sendPacket       uint64
	recvPacket       uint64
	sendPacketSize   uint64
	recvPacketSize   uint64
	localConnMapSize int

	echoId  int
	echoSeq int
}

type ServerConn struct {
	timeout        int
	ipaddrTarget   *net.UDPAddr
	conn           *net.UDPConn
	tcpaddrTarget  *net.TCPAddr
	tcpconn        *net.TCPConn
	id             string
	activeRecvTime time.Time
	activeSendTime time.Time
	close          bool
	rproto         int
	fm             *FrameMgr
	tcpmode        int
}

func (p *Server) Run() error {

	conn, err := icmp.ListenPacket("ip4:icmp", "")
	if err != nil {
		loggo.Error("Error listening for ICMP packets: %s", err.Error())
		return err
	}
	p.conn = conn

	recv := make(chan *Packet, 10000)
	go recvICMP(&p.workResultLock, &p.exit, *p.conn, recv)

	p.interval = time.NewTicker(time.Second)

	go func() {
		p.workResultLock.Add(1)
		defer p.workResultLock.Done()

		for !p.exit {
			select {
			case <-p.interval.C:
				p.checkTimeoutConn()
				p.showNet()
			case r := <-recv:
				p.processPacket(r)
			}
		}
	}()

	return nil
}

func (p *Server) Stop() {
	p.exit = true
	p.workResultLock.Wait()
	p.conn.Close()
	p.interval.Stop()
}

func (p *Server) processPacket(packet *Packet) {

	if packet.my.Key != (int32)(p.key) {
		return
	}

	p.echoId = packet.echoId
	p.echoSeq = packet.echoSeq

	if packet.my.Type == (int32)(MyMsg_PING) {
		t := time.Time{}
		t.UnmarshalBinary(packet.my.Data)
		loggo.Info("ping from %s %s %d %d %d", packet.src.String(), t.String(), packet.my.Rproto, packet.echoId, packet.echoSeq)
		sendICMP(packet.echoId, packet.echoSeq, *p.conn, packet.src, "", "", (uint32)(MyMsg_PING), packet.my.Data,
			(int)(packet.my.Rproto), -1, p.key,
			0, 0, 0, 0, 0, 0,
			0)
		return
	}

	loggo.Debug("processPacket %s %s %d", packet.my.Id, packet.src.String(), len(packet.my.Data))

	now := time.Now()

	id := packet.my.Id
	localConn := p.getServerConnById(id)
	if localConn == nil {

		if p.maxconn > 0 && p.localConnMapSize >= p.maxconn {
			loggo.Info("too many connections %d, server connected target fail %s", p.localConnMapSize, packet.my.Target)
			p.remoteError(id, packet)
			return
		}

		if packet.my.Tcpmode > 0 {

			addr := packet.my.Target

			c, err := net.DialTimeout("tcp", addr, time.Second)
			if err != nil {
				loggo.Error("Error listening for tcp packets: %s", err.Error())
				p.remoteError(id, packet)
				return
			}
			targetConn := c.(*net.TCPConn)
			ipaddrTarget := targetConn.RemoteAddr().(*net.TCPAddr)

			fm := NewFrameMgr((int)(packet.my.TcpmodeBuffersize), (int)(packet.my.TcpmodeMaxwin), (int)(packet.my.TcpmodeResendTimems), (int)(packet.my.TcpmodeCompress),
				(int)(packet.my.TcpmodeStat))

			localConn = &ServerConn{timeout: (int)(packet.my.Timeout), tcpconn: targetConn, tcpaddrTarget: ipaddrTarget, id: id, activeRecvTime: now, activeSendTime: now, close: false,
				rproto: (int)(packet.my.Rproto), fm: fm, tcpmode: (int)(packet.my.Tcpmode)}

			p.addServerConn(id, localConn)

			go p.RecvTCP(localConn, id, packet.src)

		} else {

			addr := packet.my.Target

			c, err := net.DialTimeout("udp", addr, time.Second)
			if err != nil {
				loggo.Error("Error listening for tcp packets: %s", err.Error())
				p.remoteError(id, packet)
				return
			}
			targetConn := c.(*net.UDPConn)
			ipaddrTarget := targetConn.RemoteAddr().(*net.UDPAddr)

			localConn = &ServerConn{timeout: (int)(packet.my.Timeout), conn: targetConn, ipaddrTarget: ipaddrTarget, id: id, activeRecvTime: now, activeSendTime: now, close: false,
				rproto: (int)(packet.my.Rproto), tcpmode: (int)(packet.my.Tcpmode)}

			p.addServerConn(id, localConn)

			go p.Recv(localConn, id, packet.src)
		}
	}

	localConn.activeRecvTime = now

	if packet.my.Type == (int32)(MyMsg_DATA) {

		if packet.my.Tcpmode > 0 {
			f := &Frame{}
			err := proto.Unmarshal(packet.my.Data, f)
			if err != nil {
				loggo.Error("Unmarshal tcp Error %s", err)
				return
			}

			localConn.fm.OnRecvFrame(f)

		} else {
			_, err := localConn.conn.Write(packet.my.Data)
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

	p.workResultLock.Add(1)
	defer p.workResultLock.Done()

	loggo.Info("server waiting target response %s -> %s %s", conn.tcpaddrTarget.String(), conn.id, conn.tcpconn.LocalAddr().String())

	loggo.Info("start wait remote connect tcp %s %s", conn.id, conn.tcpaddrTarget.String())
	startConnectTime := time.Now()
	for !p.exit {
		if conn.fm.IsConnected() {
			break
		}
		conn.fm.Update()
		sendlist := conn.fm.getSendList()
		for e := sendlist.Front(); e != nil; e = e.Next() {
			f := e.Value.(*Frame)
			mb, _ := proto.Marshal(f)
			sendICMP(p.echoId, p.echoSeq, *p.conn, src, "", id, (uint32)(MyMsg_DATA), mb,
				conn.rproto, -1, p.key,
				0, 0, 0, 0, 0, 0,
				0)
			p.sendPacket++
			p.sendPacketSize += (uint64)(len(mb))
		}
		time.Sleep(time.Millisecond * 10)
		now := time.Now()
		diffclose := now.Sub(startConnectTime)
		if diffclose > time.Second*(time.Duration(conn.timeout)) {
			loggo.Info("can not connect remote tcp %s %s", conn.id, conn.tcpaddrTarget.String())
			p.close(conn)
			return
		}
	}

	loggo.Info("remote connected tcp %s %s", conn.id, conn.tcpaddrTarget.String())

	bytes := make([]byte, 10240)

	tcpActiveRecvTime := time.Now()
	tcpActiveSendTime := time.Now()

	for !p.exit {
		now := time.Now()
		sleep := true

		left := common.MinOfInt(conn.fm.GetSendBufferLeft(), len(bytes))
		if left > 0 {
			conn.tcpconn.SetReadDeadline(time.Now().Add(time.Millisecond * 1))
			n, err := conn.tcpconn.Read(bytes[0:left])
			if err != nil {
				nerr, ok := err.(net.Error)
				if !ok || !nerr.Timeout() {
					loggo.Info("Error read tcp %s %s %s", conn.id, conn.tcpaddrTarget.String(), err)
					conn.fm.Close()
					break
				}
			}
			if n > 0 {
				sleep = false
				conn.fm.WriteSendBuffer(bytes[:n])
				tcpActiveRecvTime = now
			}
		}

		conn.fm.Update()

		sendlist := conn.fm.getSendList()
		if sendlist.Len() > 0 {
			sleep = false
			conn.activeSendTime = now
			for e := sendlist.Front(); e != nil; e = e.Next() {
				f := e.Value.(*Frame)
				mb, err := proto.Marshal(f)
				if err != nil {
					loggo.Error("Error tcp Marshal %s %s %s", conn.id, conn.tcpaddrTarget.String(), err)
					continue
				}
				sendICMP(p.echoId, p.echoSeq, *p.conn, src, "", id, (uint32)(MyMsg_DATA), mb,
					conn.rproto, -1, p.key,
					0, 0, 0, 0, 0, 0,
					0)
				p.sendPacket++
				p.sendPacketSize += (uint64)(len(mb))
			}
		}

		if conn.fm.GetRecvBufferSize() > 0 {
			sleep = false
			rr := conn.fm.GetRecvReadLineBuffer()
			conn.tcpconn.SetWriteDeadline(time.Now().Add(time.Millisecond * 1))
			n, err := conn.tcpconn.Write(rr)
			if err != nil {
				nerr, ok := err.(net.Error)
				if !ok || !nerr.Timeout() {
					loggo.Info("Error write tcp %s %s %s", conn.id, conn.tcpaddrTarget.String(), err)
					conn.fm.Close()
					break
				}
			}
			if n > 0 {
				conn.fm.SkipRecvBuffer(n)
				tcpActiveSendTime = now
			}
		}

		if sleep {
			time.Sleep(time.Millisecond * 10)
		}

		diffrecv := now.Sub(conn.activeRecvTime)
		diffsend := now.Sub(conn.activeSendTime)
		tcpdiffrecv := now.Sub(tcpActiveRecvTime)
		tcpdiffsend := now.Sub(tcpActiveSendTime)
		if diffrecv > time.Second*(time.Duration(conn.timeout)) || diffsend > time.Second*(time.Duration(conn.timeout)) ||
			tcpdiffrecv > time.Second*(time.Duration(conn.timeout)) || tcpdiffsend > time.Second*(time.Duration(conn.timeout)) {
			loggo.Info("close inactive conn %s %s", conn.id, conn.tcpaddrTarget.String())
			conn.fm.Close()
			break
		}

		if conn.fm.IsRemoteClosed() {
			loggo.Info("closed by remote conn %s %s", conn.id, conn.tcpaddrTarget.String())
			conn.fm.Close()
			break
		}
	}

	startCloseTime := time.Now()
	for !p.exit {
		now := time.Now()

		conn.fm.Update()

		sendlist := conn.fm.getSendList()
		for e := sendlist.Front(); e != nil; e = e.Next() {
			f := e.Value.(*Frame)
			mb, _ := proto.Marshal(f)
			sendICMP(p.echoId, p.echoSeq, *p.conn, src, "", id, (uint32)(MyMsg_DATA), mb,
				conn.rproto, -1, p.key,
				0, 0, 0, 0, 0, 0,
				0)
			p.sendPacket++
			p.sendPacketSize += (uint64)(len(mb))
		}

		nodatarecv := true
		if conn.fm.GetRecvBufferSize() > 0 {
			rr := conn.fm.GetRecvReadLineBuffer()
			conn.tcpconn.SetWriteDeadline(time.Now().Add(time.Millisecond * 100))
			n, _ := conn.tcpconn.Write(rr)
			if n > 0 {
				conn.fm.SkipRecvBuffer(n)
				nodatarecv = false
			}
		}

		diffclose := now.Sub(startCloseTime)
		timeout := diffclose > time.Second*(time.Duration(conn.timeout))
		remoteclosed := conn.fm.IsRemoteClosed()

		if timeout {
			loggo.Info("close conn had timeout %s %s", conn.id, conn.tcpaddrTarget.String())
			break
		}

		if remoteclosed && nodatarecv {
			loggo.Info("remote conn had closed %s %s", conn.id, conn.tcpaddrTarget.String())
			break
		}

		time.Sleep(time.Millisecond * 100)
	}

	time.Sleep(time.Second)

	loggo.Info("close tcp conn %s %s", conn.id, conn.tcpaddrTarget.String())
	p.close(conn)
}

func (p *Server) Recv(conn *ServerConn, id string, src *net.IPAddr) {

	p.workResultLock.Add(1)
	defer p.workResultLock.Done()

	loggo.Info("server waiting target response %s -> %s %s", conn.ipaddrTarget.String(), conn.id, conn.conn.LocalAddr().String())

	for !p.exit {
		bytes := make([]byte, 2000)

		conn.conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
		n, _, err := conn.conn.ReadFromUDP(bytes)
		if err != nil {
			nerr, ok := err.(net.Error)
			if !ok || !nerr.Timeout() {
				loggo.Info("ReadFromUDP Error read udp %s", err)
				conn.close = true
				return
			}
		}

		now := time.Now()
		conn.activeSendTime = now

		sendICMP(p.echoId, p.echoSeq, *p.conn, src, "", id, (uint32)(MyMsg_DATA), bytes[:n],
			conn.rproto, -1, p.key,
			0, 0, 0, 0, 0, 0,
			0)

		p.sendPacket++
		p.sendPacketSize += (uint64)(n)
	}
}

func (p *Server) close(conn *ServerConn) {
	if p.getServerConnById(conn.id) != nil {
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

	now := time.Now()
	for _, conn := range tmp {
		if conn.tcpmode > 0 {
			continue
		}
		diffrecv := now.Sub(conn.activeRecvTime)
		diffsend := now.Sub(conn.activeSendTime)
		if diffrecv > time.Second*(time.Duration(conn.timeout)) || diffsend > time.Second*(time.Duration(conn.timeout)) {
			conn.close = true
		}
	}

	for id, conn := range tmp {
		if conn.tcpmode > 0 {
			continue
		}
		if conn.close {
			loggo.Info("close inactive conn %s %s", id, conn.ipaddrTarget.String())
			p.close(conn)
		}
	}
}

func (p *Server) showNet() {
	p.localConnMapSize = 0
	p.localConnMap.Range(func(key, value interface{}) bool {
		p.localConnMapSize++
		return true
	})
	loggo.Info("send %dPacket/s %dKB/s recv %dPacket/s %dKB/s %dConnections",
		p.sendPacket, p.sendPacketSize/1024, p.recvPacket, p.recvPacketSize/1024, p.localConnMapSize)
	p.sendPacket = 0
	p.recvPacket = 0
	p.sendPacketSize = 0
	p.recvPacketSize = 0
}

func (p *Server) addServerConn(uuid string, serverConn *ServerConn) {
	p.localConnMap.Store(uuid, serverConn)
}

func (p *Server) getServerConnById(uuid string) *ServerConn {
	ret, ok := p.localConnMap.Load(uuid)
	if !ok {
		return nil
	}
	return ret.(*ServerConn)
}

func (p *Server) deleteServerConn(uuid string) {
	p.localConnMap.Delete(uuid)
}

func (p *Server) remoteError(uuid string, packet *Packet) {
	sendICMP(packet.echoId, packet.echoSeq, *p.conn, packet.src, "", uuid, (uint32)(MyMsg_KICK), packet.my.Data,
		(int)(packet.my.Rproto), -1, p.key,
		0, 0, 0, 0, 0, 0,
		0)
}
