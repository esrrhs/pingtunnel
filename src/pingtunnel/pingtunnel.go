package pingtunnel

import (
	"encoding/binary"
	"fmt"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"net"
	"sync/atomic"
	"syscall"
	"time"
)

type MSGID int

const (
	DATA MSGID = 0xDEADBEEF
)

const (
	protocolICMP = 1
)

// An Echo represents an ICMP echo request or reply message body.
type MyMsg struct {
	ID   uint32
	TYPE uint32
	Data []byte
}

// Len implements the Len method of MessageBody interface.
func (p *MyMsg) Len(proto int) int {
	if p == nil {
		return 0
	}
	return 8 + len(p.Data)
}

// Marshal implements the Marshal method of MessageBody interface.
func (p *MyMsg) Marshal(proto int) ([]byte, error) {
	b := make([]byte, p.Len(proto))
	binary.BigEndian.PutUint32(b[:4], uint32(p.ID))
	binary.BigEndian.PutUint32(b[4:8], uint32(p.TYPE))
	copy(b[8:], p.Data)
	return b, nil
}

// Marshal implements the Marshal method of MessageBody interface.
func (p *MyMsg) Unmarshal(b []byte) error {
	p.ID = binary.BigEndian.Uint32(b[:4])
	p.TYPE = binary.BigEndian.Uint32(b[4:8])
	p.Data = make([]byte, len(b[8:]))
	copy(p.Data, b[8:])
	return nil
}

var uuid uint32

func UniqueId() uint32 {
	newValue := atomic.AddUint32(&uuid, 1)
	return (uint32)(newValue)
}

func sendICMP(conn icmp.PacketConn, target *net.IPAddr, connId uint32, msgType uint32, data []byte) {

	m := &MyMsg{
		ID:   connId,
		TYPE: msgType,
		Data: data,
	}

	msg := &icmp.Message{
		Type: ipv4.ICMPTypeExtendedEchoRequest,
		Code: 0,
		Body: m,
	}

	bytes, err := msg.Marshal(nil)
	if err != nil {
		fmt.Printf("sendICMP Marshal error %s %s\n", target.String(), err)
		return
	}

	for {
		if _, err := conn.WriteTo(bytes, target); err != nil {
			if neterr, ok := err.(*net.OpError); ok {
				if neterr.Err == syscall.ENOBUFS {
					continue
				}
			}
			fmt.Printf("sendICMP WriteTo error %s %s\n", target.String(), err)
		}
		break
	}

	return
}

func recvICMP(conn icmp.PacketConn, recv chan<- *Packet) {

	bytes := make([]byte, 10240)
	for {
		conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
		n, srcaddr, err := conn.ReadFrom(bytes)

		if err != nil {
			if neterr, ok := err.(*net.OpError); ok {
				if neterr.Timeout() {
					// Read timeout
					continue
				} else {
					fmt.Printf("Error read icmp message %s\n", err)
					continue
				}
			}
		}

		my := &MyMsg{
		}
		my.Unmarshal(bytes[4:n])

		if my.TYPE != (uint32)(DATA) {
			fmt.Printf("processPacket diff type %d \n", my.TYPE)
			continue
		}

		recv <- &Packet{data: my.Data, id: my.ID, src: srcaddr.(*net.IPAddr)}
	}
}

type Packet struct {
	data []byte
	id   uint32
	src  *net.IPAddr
}
