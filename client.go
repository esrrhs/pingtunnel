package pingtunnel

import (
	"github.com/esrrhs/go-engine/src/common"
	"github.com/esrrhs/go-engine/src/loggo"
	"github.com/golang/protobuf/proto"
	"golang.org/x/net/icmp"
	"math"
	"math/rand"
	"net"
	"time"
)

const (
	SEND_PROTO int = 8
	RECV_PROTO int = 0
)

func NewClient(addr string, server string, target string, timeout int, key int,
	tcpmode int, tcpmode_buffersize int, tcpmode_maxwin int, tcpmode_resend_timems int) (*Client, error) {

	var ipaddr *net.UDPAddr
	var tcpaddr *net.TCPAddr
	var err error

	if tcpmode > 0 {
		tcpaddr, err = net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			return nil, err
		}
	} else {
		ipaddr, err = net.ResolveUDPAddr("udp", addr)
		if err != nil {
			return nil, err
		}
	}

	ipaddrServer, err := net.ResolveIPAddr("ip", server)
	if err != nil {
		return nil, err
	}

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return &Client{
		id:                    r.Intn(math.MaxInt16),
		ipaddr:                ipaddr,
		tcpaddr:               tcpaddr,
		addr:                  addr,
		ipaddrServer:          ipaddrServer,
		addrServer:            server,
		targetAddr:            target,
		timeout:               timeout,
		key:                   key,
		tcpmode:               tcpmode,
		tcpmode_buffersize:    tcpmode_buffersize,
		tcpmode_maxwin:        tcpmode_maxwin,
		tcpmode_resend_timems: tcpmode_resend_timems,
	}, nil
}

type Client struct {
	id       int
	sequence int

	timeout               int
	sproto                int
	rproto                int
	key                   int
	tcpmode               int
	tcpmode_buffersize    int
	tcpmode_maxwin        int
	tcpmode_resend_timems int

	ipaddr  *net.UDPAddr
	tcpaddr *net.TCPAddr
	addr    string

	ipaddrServer *net.IPAddr
	addrServer   string

	targetAddr string

	conn          *icmp.PacketConn
	listenConn    *net.UDPConn
	tcplistenConn *net.TCPListener

	localAddrToConnMap map[string]*ClientConn
	localIdToConnMap   map[string]*ClientConn

	sendPacket     uint64
	recvPacket     uint64
	sendPacketSize uint64
	recvPacketSize uint64
}

type ClientConn struct {
	ipaddr         *net.UDPAddr
	tcpaddr        *net.TCPAddr
	id             string
	activeRecvTime time.Time
	activeSendTime time.Time
	close          bool

	fm *FrameMgr
}

func (p *Client) Addr() string {
	return p.addr
}

func (p *Client) IPAddr() *net.UDPAddr {
	return p.ipaddr
}

func (p *Client) TargetAddr() string {
	return p.targetAddr
}

func (p *Client) ServerIPAddr() *net.IPAddr {
	return p.ipaddrServer
}

func (p *Client) ServerAddr() string {
	return p.addrServer
}

func (p *Client) Run() {

	conn, err := icmp.ListenPacket("ip4:icmp", "")
	if err != nil {
		loggo.Error("Error listening for ICMP packets: %s", err.Error())
		return
	}
	defer conn.Close()
	p.conn = conn

	if p.tcpmode > 0 {
		tcplistenConn, err := net.ListenTCP("tcp", p.tcpaddr)
		if err != nil {
			loggo.Error("Error listening for tcp packets: %s", err.Error())
			return
		}
		defer tcplistenConn.Close()
		p.tcplistenConn = tcplistenConn
	} else {
		listener, err := net.ListenUDP("udp", p.ipaddr)
		if err != nil {
			loggo.Error("Error listening for udp packets: %s", err.Error())
			return
		}
		defer listener.Close()
		p.listenConn = listener
	}

	p.localAddrToConnMap = make(map[string]*ClientConn)
	p.localIdToConnMap = make(map[string]*ClientConn)

	if p.tcpmode > 0 {
		go p.AcceptTcp()
	} else {
		go p.Accept()
	}

	recv := make(chan *Packet, 10000)
	go recvICMP(*p.conn, recv)

	interval := time.NewTicker(time.Second)
	defer interval.Stop()

	for {
		select {
		case <-interval.C:
			p.checkTimeoutConn()
			p.ping()
			p.showNet()
		case r := <-recv:
			p.processPacket(r)
		}
	}
}

