package pingtunnel

import (
	"encoding/binary"
	"fmt"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"net"
	"syscall"
	"time"
)

func NewPingTunnelServer(addr string, target string) (*PingTunnelServer, error) {

	ipaddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}

	ipaddrTarget, err := net.ResolveTCPAddr("tcp", target)
	if err != nil {
		return nil, err
	}

	return &PingTunnelServer{
		ipaddr:       ipaddr,
		addr:         addr,
		ipaddrTarget: ipaddrTarget,
		addrTarget:   target,
	}, nil
}

func NewPingTunnelClient(addr string, target string) (*PingTunnelClient, error) {

	ipaddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}

	ipaddrTarget, err := net.ResolveTCPAddr("tcp", target)
	if err != nil {
		return nil, err
	}

	return &PingTunnelClient{
		ipaddr:       ipaddr,
		addr:         addr,
		ipaddrTarget: ipaddrTarget,
		addrTarget:   target,
	}, nil
}

type PingTunnelClient struct {
	ipaddr *net.TCPAddr
	addr   string

	ipaddrTarget *net.TCPAddr
	addrTarget   string

	conn net.Conn
}

type PingTunnelServer struct {
	ipaddr *net.TCPAddr
	addr   string

	ipaddrTarget *net.TCPAddr
	addrTarget   string

	conn net.PacketConn
}

func (p *PingTunnelServer) Addr() string {
	return p.addr
}

func (p *PingTunnelServer) IPAddr() *net.TCPAddr {
	return p.ipaddr
}

func (p *PingTunnelServer) TargetAddr() string {
	return p.addrTarget
}

func (p *PingTunnelServer) TargetIPAddr() *net.TCPAddr {
	return p.ipaddrTarget
}

func (p *PingTunnelClient) Addr() string {
	return p.addr
}

func (p *PingTunnelClient) IPAddr() *net.TCPAddr {
	return p.ipaddr
}

func (p *PingTunnelClient) TargetAddr() string {
	return p.addrTarget
}

func (p *PingTunnelClient) TargetIPAddr() *net.TCPAddr {
	return p.ipaddrTarget
}

func (p *PingTunnelServer) Run() {
	conn, err := net.ListenPacket("udp4", p.addr)
	if err != nil {
		fmt.Println(err)
		return
	}

	p.conn = conn

	p.Recv()
}

func (p *PingTunnelClient) Run() {

	conn, err := net.Dial("udp4", p.addrTarget)
	if err != nil {
		fmt.Println(err)
		return
	}

	p.conn = conn

	n := 0
	for {
		p.Send(n, []byte("haha"))
		n++
	}
}

func (p *PingTunnelClient) Send(id int, data []byte) error {

	body := &Msg{
		ID:   id,
		Data: data,
	}

	msg := &icmp.Message{
		Type: ipv4.ICMPTypeExtendedEchoRequest,
		Code: 0,
		Body: body,
	}

	bytes, err := msg.Marshal(nil)
	if err != nil {
		return err
	}

	for {
		if _, err := p.conn.Write(bytes); err != nil {
			if neterr, ok := err.(*net.OpError); ok {
				if neterr.Err == syscall.ENOBUFS {
					continue
				}
			}
		}
		break
	}

	fmt.Printf("send %d\n", id)

	return nil
}

func (p *PingTunnelServer) Recv() error {

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

		var sbytes []byte
		sbytes = ipv4Payload(bytes)

		var m *icmp.Message
		if m, err = icmp.ParseMessage(1, sbytes[:n]); err != nil {
			return fmt.Errorf("Error parsing icmp message")
		}

		fmt.Printf("%d %d %d %s \n", m.Type, m.Code, n, srcaddr)
	}
}

func ipv4Payload(b []byte) []byte {
	if len(b) < ipv4.HeaderLen {
		return b
	}
	hdrlen := int(b[0]&0x0f) << 2
	return b[hdrlen:]
}

type Msg struct {
	ID   int    // identifier
	Data []byte // data
}

func (p *Msg) Len(proto int) int {
	if p == nil {
		return 0
	}
	return 4 + len(p.Data)
}

func (p *Msg) Marshal(proto int) ([]byte, error) {
	b := make([]byte, 4+len(p.Data))
	binary.BigEndian.PutUint32(b, uint32(p.ID))
	copy(b[4:], p.Data)
	return b, nil
}

func (p *PingTunnelServer) listen(netProto string, source string) *icmp.PacketConn {

	conn, err := icmp.ListenPacket(netProto, source)
	if err != nil {
		fmt.Printf("Error listening for ICMP packets: %s\n", err.Error())
		return nil
	}
	return conn
}
