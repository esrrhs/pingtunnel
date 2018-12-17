package pingtunnel

import (
	"fmt"
	"golang.org/x/net/icmp"
	"net"
	"time"
)

func NewServer(target string) (*Server, error) {

	ipaddrTarget, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return nil, err
	}

	return &Server{
		ipaddrTarget: ipaddrTarget,
		addrTarget:   target,
	}, nil
}

type Server struct {
	ipaddrTarget *net.UDPAddr
	addrTarget   string

	conn *icmp.PacketConn

	localConnMap map[uint32]*net.UDPConn
}

func (p *Server) TargetAddr() string {
	return p.addrTarget
}

func (p *Server) TargetIPAddr() *net.UDPAddr {
	return p.ipaddrTarget
}

func (p *Server) Run() {

	conn, err := icmp.ListenPacket("ip4:icmp", "")
	if err != nil {
		fmt.Printf("Error listening for ICMP packets: %s\n", err.Error())
		return
	}
	p.conn = conn

	p.localConnMap = make(map[uint32]*net.UDPConn)

	recv := make(chan *Packet, 1000)
	go recvICMP(*p.conn, recv)

	for {
		select {
		case r := <-recv:
			p.processPacket(r)
		}
	}
}

func (p *Server) processPacket(packet *Packet) {

	fmt.Printf("processPacket %d %s %d\n", packet.id, packet.src.String(), len(packet.data))

	id := packet.id
	udpConn := p.localConnMap[id]
	if udpConn == nil {
		targetConn, err := net.ListenUDP("udp", p.ipaddrTarget)
		if err != nil {
			fmt.Printf("Error listening for udp packets: %s\n", err.Error())
			return
		}
		udpConn = targetConn
		p.localConnMap[id] = udpConn
		go p.Recv(udpConn, id, packet.src)
	}

	_, err := udpConn.WriteToUDP(packet.data, p.ipaddrTarget)
	if err != nil {
		fmt.Printf("WriteToUDP Error read udp %s\n", err)
		p.localConnMap[id] = nil
		return
	}
}

func (p *Server) Recv(conn *net.UDPConn, id uint32, src *net.IPAddr) {

	fmt.Println("server waiting target response")

	bytes := make([]byte, 10240)

	for {
		p.conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
		n, _, err := conn.ReadFromUDP(bytes)
		if err != nil {
			if neterr, ok := err.(*net.OpError); ok {
				if neterr.Timeout() {
					// Read timeout
					continue
				} else {
					fmt.Printf("ReadFromUDP Error read udp %s\n", err)
					p.localConnMap[id] = nil
					return
				}
			}
		}

		sendICMP(*p.conn, src, id, (uint32)(DATA), bytes[:n])
	}
}
