package pingtunnel

import (
	"fmt"
	"golang.org/x/net/icmp"
	"net"
	"time"
)

func NewClient(addr string, server string, target string, timeout int, sproto int, rproto int) (*Client, error) {

	ipaddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}

	ipaddrServer, err := net.ResolveIPAddr("ip", server)
	if err != nil {
		return nil, err
	}

	return &Client{
		ipaddr:       ipaddr,
		addr:         addr,
		ipaddrServer: ipaddrServer,
		addrServer:   server,
		targetAddr:   target,
		timeout:      timeout,
		sproto:       sproto,
		rproto:       rproto,
	}, nil
}

type Client struct {
	timeout int
	sproto  int
	rproto  int

	ipaddr *net.UDPAddr
	addr   string

	ipaddrServer *net.IPAddr
	addrServer   string

	targetAddr string

	conn       *icmp.PacketConn
	listenConn *net.UDPConn

	localAddrToConnMap map[string]*ClientConn
	localIdToConnMap   map[string]*ClientConn
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

	for {
		select {
		case <-interval.C:
			p.checkTimeoutConn()
			p.ping()
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
		sendICMP(*p.conn, p.ipaddrServer, p.targetAddr, clientConn.id, (uint32)(DATA), bytes[:n], p.sproto, p.rproto)
	}
}

func (p *Client) processPacket(packet *Packet) {

	fmt.Printf("processPacket %s %s %d\n", packet.id, packet.src.String(), len(packet.data))

	clientConn := p.localIdToConnMap[packet.id]
	if clientConn == nil {
		fmt.Printf("processPacket no conn %s \n", packet.id)
		return
	}

	addr := clientConn.ipaddr

	now := time.Now()
	clientConn.activeTime = now

	_, err := p.listenConn.WriteToUDP(packet.data, addr)
	if err != nil {
		fmt.Printf("WriteToUDP Error read udp %s\n", err)
		clientConn.close = true
		return
	}
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
	now := time.Now()
	b, _ := now.MarshalBinary()
	sendICMP(*p.conn, p.ipaddrServer, p.targetAddr, "", (uint32)(PING), b, p.sproto, p.rproto)
	fmt.Printf("ping %s %s\n", p.addrServer, now.String())
}