func (p *Client) AcceptTcp() error {

	loggo.Info("client waiting local accept tcp")

	for {
		p.tcplistenConn.SetDeadline(time.Now().Add(time.Millisecond * 1000))

		conn, err := p.tcplistenConn.AcceptTCP()
		if err != nil {
			nerr, ok := err.(net.Error)
			if !ok || !nerr.Timeout() {
				loggo.Info("Error accept tcp %s", err)
				continue
			}
		}

		if conn != nil {
			go p.AcceptTcpConn(conn)
		}
	}

}

func (p *Client) AcceptTcpConn(conn *net.TCPConn) {

	uuid := UniqueId()
	tcpsrcaddr := conn.RemoteAddr().(*net.TCPAddr)

	fm := NewFrameMgr(p.tcpmode_buffersize, p.tcpmode_maxwin, p.tcpmode_resend_timems)

	now := time.Now()
	clientConn := &ClientConn{tcpaddr: tcpsrcaddr, id: uuid, activeRecvTime: now, activeSendTime: now, close: false,
		fm: fm}
	p.localAddrToConnMap[tcpsrcaddr.String()] = clientConn
	p.localIdToConnMap[uuid] = clientConn
	loggo.Info("client accept new local tcp %s %s", uuid, tcpsrcaddr.String())

	loggo.Info("start connect remote tcp %s %s", uuid, tcpsrcaddr.String())
	startConnectTime := time.Now()
	for {
		if clientConn.fm.IsRemoteConnected() {
			break
		}
		clientConn.fm.Update()
		sendlist := clientConn.fm.getSendList()
		for e := sendlist.Front(); e != nil; e = e.Next() {
			f := e.Value.(*Frame)
			mb, _ := proto.Marshal(f)
			p.sequence++
			sendICMP(p.id, p.sequence, *p.conn, p.ipaddrServer, p.targetAddr, clientConn.id, (uint32)(MyMsg_DATA), mb,
				SEND_PROTO, RECV_PROTO, p.key,
				p.tcpmode, p.tcpmode_buffersize, p.tcpmode_maxwin, p.tcpmode_resend_timems,
				p.timeout)
			p.sendPacket++
			p.sendPacketSize += (uint64)(len(mb))
		}
		time.Sleep(time.Millisecond * 10)
		now := time.Now()
		diffclose := now.Sub(startConnectTime)
		if diffclose > time.Second*(time.Duration(p.timeout)) {
			loggo.Info("can not connect remote tcp %s %s", uuid, tcpsrcaddr.String())
			p.Close(clientConn)
			return
		}
	}

	loggo.Info("connected remote tcp %s %s", uuid, tcpsrcaddr.String())
	bytes := make([]byte, 10240)

	tcpActiveRecvTime := time.Now()
	tcpActiveSendTime := time.Now()

	for {
		now := time.Now()
		sleep := true

		left := common.MinOfInt(clientConn.fm.GetSendBufferLeft(), len(bytes))
		if left > 0 {
			conn.SetReadDeadline(time.Now().Add(time.Millisecond * 1))
			n, err := conn.Read(bytes[0:left])
			if err != nil {
				nerr, ok := err.(net.Error)
				if !ok || !nerr.Timeout() {
					loggo.Info("Error read tcp %s %s %s", uuid, tcpsrcaddr.String(), err)
					clientConn.fm.Close()
					break
				}
			}
			if n > 0 {
				sleep = false
				clientConn.fm.WriteSendBuffer(bytes[:n])
				tcpActiveRecvTime = now
			}
		}

		clientConn.fm.Update()

		sendlist := clientConn.fm.getSendList()
		if sendlist.Len() > 0 {
			sleep = false
			clientConn.activeSendTime = now
			for e := sendlist.Front(); e != nil; e = e.Next() {
				f := e.Value.(*Frame)
				mb, err := proto.Marshal(f)
				if err != nil {
					loggo.Error("Error tcp Marshal %s %s %s", uuid, tcpsrcaddr.String(), err)
					continue
				}
				p.sequence++
				sendICMP(p.id, p.sequence, *p.conn, p.ipaddrServer, p.targetAddr, clientConn.id, (uint32)(MyMsg_DATA), mb,
					SEND_PROTO, RECV_PROTO, p.key,
					p.tcpmode, p.tcpmode_buffersize, p.tcpmode_maxwin, p.tcpmode_resend_timems,
					p.timeout)
				p.sendPacket++
				p.sendPacketSize += (uint64)(len(mb))
			}
		}

		if clientConn.fm.GetRecvBufferSize() > 0 {
			sleep = false
			rr := clientConn.fm.GetRecvReadLineBuffer()
			conn.SetWriteDeadline(time.Now().Add(time.Millisecond * 1))
			n, err := conn.Write(rr)
			if err != nil {
				nerr, ok := err.(net.Error)
				if !ok || !nerr.Timeout() {
					loggo.Info("Error write tcp %s %s %s", uuid, tcpsrcaddr.String(), err)
					clientConn.fm.Close()
					break
				}
			}
			if n > 0 {
				clientConn.fm.SkipRecvBuffer(n)
				tcpActiveSendTime = now
			}
		}

		if sleep {
			time.Sleep(time.Millisecond * 10)
		}

		diffrecv := now.Sub(clientConn.activeRecvTime)
		diffsend := now.Sub(clientConn.activeSendTime)
		tcpdiffrecv := now.Sub(tcpActiveRecvTime)
		tcpdiffsend := now.Sub(tcpActiveSendTime)
		if diffrecv > time.Second*(time.Duration(p.timeout)) || diffsend > time.Second*(time.Duration(p.timeout)) ||
			tcpdiffrecv > time.Second*(time.Duration(p.timeout)) || tcpdiffsend > time.Second*(time.Duration(p.timeout)) {
			loggo.Info("close inactive conn %s %s", clientConn.id, clientConn.tcpaddr.String())
			clientConn.fm.Close()
			break
		}

		if clientConn.fm.IsRemoteClosed() {
			loggo.Info("closed by remote conn %s %s", clientConn.id, clientConn.tcpaddr.String())
			clientConn.fm.Close()
			break
		}
	}

	startCloseTime := time.Now()
	for {
		now := time.Now()

		clientConn.fm.Update()

		sendlist := clientConn.fm.getSendList()
		for e := sendlist.Front(); e != nil; e = e.Next() {
			f := e.Value.(*Frame)
			mb, _ := proto.Marshal(f)
			p.sequence++
			sendICMP(p.id, p.sequence, *p.conn, p.ipaddrServer, p.targetAddr, clientConn.id, (uint32)(MyMsg_DATA), mb,
				SEND_PROTO, RECV_PROTO, p.key,
				p.tcpmode, p.tcpmode_buffersize, p.tcpmode_maxwin, p.tcpmode_resend_timems,
				p.timeout)
			p.sendPacket++
			p.sendPacketSize += (uint64)(len(mb))
		}

		nodatarecv := true
		if clientConn.fm.GetRecvBufferSize() > 0 {
			rr := clientConn.fm.GetRecvReadLineBuffer()
			conn.SetWriteDeadline(time.Now().Add(time.Millisecond * 100))
			n, _ := conn.Write(rr)
			if n > 0 {
				clientConn.fm.SkipRecvBuffer(n)
				nodatarecv = false
			}
		}

		diffclose := now.Sub(startCloseTime)
		timeout := diffclose > time.Second*(time.Duration(p.timeout))
		remoteclosed := clientConn.fm.IsRemoteClosed()

		if timeout {
			loggo.Info("close conn had timeout %s %s", clientConn.id, clientConn.tcpaddr.String())
			break
		}

		if remoteclosed && nodatarecv {
			loggo.Info("remote conn had closed %s %s", clientConn.id, clientConn.tcpaddr.String())
			break
		}

		time.Sleep(time.Millisecond * 100)
	}

	loggo.Info("close tcp conn %s %s", clientConn.id, clientConn.tcpaddr.String())
	conn.Close()
	p.Close(clientConn)
}

