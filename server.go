package pingtunnel

import (
	"github.com/esrrhs/go-engine/src/loggo"
	"golang.org/x/net/icmp"
	"net"
	"time"
)

func NewServer(timeout int, key int) (*Server, error) {
	return &Server{
		timeout: timeout,
		key:     key,
	}, nil
}

type Server struct {
	timeout int
	key     int

	conn *icmp.PacketConn

	localConnMap map[string]*ServerConn

	sendPacket     uint64
	recvPacket     uint64
	sendPacketSize uint64
	recvPacketSize uint64

	sendCatchPacket uint64
	recvCatchPacket uint64

	echoId  int
	echoSeq int
}

type ServerConn struct {
	ipaddrTarget  *net.UDPAddr
	conn          *net.UDPConn
	tcpaddrTarget *net.TCPAddr
	tcpconn       *net.TCPConn
	id            string
	activeTime    time.Time
	close         bool
	rproto        int
	catch         int
	catchQueue    chan *CatchMsg
}

func (p *Server) Run() {

	conn, err := icmp.ListenPacket("ip4:icmp", "")
	if err != nil {
		loggo.Error("Error listening for ICMP packets: %s", err.Error())
		return
	}
	p.conn = conn

	p.localConnMap = make(map[string]*ServerConn)

	recv := make(chan *Packet, 10000)
	go recvICMP(*p.conn, recv)

	interval := time.NewTicker(time.Second)
	defer interval.Stop()

	for {
		select {
		case <-interval.C:
			p.checkTimeoutConn()
			p.showNet()
		case r := <-recv:
			p.processPacket(r)
		}
	}
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
			(int)(packet.my.Rproto), -1, 0, p.key,
			0, 0, 0, 0)
		return
	}

	loggo.Debug("processPacket %s %s %d", packet.my.Id, packet.src.String(), len(packet.my.Data))

	now := time.Now()

	id := packet.my.Id
	localConn := p.localConnMap[id]
	if localConn == nil {

		if packet.my.Tcpmode > 0 {

			addr := packet.my.Target
			ipaddrTarget, err := net.ResolveTCPAddr("tcp", addr)
			if err != nil {
				loggo.Error("Error ResolveUDPAddr for tcp addr: %s %s", addr, err.Error())
				return
			}

			targetConn, err := net.DialTCP("tcp", nil, ipaddrTarget)
			if err != nil {
				loggo.Error("Error listening for tcp packets: %s", err.Error())
				return
			}

			catchQueue := make(chan *CatchMsg, packet.my.Catch)

			localConn = &ServerConn{tcpconn: targetConn, tcpaddrTarget: ipaddrTarget, id: id, activeTime: now, close: false,
				rproto: (int)(packet.my.Rproto), catchQueue: catchQueue}

			p.localConnMap[id] = localConn

			go p.RecvTCP(localConn, id, packet.src)

		} else {

			addr := packet.my.Target
			ipaddrTarget, err := net.ResolveUDPAddr("udp", addr)
			if err != nil {
				loggo.Error("Error ResolveUDPAddr for udp addr: %s %s", addr, err.Error())
				return
			}

			targetConn, err := net.DialUDP("udp", nil, ipaddrTarget)
			if err != nil {
				loggo.Error("Error listening for udp packets: %s", err.Error())
				return
			}

			catchQueue := make(chan *CatchMsg, packet.my.Catch)

			localConn = &ServerConn{conn: targetConn, ipaddrTarget: ipaddrTarget, id: id, activeTime: now, close: false,
				rproto: (int)(packet.my.Rproto), catchQueue: catchQueue}

			p.localConnMap[id] = localConn

			go p.Recv(localConn, id, packet.src)
		}
	}

	localConn.activeTime = now
	localConn.catch = (int)(packet.my.Catch)

	if packet.my.Type == (int32)(MyMsg_CATCH) {
		select {
		case re := <-localConn.catchQueue:
			sendICMP(packet.echoId, packet.echoSeq, *p.conn, re.src, "", re.id, (uint32)(MyMsg_CATCH), re.data,
				re.conn.rproto, -1, 0, p.key,
				0, 0, 0, 0)
			p.sendCatchPacket++
		case <-time.After(time.Duration(1) * time.Millisecond):
		}
		p.recvCatchPacket++
		return
	}

	if packet.my.Type == (int32)(MyMsg_DATA) {

		_, err := localConn.conn.Write(packet.my.Data)
		if err != nil {
			loggo.Error("WriteToUDP Error %s", err)
			localConn.close = true
			return
		}

		p.recvPacket++
		p.recvPacketSize += (uint64)(len(packet.my.Data))
	}
}

