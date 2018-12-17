package pingtunnel

import (
	"fmt"
	"golang.org/x/net/icmp"
	"net"
	"time"
)

func NewServer(target string) (*Server, error) {

	ipaddrTarget, err := net.ResolveTCPAddr("tcp", target)
	if err != nil {
		return nil, err
	}

	return &Server{
		ipaddrTarget: ipaddrTarget,
		addrTarget:   target,
	}, nil
}

type Server struct {
	ipaddrTarget *net.TCPAddr
	addrTarget   string

	conn net.PacketConn
}

func (p *Server) TargetAddr() string {
	return p.addrTarget
}

func (p *Server) TargetIPAddr() *net.TCPAddr {
	return p.ipaddrTarget
}

func (p *Server) Run() {

	conn, err := icmp.ListenPacket("ip4:icmp", "")
	if err != nil {
		fmt.Printf("Error listening for ICMP packets: %s\n", err.Error())
		return
	}
	p.conn = conn

	p.Recv()
}

func (p *Server) Recv() error {

	for {
		bytes := make([]byte, 512)
		p.conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
		n, srcaddr, err := p.conn.ReadFrom(bytes)

		if err != nil {
			if neterr, ok := err.(*net.OpError); ok {
				if neterr.Timeout() {
					// Read timeout
					continue
				} else {
					return err
				}
			}
		}

		bytes1 := ipv4Payload(bytes)

		var m *icmp.Message
		if m, err = icmp.ParseMessage(protocolICMP, bytes1[:n]); err != nil {
			fmt.Println("Error parsing icmp message")
			return err
		}

		fmt.Printf("%d %d %d %s \n", m.Type, m.Code, n, srcaddr)
	}
}

func (p *Server) listen(netProto string, source string) *icmp.PacketConn {

	conn, err := icmp.ListenPacket(netProto, source)
	if err != nil {
		fmt.Printf("Error listening for ICMP packets: %s\n", err.Error())
		return nil
	}
	return conn
}
