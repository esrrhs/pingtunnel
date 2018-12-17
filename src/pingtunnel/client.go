package pingtunnel

import (
	"fmt"
	"golang.org/x/net/icmp"
	"net"
	"time"
)

func NewClient(addr string, target string) (*Client, error) {

	ipaddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}

	ipaddrTarget, err := net.ResolveIPAddr("ip", target)
	if err != nil {
		return nil, err
	}

	return &Client{
		ipaddr:       ipaddr,
		addr:         addr,
		ipaddrTarget: ipaddrTarget,
		addrTarget:   target,
	}, nil
}

type Client struct {
	ipaddr *net.UDPAddr
	addr   string

	ipaddrTarget *net.IPAddr
	addrTarget   string

	conn       *icmp.PacketConn
	listenConn *net.UDPConn

	localConnToIdMap map[string]uint32
	localIdToConnMap map[uint32]*net.UDPAddr
}

func (p *Client) Addr() string {
	return p.addr
}

func (p *Client) IPAddr() *net.UDPAddr {
	return p.ipaddr
}

func (p *Client) TargetAddr() string {
	return p.addrTarget
}

func (p *Client) TargetIPAddr() *net.IPAddr {
	return p.ipaddrTarget
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

	p.localConnToIdMap = make(map[string]uint32)
	p.localIdToConnMap = make(map[uint32]*net.UDPAddr)

	go p.Accept()

	recv := make(chan *Packet, 1000)
	go recvICMP(*p.conn, recv)

	for {
		select {
		case r := <-recv:
			p.processPacket(r)
		}
	}
}

func (p *Client) Accept() error {

	fmt.Println("client waiting local accept")

	bytes := make([]byte, 10240)

	for {
		p.conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
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

		uuid := p.localConnToIdMap[srcaddr.String()]
		if uuid == 0 {
			uuid = UniqueId()
			p.localConnToIdMap[srcaddr.String()] = uuid
			p.localIdToConnMap[uuid] = srcaddr
			fmt.Printf("client accept new local %d %s\n", uuid, srcaddr.String())
		}

		sendICMP(*p.conn, p.ipaddrTarget, uuid, (uint32)(DATA), bytes[:n])
	}
}

func (p *Client) processPacket(packet *Packet) {

	fmt.Printf("processPacket %d %s %d\n", packet.id, packet.src.String(), len(packet.data))

	addr := p.localIdToConnMap[packet.id]
	if addr == nil {
		fmt.Printf("processPacket no conn %d \n", packet.id)
		return
	}

	_, err := p.listenConn.WriteToUDP(packet.data, addr)
	if err != nil {
		fmt.Printf("WriteToUDP Error read udp %s\n", err)
		p.localConnToIdMap[addr.String()] = 0
		p.localIdToConnMap[packet.id] = nil
		return
	}
}