func (p *Server) RecvTCP(conn *ServerConn, id string, src *net.IPAddr) {

	loggo.Info("server waiting target response %s -> %s %s", conn.tcpaddrTarget.String(), conn.id, conn.tcpconn.LocalAddr().String())

	for {
		bytes := make([]byte, 2000)

		conn.conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
		n, _, err := conn.conn.ReadFromUDP(bytes)
		if err != nil {
			if neterr, ok := err.(*net.OpError); ok {
				if neterr.Timeout() {
					// Read timeout
					continue
				} else {
					loggo.Error("ReadFromUDP Error read udp %s", err)
					conn.close = true
					return
				}
			}
		}

		now := time.Now()
		conn.activeTime = now

		if conn.catch > 0 {
			select {
			case conn.catchQueue <- &CatchMsg{conn: conn, id: id, src: src, data: bytes[:n]}:
			case <-time.After(time.Duration(10) * time.Millisecond):
			}
		} else {
			sendICMP(p.echoId, p.echoSeq, *p.conn, src, "", id, (uint32)(MyMsg_DATA), bytes[:n],
				conn.rproto, -1, 0, p.key,
				0, 0, 0, 0)
		}

		p.sendPacket++
		p.sendPacketSize += (uint64)(n)
	}
}

func (p *Server) Recv(conn *ServerConn, id string, src *net.IPAddr) {

	loggo.Info("server waiting target response %s -> %s %s", conn.ipaddrTarget.String(), conn.id, conn.conn.LocalAddr().String())

	for {
		bytes := make([]byte, 2000)

		conn.conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
		n, _, err := conn.conn.ReadFromUDP(bytes)
		if err != nil {
			if neterr, ok := err.(*net.OpError); ok {
				if neterr.Timeout() {
					// Read timeout
					continue
				} else {
					loggo.Error("ReadFromUDP Error read udp %s", err)
					conn.close = true
					return
				}
			}
		}

		now := time.Now()
		conn.activeTime = now

		if conn.catch > 0 {
			select {
			case conn.catchQueue <- &CatchMsg{conn: conn, id: id, src: src, data: bytes[:n]}:
			case <-time.After(time.Duration(10) * time.Millisecond):
			}
		} else {
			sendICMP(p.echoId, p.echoSeq, *p.conn, src, "", id, (uint32)(MyMsg_DATA), bytes[:n],
				conn.rproto, -1, 0, p.key,
				0, 0, 0, 0)
		}

		p.sendPacket++
		p.sendPacketSize += (uint64)(n)
	}
}

func (p *Server) Close(conn *ServerConn) {
	if p.localConnMap[conn.id] != nil {
		conn.conn.Close()
		delete(p.localConnMap, conn.id)
	}
}

func (p *Server) checkTimeoutConn() {

	now := time.Now()
	for _, conn := range p.localConnMap {
		diff := now.Sub(conn.activeTime)
		if diff > time.Second*(time.Duration(p.timeout)) {
			conn.close = true
		}
	}

	for id, conn := range p.localConnMap {
		if conn.close {
			loggo.Info("close inactive conn %s %s", id, conn.ipaddrTarget.String())
			p.Close(conn)
		}
	}
}

func (p *Server) showNet() {
	loggo.Info("send %dPacket/s %dKB/s recv %dPacket/s %dKB/s sendCatch %d/s recvCatch %d/s",
		p.sendPacket, p.sendPacketSize/1024, p.recvPacket, p.recvPacketSize/1024, p.sendCatchPacket, p.recvCatchPacket)
	p.sendPacket = 0
	p.recvPacket = 0
	p.sendPacketSize = 0
	p.recvPacketSize = 0
	p.sendCatchPacket = 0
	p.recvCatchPacket = 0
}
