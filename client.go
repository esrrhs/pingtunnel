package pingtunnel

import (
	"github.com/esrrhs/go-engine/src/loggo"
	"golang.org/x/net/icmp"
	"math"
	"math/rand"
	"net"
	"time"
)

func NewClient(addr string, server string, target string, timeout int, sproto int, rproto int, catch int, key int, tcpmode bool) (*Client, error) {

	var ipaddr *net.UDPAddr
	var tcpaddr *net.TCPAddr
	var err error

	if tcpmode {
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
		id:           r.Intn(math.MaxInt16),
		ipaddr:       ipaddr,
		tcpaddr:      tcpaddr,
		addr:         addr,
		ipaddrServer: ipaddrServer,
		addrServer:   server,
		targetAddr:   target,
		timeout:      timeout,
		sproto:       sproto,
		rproto:       rproto,
		catch:        catch,
		key:          key,
		tcpmode:      tcpmode,
	}, nil
}

type Client struct {
	id       int
	sequence int

	timeout               int
	sproto                int
	rproto                int
	catch                 int
	key                   int
	tcpmode               bool
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

	sendCatchPacket uint64
	recvCatchPacket uint64
}

type ClientConn struct {
	ipaddr     *net.UDPAddr
	tcpaddr    *net.TCPAddr
	id         string
	activeTime time.Time
	close      bool

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

	if p.tcpmode {
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

	if p.tcpmode {
		go p.AcceptTcp()
	} else {
		go p.Accept()
	}

	recv := make(chan *Packet, 10000)
	go recvICMP(*p.conn, recv)

	interval := time.NewTicker(time.Second)
	defer interval.Stop()

	inter := 1000
	if p.catch > 0 {
		inter = 1000 / p.catch
		if inter <= 0 {
			inter = 1
		}
	}
	intervalCatch := time.NewTicker(time.Millisecond * time.Duration(inter))
	defer intervalCatch.Stop()

	for {
		select {
		case <-interval.C:
			p.checkTimeoutConn()
			p.ping()
			p.showNet()
		case <-intervalCatch.C:
			p.sendCatch()
		case r := <-recv:
			p.processPacket(r)
		}
	}
}

func (p *Client) AcceptTcp() error {

	loggo.Info("client waiting local accept tcp")

	for {
		p.tcplistenConn.SetDeadline(time.Now().Add(time.Millisecond * 100))

		conn, err := p.tcplistenConn.AcceptTCP()
		if err != nil {
			if neterr, ok := err.(*net.OpError); ok {
				if neterr.Timeout() {
					// Read timeout
					continue
				} else {
					loggo.Error("Error accept tcp %s", err)
					continue
				}
			}
		}

		go p.AcceptTcpConn(conn)
	}

}

func (p *Client) AcceptTcpConn(conn *net.TCPConn) {

	now := time.Now()
	uuid := UniqueId()
	tcpsrcaddr := conn.RemoteAddr().(*net.TCPAddr)

	fm := NewFrameMgr(p.tcpmode_buffersize, p.tcpmode_maxwin, p.tcpmode_resend_timems)

	clientConn := &ClientConn{tcpaddr: tcpsrcaddr, id: uuid, activeTime: now, close: false,
		fm: fm}
	p.localAddrToConnMap[tcpsrcaddr.String()] = clientConn
	p.localIdToConnMap[uuid] = clientConn
	loggo.Info("client accept new local tcp %s %s", uuid, tcpsrcaddr.String())

	bytes := make([]byte, 10240)

	for {
		left := clientConn.fm.GetSendBufferLeft()
		if left >= len(bytes) {
			conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
			n, err := conn.Read(bytes)
			if err != nil {
				if neterr, ok := err.(*net.OpError); ok {
					if neterr.Timeout() {
						// Read timeout
						n = 0
					} else {
						loggo.Error("Error read tcp %s %s %s", uuid, tcpsrcaddr.String(), err)
						break
					}
				}
			}
			if n > 0 {
				clientConn.fm.WriteSendBuffer(bytes[:n])
			}
		}

		clientConn.fm.Update()

		sendlist := clientConn.fm.getSendList()

		clientConn.activeTime = now

		for e := sendlist.Front(); e != nil; e = e.Next() {

			f := e.Value.(Frame)
			mb, err := f.Marshal(0)
			if err != nil {
				loggo.Error("Error tcp Marshal %s %s %s", uuid, tcpsrcaddr.String(), err)
				break
			}

			p.sequence++

			p.sendPacket++
			p.sendPacketSize += (uint64)(f.size)

			sendICMP(p.id, p.sequence, *p.conn, p.ipaddrServer, p.targetAddr, clientConn.id, (uint32)(DATA), mb,
				p.sproto, p.rproto, p.catch, p.key)
		}
	}

	loggo.Info("close inactive conn %s %s", clientConn.id, clientConn.tcpaddr.String())
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
			if neterr, ok := err.(*net.OpError); ok {
				if neterr.Timeout() {
					// Read timeout
					continue
				} else {
					loggo.Error("Error read udp %s", err)
					continue
				}
			}
		}

		now := time.Now()
		clientConn := p.localAddrToConnMap[srcaddr.String()]
		if clientConn == nil {
			uuid := UniqueId()
			clientConn = &ClientConn{ipaddr: srcaddr, id: uuid, activeTime: now, close: false}
			p.localAddrToConnMap[srcaddr.String()] = clientConn
			p.localIdToConnMap[uuid] = clientConn
			loggo.Info("client accept new local udp %s %s", uuid, srcaddr.String())
		}

		clientConn.activeTime = now
		sendICMP(p.id, p.sequence, *p.conn, p.ipaddrServer, p.targetAddr, clientConn.id, (uint32)(DATA), bytes[:n],
			p.sproto, p.rproto, p.catch, p.key)

		p.sequence++

		p.sendPacket++
		p.sendPacketSize += (uint64)(n)
	}
}