func (p *Client) Accept() error {

	loggo.Info("client waiting local accept udp")

	bytes := make([]byte, 10240)

	for {
		p.listenConn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
		n, srcaddr, err := p.listenConn.ReadFromUDP(bytes)
		if err != nil {
			nerr, ok := err.(net.Error)
			if !ok || !nerr.Timeout() {
				loggo.Info("Error read udp %s", err)
				continue
			}
		}
		if n <= 0 {
			continue
		}

		now := time.Now()
		clientConn := p.localAddrToConnMap[srcaddr.String()]
		if clientConn == nil {
			uuid := UniqueId()
			clientConn = &ClientConn{ipaddr: srcaddr, id: uuid, activeRecvTime: now, activeSendTime: now, close: false}
			p.localAddrToConnMap[srcaddr.String()] = clientConn
			p.localIdToConnMap[uuid] = clientConn
			loggo.Info("client accept new local udp %s %s", uuid, srcaddr.String())
		}

		clientConn.activeSendTime = now
		sendICMP(p.id, p.sequence, *p.conn, p.ipaddrServer, p.targetAddr, clientConn.id, (uint32)(MyMsg_DATA), bytes[:n],
			SEND_PROTO, RECV_PROTO, p.key,
			p.tcpmode, p.tcpmode_buffersize, p.tcpmode_maxwin, p.tcpmode_resend_timems,
			p.timeout)

		p.sequence++

		p.sendPacket++
		p.sendPacketSize += (uint64)(n)
	}
}

