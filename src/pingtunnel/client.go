package pingtunnel

import (
	"encoding/json"
	"fmt"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"net"
	"syscall"
)

func NewClient(addr string, target string) (*Client, error) {

	ipaddr, err := net.ResolveTCPAddr("tcp", addr)
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
	ipaddr *net.TCPAddr
	addr   string

	ipaddrTarget *net.IPAddr
	addrTarget   string

	conn       *icmp.PacketConn
	listenConn *net.TCPListener
}

func (p *Client) Addr() string {
	return p.addr
}

func (p *Client) IPAddr() *net.TCPAddr {
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

	listener, err := net.ListenTCP("tcp", p.ipaddr)
	if err != nil {
		fmt.Printf("Error listening for tcp packets: %s\n", err.Error())
		return
	}

	p.listenConn = listener

	p.Accept()
}

func (p *Client) Accept() error {

	fmt.Println("client waiting local accept")

	for {
		localConn, err := p.listenConn.AcceptTCP()
		if err != nil {
			fmt.Println(err)
			continue
		}

		localConn.SetLinger(0)
		go p.handleConn(*localConn)
	}
}

func (p *Client) handleConn(conn net.TCPConn) {
	defer conn.Close()

	uuid := UniqueId()

	fmt.Printf("client new conn %s %s", conn.RemoteAddr().String(), uuid)

	data, err := json.Marshal(RegisterData{localaddr: conn.RemoteAddr().String()})
	if err != nil {
		fmt.Printf("Unable to marshal data %s\n", err)
		return
	}

	for {
		p.sendICMP(uuid, REGISTER, data)
	}
}

func (p *Client) sendICMP(connId string, msgType MSGID, data []byte) error {

	body := &Msg{
		ID:   connId,
		TYPE: (int)(msgType),
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
		if _, err := (*p.conn).WriteTo(bytes, p.ipaddrTarget); err != nil {
			if neterr, ok := err.(*net.OpError); ok {
				if neterr.Err == syscall.ENOBUFS {
					continue
				}
			}
			fmt.Printf("sendICMP error %s %s\n", p.ipaddrTarget.String(), err)
		}
		break
	}

	return nil
}
