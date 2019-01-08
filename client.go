package pingtunnel

import (
	"fmt"
	"golang.org/x/net/icmp"
	"math"
	"math/rand"
	"net"
	"time"
)

func NewClient(addr string, server string, target string, timeout int, sproto int, rproto int, catch int, key int) (*Client, error) {

	ipaddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}

	ipaddrServer, err := net.ResolveIPAddr("ip", server)
	if err != nil {
		return nil, err
	}

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return &Client{
		id:           r.Intn(math.MaxInt16),
		ipaddr:       ipaddr,
		addr:         addr,
		ipaddrServer: ipaddrServer,
		addrServer:   server,
		targetAddr:   target,
		timeout:      timeout,
		sproto:       sproto,
		rproto:       rproto,
		catch:        catch,
		key:          key,
	}, nil
}

type Client struct {
	id       int
	sequence int

	timeout int
	sproto  int
	rproto  int
	catch   int
	key     int

	ipaddr *net.UDPAddr
	addr   string

	ipaddrServer *net.IPAddr
	addrServer   string

	targetAddr string

	conn       *icmp.PacketConn
	listenConn *net.UDPConn

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
	id         string
	activeTime time.Time
	close      bool
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
		fmt.Printf("Error listening for ICMP packets: %s\n", err.Error())
		return
	}
	defer conn.Close()
	p.conn = conn

	listener, err := net.ListenUDP("udp", p.ipaddr)
	if err != nil {
		fmt.Printf("Error listening for udp packets: %s\n", err.Error())
		return
	}
	defer listener.Close()
	p.listenConn = listener

	p.localAddrToConnMap = make(map[string]*ClientConn)
	p.localIdToConnMap = make(map[string]*ClientConn)

	go p.Accept()

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

func (p *Client) Accept() error {

	fmt.Println("client waiting local accept")

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
					fmt.Printf("Error read udp %s\n", err)
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
			fmt.Printf("client accept new local %s %s\n", uuid, srcaddr.String())
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

	if packet.msgType == PING {
		t := time.Time{}
		t.UnmarshalBinary(packet.data)
		d := time.Now().Sub(t)
		fmt.Printf("pong from %s %s\n", packet.src.String(), d.String())
		return
	}

	//fmt.Printf("processPacket %s %s %d\n", packet.id, packet.src.String(), len(packet.data))

	clientConn := p.localIdToConnMap[packet.id]
	if clientConn == nil {
		//fmt.Printf("processPacket no conn %s \n", packet.id)
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
		fmt.Printf("WriteToUDP Error read udp %s\n", err)
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
	now := time.Now()
	for _, conn := range p.localIdToConnMap {
		diff := now.Sub(conn.activeTime)
		if diff > time.Second*(time.Duration(p.timeout)) {
			conn.close = true
		}
	}

	for id, conn := range p.localIdToConnMap {
		if conn.close {
			fmt.Printf("close inactive conn %s %s\n", id, conn.ipaddr.String())
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
		fmt.Printf("ping %s %s %d %d %d %d\n", p.addrServer, now.String(), p.sproto, p.rproto, p.id, p.sequence)
		p.sequence++
	}
}

func (p *Client) showNet() {
	fmt.Printf("send %dPacket/s %dKB/s recv %dPacket/s %dKB/s sendCatch %d/s recvCatch %d/s\n",
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
