package pingtunnel

import (
	"fmt"
	"golang.org/x/net/icmp"
	"net"
	"strconv"
	"time"
)

func NewServer() (*Server, error) {
	return &Server{
	}, nil
}

type Server struct {
	conn *icmp.PacketConn

	localConnMap map[uint32]*Conn
}

type Conn struct {
	ipaddrTarget *net.UDPAddr
	conn         *net.UDPConn
	id           uint32
}

func (p *Server) Run() {

	conn, err := icmp.ListenPacket("ip4:icmp", "")
	if err != nil {
		fmt.Printf("Error listening for ICMP packets: %s\n", err.Error())
		return
	}
	p.conn = conn

	p.localConnMap = make(map[uint32]*Conn)

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

		addr := ":" + strconv.Itoa((int)(packet.target))
		ipaddrTarget, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			fmt.Printf("Error ResolveUDPAddr for udp addr: %s %s\n", addr, err.Error())
			return
		}

		targetConn, err := net.DialUDP("udp", nil, ipaddrTarget)
		if err != nil {
			fmt.Printf("Error listening for udp packets: %s\n", err.Error())
			return
		}
		udpConn = &Conn{conn: targetConn, ipaddrTarget: ipaddrTarget, id: id}
		p.localConnMap[id] = udpConn
		go p.Recv(udpConn, id, packet.src)
	}

	_, err := udpConn.conn.Write(packet.data)
	if err != nil {
		fmt.Printf("WriteToUDP Error %s\n", err)
		p.Close(udpConn)
		return
	}
}

func (p *Server) Recv(conn *Conn, id uint32, src *net.IPAddr) {

	fmt.Printf("server waiting target response %s\n", conn.ipaddrTarget.String())

	bytes := make([]byte, 10240)

	for {
		conn.conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
		n, _, err := conn.conn.ReadFromUDP(bytes)
		if err != nil {
			if neterr, ok := err.(*net.OpError); ok {
				if neterr.Timeout() {
					// Read timeout
					continue
				} else {
					fmt.Printf("ReadFromUDP Error read udp %s\n", err)
					p.Close(conn)
					return
				}
			}
		}

		sendICMP(*p.conn, src, 0, id, (uint32)(DATA), bytes[:n])
	}
}

func (p *Server) Close(conn *Conn) {
	if p.localConnMap[conn.id] != nil {
		conn.conn.Close()
		p.localConnMap[conn.id] = nil
	}
}
