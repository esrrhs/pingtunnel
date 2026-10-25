package pingtunnel

import (
	"encoding/binary"
	"net"
	"sync"
	"time"

	"github.com/esrrhs/gohome/common"
	"github.com/esrrhs/gohome/loggo"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"google.golang.org/protobuf/proto"
)

func sendICMP(id int, sequence int, conn icmpPacketConn, server *net.IPAddr, target string,
	connId string, msgType uint32, data []byte, sproto int, rproto int, key int,
	tcpmode int, tcpmode_buffer_size int, tcpmode_maxwin int, tcpmode_resend_time int, tcpmode_compress int, tcpmode_stat int,
	timeout int, cryptoConfig *CryptoConfig) {

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
		TcpmodeCompress:     (int32)(tcpmode_compress),
		TcpmodeStat:         (int32)(tcpmode_stat),
		Timeout:             (int32)(timeout),
		Magic:               (int32)(MyMsg_MAGIC),
	}

	mb, err := proto.Marshal(m)
	if err != nil {
		loggo.Error("sendICMP Marshal MyMsg error %s %s", server.String(), err)
		return
	}

	// Encrypt the marshaled data if encryption is enabled
	if cryptoConfig != nil {
		mb, err = cryptoConfig.Encrypt(mb)
		if err != nil {
			loggo.Error("sendICMP Encrypt error %s %s", server.String(), err)
			return
		}
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

	conn.WriteTo(bytes, icmpDstAddr(server))
}

func recvICMP(workResultLock *sync.WaitGroup, exit *bool, conn icmpPacketConn, recv chan<- *Packet, cryptoConfig *CryptoConfig) {

	defer common.CrashLog()

	(*workResultLock).Add(1)
	defer (*workResultLock).Done()

	bytes := make([]byte, 10240)
	for !*exit {
		conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
		n, srcaddr, err := conn.ReadFrom(bytes)

		if err != nil {
			nerr, ok := err.(net.Error)
			if !ok || !nerr.Timeout() {
				loggo.Info("Error read icmp message %s", err)
				continue
			}
		}

		if n <= 0 {
			continue
		}

		icmpType := int(bytes[0])
		echoId := int(binary.BigEndian.Uint16(bytes[4:6]))
		echoSeq := int(binary.BigEndian.Uint16(bytes[6:8]))

		// Extract the payload data
		payloadData := bytes[8:n]

		// Decrypt the data if encryption is enabled
		if cryptoConfig != nil {
			payloadData, err = cryptoConfig.Decrypt(payloadData)
			if err != nil {
				loggo.Debug("recvICMP Decrypt error: %s", err)
				continue
			}
		}

		my := &MyMsg{}
		err = proto.Unmarshal(payloadData, my)
		if err != nil {
			loggo.Debug("Unmarshal MyMsg error: %s", err)
			continue
		}

		if my.Magic != (int32)(MyMsg_MAGIC) {
			loggo.Debug("processPacket data invalid %s", my.Id)
			continue
		}

		recv <- &Packet{my: my,
			src:    icmpSrcToIPAddr(srcaddr),
			echoId: echoId, echoSeq: echoSeq,
			icmpType: icmpType}
	}
}

type Packet struct {
	my       *MyMsg
	src      *net.IPAddr
	echoId   int
	echoSeq  int
	icmpType int // ICMP type byte of the received datagram
}

const (
	FRAME_MAX_SIZE         int = 888
	FRAME_MAX_ID           int = 1000000
	PACKET_QUEUE_CAP       int = 10000
	DEFAULT_TCP_WIN        int = 20000
	DEFAULT_TCP_BUF        int = 1024 * 1024
	DEFAULT_TCP_RESEND     int = 400
	DEFAULT_SERVER_THREADS int = 64
	DEFAULT_SERVER_BUFFER  int = 1000
)

// icmpPacketConn is the underlay the client and server exchange MyMsg packets
// over. *icmp.PacketConn implements it; tests may substitute an in-memory
// transport so the full tunnel runs unprivileged.
type icmpPacketConn interface {
	WriteTo(b []byte, dst net.Addr) (int, error)
	ReadFrom(b []byte) (int, net.Addr, error)
	SetReadDeadline(t time.Time) error
	Close() error
}

// icmpListen obtains the ICMP underlay conn. Package-level so tests can swap
// it for a fake; production always uses listenICMP.
var icmpListen = func(addr string) (icmpPacketConn, error) { return listenICMP(addr) }
