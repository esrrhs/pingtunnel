package pingtunnel

import (
	"fmt"
	"golang.org/x/net/icmp"
	"net"
	"time"
)

func NewServer(timeout int) (*Server, error) {
	return &Server{
		timeout: timeout,
	}, nil
}

type Server struct {
	timeout int

	conn *icmp.PacketConn

	localConnMap map[string]*ServerConn

	sendPacket     uint64
	recvPacket     uint64
	sendPacketSize uint64
	recvPacketSize uint64
}

type ServerConn struct {
	ipaddrTarget *net.UDPAddr
	conn         *net.UDPConn
	id           string
	activeTime   time.Time
	close        bool
	rproto       int
}

func (p *Server) Run() {

	conn, err := icmp.ListenPacket("ip4:icmp", "")
	if err != nil {
		fmt.Printf("Error listening for ICMP packets: %s\n", err.Error())
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

	if packet.msgType == PING {
		t := time.Time{}
		t.UnmarshalBinary(packet.data)
		fmt.Printf("ping from %s %s %d\n", packet.src.String(), t.String(), packet.rproto)
		sendICMP(*p.conn, packet.src, "", "", (uint32)(PING), packet.data, packet.rproto, 0)
		return
	}

	fmt.Printf("processPacket %s %s %d\n", packet.id, packet.src.String(), len(packet.data))

	now := time.Now()

	id := packet.id
	udpConn := p.localConnMap[id]
	if udpConn == nil {

		addr := packet.target
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
		udpConn = &ServerConn{conn: targetConn, ipaddrTarget: ipaddrTarget, id: id, activeTime: now, close: false, rproto: packet.rproto}
		p.localConnMap[id] = udpConn
		go p.Recv(udpConn, id, packet.src)
	}

	udpConn.activeTime = now

	_, err := udpConn.conn.Write(packet.data)
	if err != nil {
		fmt.Printf("WriteToUDP Error %s\n", err)
		udpConn.close = true
		return
	}

	p.recvPacket++
	p.recvPacketSize += (uint64)(len(packet.data))
}

func (p *Server) Recv(conn *ServerConn, id string, src *net.IPAddr) {

	fmt.Printf("server waiting target response %s -> %s %s\n", conn.ipaddrTarget.String(), conn.id, conn.conn.LocalAddr().String())

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
					conn.close = true
					return
				}
			}
		}

		now := time.Now()
		conn.activeTime = now

		sendICMP(*p.conn, src, "", id, (uint32)(DATA), bytes[:n], conn.rproto, 0)

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
			fmt.Printf("close inactive conn %s %s\n", id, conn.ipaddrTarget.String())
			p.Close(conn)
		}
	}
}

func (p *Server) showNet() {
	fmt.Printf("send %d/s %dKB/s recv %d/s %dKB/s\n", p.sendPacket, p.sendPacket/1024, p.recvPacket, p.recvPacket/1024)
	p.sendPacket = 0
	p.recvPacket = 0
	p.sendPacketSize = 0
	p.recvPacketSize = 0
}
