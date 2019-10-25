package pingtunnel

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"github.com/esrrhs/go-engine/src/loggo"
	"github.com/golang/protobuf/proto"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"io"
	"net"
	"syscall"
	"time"
)

func sendICMP(id int, sequence int, conn icmp.PacketConn, server *net.IPAddr, target string,
	connId string, msgType uint32, data []byte, sproto int, rproto int, key int,
	tcpmode int, tcpmode_buffer_size int, tcpmode_maxwin int, tcpmode_resend_time int) {

	m := &MyMsg{
		Id:                  connId,
		Type:                (int32)(msgType),
		Target:              target,
		Data:                data,
		Rproto:              (int32)(rproto),
		Key:                 (int32)(key),
		Tcpmode:             (int32)(tcpmode),
		TcpmodeBuffersize:   (int32)(tcpmode_buffer_size),
		TcpmodeMaxwin:       (int32)(tcpmode_maxwin),
		TcpmodeResendTimems: (int32)(tcpmode_resend_time),
		Magic:               (int32)(MyMsg_MAGIC),
	}

	mb, err := proto.Marshal(m)
	if err != nil {
		loggo.Error("sendICMP Marshal MyMsg error %s %s", server.String(), err)
		return
	}

	body := &icmp.Echo{
		ID:   id,
		Seq:  sequence,
		Data: mb,
	}

	msg := &icmp.Message{
		Type: (ipv4.ICMPType)(sproto),
		Code: 0,
		Body: body,
	}

	bytes, err := msg.Marshal(nil)
	if err != nil {
		loggo.Error("sendICMP Marshal error %s %s", server.String(), err)
		return
	}

	for {
		if _, err := conn.WriteTo(bytes, server); err != nil {
			if neterr, ok := err.(*net.OpError); ok {
				if neterr.Err == syscall.ENOBUFS {
					continue
				}
			}
			loggo.Error("sendICMP WriteTo error %s %s", server.String(), err)
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
			nerr, ok := err.(net.Error)
			if !ok || !nerr.Timeout() {
				loggo.Error("Error read icmp message %s", err)
				continue
			}
		}

		if n <= 0 {
			continue
		}

		echoId := int(binary.BigEndian.Uint16(bytes[4:6]))
		echoSeq := int(binary.BigEndian.Uint16(bytes[6:8]))

		my := &MyMsg{}
		err = proto.Unmarshal(bytes[8:n], my)
		if err != nil {
			loggo.Debug("Unmarshal MyMsg error: %s", err)
			continue
		}

		if my.Magic == (int32)(MyMsg_MAGIC) {
			loggo.Debug("processPacket data invalid %s", my.Id)
			continue
		}

		if my.Data == nil {
			loggo.Error("processPacket data nil %s", my.Id)
			continue
		}

		recv <- &Packet{my: my,
			src:    srcaddr.(*net.IPAddr),
			echoId: echoId, echoSeq: echoSeq}
	}
}

type Packet struct {
	my      *MyMsg
	src     *net.IPAddr
	echoId  int
	echoSeq int
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

const (
	FRAME_MAX_SIZE int = 888
	FRAME_MAX_ID   int = 10000
)