func (p *Client) processPacket(packet *Packet) {

	if packet.my.Rproto >= 0 {
		return
	}

	if packet.my.Key != (int32)(p.key) {
		return
	}

	if packet.echoId != p.id {
		return
	}

	if packet.my.Type == (int32)(MyMsg_PING) {
		t := time.Time{}
		t.UnmarshalBinary(packet.my.Data)
		d := time.Now().Sub(t)
		loggo.Info("pong from %s %s", packet.src.String(), d.String())
		return
	}

	loggo.Debug("processPacket %s %s %d", packet.my.Id, packet.src.String(), len(packet.my.Data))

	clientConn := p.localIdToConnMap[packet.my.Id]
	if clientConn == nil {
		loggo.Debug("processPacket no conn %s ", packet.my.Id)
		return
	}

	addr := clientConn.ipaddr

	now := time.Now()
	clientConn.activeRecvTime = now

	if p.tcpmode > 0 {
		f := &Frame{}
		err := proto.Unmarshal(packet.my.Data, f)
		if err != nil {
			loggo.Error("Unmarshal tcp Error %s", err)
			return
		}

		clientConn.fm.OnRecvFrame(f)
	} else {
		_, err := p.listenConn.WriteToUDP(packet.my.Data, addr)
		if err != nil {
			loggo.Info("WriteToUDP Error read udp %s", err)
			clientConn.close = true
			return
		}
	}

	p.recvPacket++
	p.recvPacketSize += (uint64)(len(packet.my.Data))
}

func (p *Client) Close(clientConn *ClientConn) {
	if p.localIdToConnMap[clientConn.id] != nil {
		delete(p.localIdToConnMap, clientConn.id)
		delete(p.localAddrToConnMap, clientConn.ipaddr.String())
	}
}

func (p *Client) checkTimeoutConn() {

	if p.tcpmode > 0 {
		return
	}

	now := time.Now()
	for _, conn := range p.localIdToConnMap {
		diffrecv := now.Sub(conn.activeRecvTime)
		diffsend := now.Sub(conn.activeSendTime)
		if diffrecv > time.Second*(time.Duration(p.timeout)) || diffsend > time.Second*(time.Duration(p.timeout)) {
			conn.close = true
		}
	}

	for id, conn := range p.localIdToConnMap {
		if conn.close {
			loggo.Info("close inactive conn %s %s", id, conn.ipaddr.String())
			p.Close(conn)
		}
	}
}

func (p *Client) ping() {
	if p.sendPacket == 0 {
		now := time.Now()
		b, _ := now.MarshalBinary()
		sendICMP(p.id, p.sequence, *p.conn, p.ipaddrServer, p.targetAddr, "", (uint32)(MyMsg_PING), b,
			SEND_PROTO, RECV_PROTO, p.key,
			p.tcpmode, p.tcpmode_buffersize, p.tcpmode_maxwin, p.tcpmode_resend_timems,
			p.timeout)
		loggo.Info("ping %s %s %d %d %d %d", p.addrServer, now.String(), p.sproto, p.rproto, p.id, p.sequence)
		p.sequence++
	}
}

func (p *Client) showNet() {
	loggo.Info("send %dPacket/s %dKB/s recv %dPacket/s %dKB/s",
		p.sendPacket, p.sendPacketSize/1024, p.recvPacket, p.recvPacketSize/1024)
	p.sendPacket = 0
	p.recvPacket = 0
	p.sendPacketSize = 0
	p.recvPacketSize = 0
}
