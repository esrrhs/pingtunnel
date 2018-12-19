package pingtunnel

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"io"
	"net"
	"syscall"
	"time"
)

const (
	DATA uint32 = 0x01010101
)

// An Echo represents an ICMP echo request or reply message body.
type MyMsg struct {
	TYPE    uint32
	ID      string
	TARGET  string
	Data    []byte
	RPROTO  uint16
	ENDTYPE uint32
}

// Len implements the Len method of MessageBody interface.
func (p *MyMsg) Len(proto int) int {
	if p == nil {
		return 0
	}
	return 4 + p.LenString(p.ID) + p.LenString(p.TARGET) + p.LenData(p.Data) + 2 + 4
}

func (p *MyMsg) LenString(s string) int {
	return 2 + len(s)
}

func (p *MyMsg) LenData(data []byte) int {
	return 2 + len(data)
}

// Marshal implements the Marshal method of MessageBody interface.
func (p *MyMsg) Marshal(proto int) ([]byte, error) {

	b := make([]byte, p.Len(proto))

	binary.BigEndian.PutUint32(b[:4], uint32(p.TYPE))

	id := p.MarshalString(p.ID)
	copy(b[4:], id)

	target := p.MarshalString(p.TARGET)
	copy(b[4+p.LenString(p.ID):], target)

	data := p.MarshalData(p.Data)
	copy(b[4+p.LenString(p.ID)+p.LenString(p.TARGET):], data)

	binary.BigEndian.PutUint16(b[4+p.LenString(p.ID)+p.LenString(p.TARGET)+p.LenData(p.Data):], uint16(p.RPROTO))

	binary.BigEndian.PutUint32(b[4+p.LenString(p.ID)+p.LenString(p.TARGET)+p.LenData(p.Data)+2:], uint32(p.ENDTYPE))

	return b, nil
}

func (p *MyMsg) MarshalString(s string) []byte {
	b := make([]byte, p.LenString(s))
	binary.BigEndian.PutUint16(b[:2], uint16(len(s)))
	copy(b[2:], []byte(s))
	return b
}

func (p *MyMsg) MarshalData(data []byte) []byte {
	b := make([]byte, p.LenData(data))
	binary.BigEndian.PutUint16(b[:2], uint16(len(data)))
	copy(b[2:], []byte(data))
	return b
}

// Marshal implements the Marshal method of MessageBody interface.
func (p *MyMsg) Unmarshal(b []byte) error {
	defer func() {
		recover()
	}()

	p.TYPE = binary.BigEndian.Uint32(b[:4])

	p.ID = p.UnmarshalString(b[4:])

	p.TARGET = p.UnmarshalString(b[4+p.LenString(p.ID):])

	p.Data = p.UnmarshalData(b[4+p.LenString(p.ID)+p.LenString(p.TARGET):])

	p.RPROTO = binary.BigEndian.Uint16(b[4+p.LenString(p.ID)+p.LenString(p.TARGET)+p.LenData(p.Data):])

	p.ENDTYPE = binary.BigEndian.Uint32(b[4+p.LenString(p.ID)+p.LenString(p.TARGET)+p.LenData(p.Data)+2:])

	return nil
}

func (p *MyMsg) UnmarshalString(b []byte) string {
	len := binary.BigEndian.Uint16(b[:2])
	if len > 32 || len < 0 {
		panic(nil)
	}
	data := make([]byte, len)
	copy(data, b[2:])
	return string(data)
}

func (p *MyMsg) UnmarshalData(b []byte) []byte {
	len := binary.BigEndian.Uint16(b[:2])
	if len > 2048 || len < 0 {
		panic(nil)
	}
	data := make([]byte, len)
	copy(data, b[2:])
	return data
}

func sendICMP(conn icmp.PacketConn, server *net.IPAddr, target string, connId string, msgType uint32, data []byte, sproto int, rproto int) {

	m := &MyMsg{
		ID:      connId,
		TYPE:    msgType,
		TARGET:  target,
		Data:    data,
		RPROTO:  (uint16)(rproto),
		ENDTYPE: msgType,
	}

	msg := &icmp.Message{
		Type: (ipv4.ICMPType)(sproto),
		Code: 0,
		Body: m,
	}

	bytes, err := msg.Marshal(nil)
	if err != nil {
		fmt.Printf("sendICMP Marshal error %s %s\n", server.String(), err)
		return
	}

	for {
		if _, err := conn.WriteTo(bytes, server); err != nil {
			if neterr, ok := err.(*net.OpError); ok {
				if neterr.Err == syscall.ENOBUFS {
					continue
				}
			}
			fmt.Printf("sendICMP WriteTo error %s %s\n", server.String(), err)
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

		if my.TYPE != (uint32)(DATA) || my.ENDTYPE != (uint32)(DATA) {
			fmt.Printf("processPacket diff type %s %d %d \n", my.ID, my.TYPE, my.ENDTYPE)
			continue
		}

		if my.Data == nil {
			fmt.Printf("processPacket data nil %s\n", my.ID)
			return
		}

		recv <- &Packet{data: my.Data, id: my.ID, target: my.TARGET, src: srcaddr.(*net.IPAddr), rproto: (int)(my.RPROTO)}
	}
}

type Packet struct {
	data   []byte
	id     string
	target string
	src    *net.IPAddr
	rproto int
}

func UniqueId() string {
	b := make([]byte, 48)

	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return ""
	}
	return GetMd5String(base64.URLEncoding.EncodeToString(b))
}

func GetMd5String(s string) string {
	h := md5.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}