func (p *Client) processPacket(packet *Packet) {

	if packet.rproto >= 0 {
		return
	}

	if packet.key != p.key {
		return
	}

	if packet.echoId != p.id {
		return
	}

	if packet.msgType == PING {
		t := time.Time{}
		t.UnmarshalBinary(packet.data)
		d := time.Now().Sub(t)
		loggo.Info("pong from %s %s", packet.src.String(), d.String())
		return
	}

	//loggo.Debug("processPacket %s %s %d", packet.id, packet.src.String(), len(packet.data))

	clientConn := p.localIdToConnMap[packet.id]
	if clientConn == nil {
		//loggo.Debug("processPacket no conn %s ", packet.id)
		return
	}

	addr := clientConn.ipaddr

	now := time.Now()
	clientConn.activeTime = now

	if packet.msgType == CATCH {
		p.recvCatchPacket++
	}

	_, err := p.listenConn.WriteToUDP(packet.data, addr)
	if err != nil {
		loggo.Error("WriteToUDP Error read udp %s", err)
		clientConn.close = true
		return
	}

	p.recvPacket++
	p.recvPacketSize += (uint64)(len(packet.data))
}

func (p *Client) Close(clientConn *ClientConn) {
	if p.localIdToConnMap[clientConn.id] != nil {
		delete(p.localIdToConnMap, clientConn.id)
		delete(p.localAddrToConnMap, clientConn.ipaddr.String())
	}
}

func (p *Client) checkTimeoutConn() {

	if p.tcpmode {
		return
	}

	now := time.Now()
	for _, conn := range p.localIdToConnMap {
		diff := now.Sub(conn.activeTime)
		if diff > time.Second*(time.Duration(p.timeout)) {
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
		sendICMP(p.id, p.sequence, *p.conn, p.ipaddrServer, p.targetAddr, "", (uint32)(PING), b,
			p.sproto, p.rproto, p.catch, p.key)
		loggo.Info("ping %s %s %d %d %d %d", p.addrServer, now.String(), p.sproto, p.rproto, p.id, p.sequence)
		p.sequence++
	}
}

func (p *Client) showNet() {
	loggo.Info("send %dPacket/s %dKB/s recv %dPacket/s %dKB/s sendCatch %d/s recvCatch %d/s",
		p.sendPacket, p.sendPacketSize/1024, p.recvPacket, p.recvPacketSize/1024, p.sendCatchPacket, p.recvCatchPacket)
	p.sendPacket = 0
	p.recvPacket = 0
	p.sendPacketSize = 0
	p.recvPacketSize = 0
	p.sendCatchPacket = 0
	p.recvCatchPacket = 0
}

func (p *Client) sendCatch() {
	if p.catch > 0 {
		for _, conn := range p.localIdToConnMap {
			sendICMP(p.id, p.sequence, *p.conn, p.ipaddrServer, p.targetAddr, conn.id, (uint32)(CATCH), make([]byte, 0),
				p.sproto, p.rproto, p.catch, p.key)
			p.sequence++
			p.sendCatchPacket++
		}
	}
}
