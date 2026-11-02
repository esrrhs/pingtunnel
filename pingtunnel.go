package pingtunnel

import (
	"encoding/binary"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/esrrhs/gohome/common"
	"github.com/esrrhs/gohome/loggo"
	"google.golang.org/protobuf/proto"
)

// Per-packet send buffers are recycled: every consumer below (AEAD Seal, the
// checksum build, conn.WriteTo) copies synchronously before sendICMP returns,
// so nothing references a buffer after it goes back to the pool.
// Send-side MyMsg structs are used strictly synchronously inside sendICMP
// (built, marshaled, dropped), so they pool cleanly.
var sendMyMsgPool = sync.Pool{
	New: func() interface{} {
		return new(MyMsg)
	},
}

var sendBufPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 0, 2048)
		return &b
	},
}

func getSendBuf(size int) *[]byte {
	bp := sendBufPool.Get().(*[]byte)
	if cap(*bp) < size {
		*bp = make([]byte, 0, size)
	}
	return bp
}

func putSendBuf(bp *[]byte) {
	*bp = (*bp)[:0]
	sendBufPool.Put(bp)
}

// finalizeICMPEcho writes the 8-byte IPv4 ICMP echo header (type, code,
// checksum, id, seq) at the head of pkt, whose body is already in place, and
// computes the checksum exactly like x/net/icmp: accumulated little-endian
// over the whole message with the checksum field zeroed, then XOR-placed at
// bytes 2-3. The equivalence is pinned by TestSendICMPEchoMarshalEquivalence.
func finalizeICMPEcho(pkt []byte, sproto int, id int, sequence int) {
	pkt[0] = byte(sproto)
	pkt[1] = 0
	pkt[2] = 0
	pkt[3] = 0
	pkt[4] = byte(id >> 8)
	pkt[5] = byte(id)
	pkt[6] = byte(sequence >> 8)
	pkt[7] = byte(sequence)

	csumcv := len(pkt) - 1
	s := uint32(0)
	for i := 0; i < csumcv; i += 2 {
		s += uint32(pkt[i+1])<<8 | uint32(pkt[i])
	}
	if csumcv&1 == 0 {
		s += uint32(pkt[csumcv])
	}
	s = s>>16 + s&0xffff
	s = s + s>>16
	csum := ^uint16(s)
	pkt[2] ^= byte(csum)
	pkt[3] ^= byte(csum >> 8)
}

func sendICMP(id int, sequence int, conn icmpPacketConn, server *net.IPAddr, target string,
	connId string, msgType uint32, data []byte, sproto int, rproto int, key int,
	tcpmode int, tcpmode_buffer_size int, tcpmode_maxwin int, tcpmode_resend_time int, tcpmode_compress int, tcpmode_stat int,
	timeout int, cryptoConfig *CryptoConfig) {

	m := sendMyMsgPool.Get().(*MyMsg)
	*m = MyMsg{
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

	wire := getSendBuf(0)
	wireB, err := proto.MarshalOptions{}.MarshalAppend((*wire)[:0], m)
	sendMyMsgPool.Put(m)
	if err != nil {
		loggo.Error("sendICMP Marshal MyMsg error %s %s", server.String(), err)
		putSendBuf(wire)
		return
	}

	// The packet buffer carries [8B header][nonce][ciphertext] when
	// encrypted, [8B header][plaintext] otherwise; the 8-byte headroom is
	// reserved up front so the envelope is written directly into place.
	pkt := getSendBuf(8 + len(wireB) + 32)
	pktB := (*pkt)[:8]
	if cryptoConfig != nil {
		pktB, err = cryptoConfig.EncryptInto(pktB, wireB)
	} else {
		pktB = append(pktB, wireB...)
	}
	putSendBuf(wire)
	if err != nil {
		loggo.Error("sendICMP Encrypt error %s %s", server.String(), err)
		putSendBuf(pkt)
		return
	}

	finalizeICMPEcho(pktB, sproto, id, sequence)

	conn.WriteTo(pktB, icmpDstAddr(server))
	putSendBuf(pkt)
}

func recvICMP(workResultLock *sync.WaitGroup, exit *atomic.Bool, conn icmpPacketConn, recv chan<- *Packet, cryptoConfig *CryptoConfig) {

	defer common.CrashLog()

	defer (*workResultLock).Done()

	bytes := make([]byte, 10240)
	for !exit.Load() {
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
