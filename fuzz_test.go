package pingtunnel

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/esrrhs/gohome/common"
	"github.com/esrrhs/gohome/network"
	"google.golang.org/protobuf/proto"
)

// Fuzz targets for the network-facing parsers. Each target asserts only
// crash-freedom plus lightweight postconditions on success paths; the seed
// corpus runs as part of the normal unit suite, and CI gives each target a
// short dedicated -fuzz window.

// FuzzReadSocks5Request drives the SOCKS5 request parser with arbitrary
// bytes. The parser's contract is to extract whatever target string the
// client sent — including non-printable domains, which downstream dialing
// then rejects — so the postcondition on success is only that an address
// was produced (domain parsing rejects empty domains; IP forms are never
// empty). Format validity beyond that is the dialer's job, not the
// parser's.
func FuzzReadSocks5Request(f *testing.F) {
	f.Add([]byte{0x05, 0x01, 0x00, 0x01, 127, 0, 0, 1, 0x1f, 0x90})
	f.Add([]byte{0x05, 0x03, 0x00, 0x03, 9, 'l', 'o', 'c', 'a', 'l', 'h', 'o', 's', 't', 0x00, 0x50})
	f.Add([]byte{0x05, 0x01, 0x00, 0x04, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 0x01, 0xbb})
	f.Add([]byte{0x04, 0x01, 0x00, 0x01})
	f.Add([]byte{0x05, 0x01, 0x01, 0x01})
	f.Add([]byte{0x05, 0x02})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		req, err := readSocks5Request(bytes.NewReader(data))
		if err != nil {
			return
		}
		if req.Address == "" {
			t.Fatal("parsed request without error but address is empty")
		}
	})
}

// FuzzParseSocks5UDPDatagram drives the SOCKS5 UDP associate datagram
// header parser. On success the payload must be a suffix of the input.
func FuzzParseSocks5UDPDatagram(f *testing.F) {
	f.Add([]byte{0x00, 0x00, 0x00, 0x01, 10, 0, 0, 1, 0x00, 0x35, 'x'})
	f.Add([]byte{0x00, 0x00, 0x00, 0x03, 4, 'a', 'b', 'c', 'd', 0x00, 0x35})
	f.Add([]byte{0x00, 0x00, 0x01, 0x01})
	f.Add([]byte{0x00, 0x00})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		target, payload, err := parseSocks5UDPDatagram(data)
		if err != nil {
			return
		}
		if target == "" {
			t.Fatal("parsed datagram without error but target is empty")
		}
		if len(payload) > len(data) || !bytes.Equal(data[len(data)-len(payload):], payload) {
			t.Fatal("payload is not a suffix of the input datagram")
		}
	})
}

// FuzzMyMsgUnmarshal drives the protobuf wire format every tunnel packet is
// decoded through. Decoding must either fail or produce a message whose
// re-encode is parseable — protobuf round-trip stability.
func FuzzMyMsgUnmarshal(f *testing.F) {
	f.Add([]byte{0x0a, 0x03, 'i', 'd', 0x10, 0x01, 0x22, 0x03, 1, 2, 3})
	f.Add([]byte{0x08, 0xff, 0xff, 0xff, 0xff, 0x0f})
	f.Add([]byte{0xff, 0xff, 0xff})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		m := &MyMsg{}
		if err := proto.Unmarshal(data, m); err != nil {
			return
		}
		wire, err := proto.Marshal(m)
		if err != nil {
			t.Fatalf("valid message failed to re-marshal: %v", err)
		}
		again := &MyMsg{}
		if err := proto.Unmarshal(wire, again); err != nil {
			t.Fatalf("re-marshaled message failed to unmarshal: %v", err)
		}
	})
}

// FuzzCryptoDecrypt drives the AEAD decrypt paths with arbitrary ciphertext
// (wrong nonces, truncated frames, mutated tags): neither the allocating
// Decrypt nor the in-place variant recvICMP actually uses may panic, only
// return an error or plaintext. When both accept an input they must agree on
// the plaintext, which pins DecryptInPlace to Decrypt's semantics.
func FuzzCryptoDecrypt(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0xab}, 64))
	// A real envelope: nonce || ciphertext || tag, so the mutation steps have
	// a well-formed starting point to corrupt.
	for _, mode := range []EncryptionMode{AES128, AES256, CHACHA20} {
		cfg, err := NewCryptoConfig(mode, "fuzz-key-12345678")
		if err != nil {
			f.Fatalf("NewCryptoConfig(%v): %v", mode, err)
		}
		enc, err := cfg.Encrypt([]byte("fuzz seed payload"))
		if err != nil {
			f.Fatalf("Encrypt(%v): %v", mode, err)
		}
		f.Add(enc)
	}

	// Key derivation is deliberately slow, so build the configs once: doing it
	// per input would cap the target at a few execs per second.
	var cfgs []*CryptoConfig
	for _, mode := range []EncryptionMode{AES128, AES256, CHACHA20} {
		cfg, err := NewCryptoConfig(mode, "fuzz-key-12345678")
		if err != nil {
			f.Fatalf("NewCryptoConfig(%v): %v", mode, err)
		}
		cfgs = append(cfgs, cfg)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		for _, cfg := range cfgs {
			want, wantErr := cfg.Decrypt(data)

			buf := append([]byte(nil), data...)
			got, gotErr := cfg.DecryptInPlace(buf)

			if (wantErr == nil) != (gotErr == nil) {
				t.Fatalf("Decrypt err=%v but DecryptInPlace err=%v for %x", wantErr, gotErr, data)
			}
			if wantErr == nil && !bytes.Equal(want, got) {
				t.Fatalf("Decrypt = %x, DecryptInPlace = %x", want, got)
			}
			if gotErr == nil && len(got) > len(buf) {
				t.Fatalf("DecryptInPlace returned %d bytes from a %d byte buffer", len(got), len(buf))
			}
		}
	})
}

// fuzzConn adapts a byte slice to net.Conn for the parsers that only read
// from a connection (the SOCKS5 proxy reply). Writes are discarded.
type fuzzConn struct{ *bytes.Reader }

func (fuzzConn) Write(b []byte) (int, error)        { return len(b), nil }
func (fuzzConn) Close() error                       { return nil }
func (fuzzConn) LocalAddr() net.Addr                { return fuzzAddr{} }
func (fuzzConn) RemoteAddr() net.Addr               { return fuzzAddr{} }
func (fuzzConn) SetDeadline(t time.Time) error      { return nil }
func (fuzzConn) SetReadDeadline(t time.Time) error  { return nil }
func (fuzzConn) SetWriteDeadline(t time.Time) error { return nil }

type fuzzAddr struct{}

func (fuzzAddr) Network() string { return "fuzz" }
func (fuzzAddr) String() string  { return "fuzz" }

// FuzzReadSocks5Reply drives the reply parser that consumes an upstream
// SOCKS5 proxy's response — untrusted bytes from a remote host, unlike the
// client request parser above. Malformed versions, reserved bytes and
// truncated/oversized address blocks must be rejected without panicking.
func FuzzReadSocks5Reply(f *testing.F) {
	f.Add([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0x1f, 0x90})
	f.Add([]byte{0x05, 0x00, 0x00, 0x03, 9, 'l', 'o', 'c', 'a', 'l', 'h', 'o', 's', 't', 0x00, 0x50})
	f.Add([]byte{0x05, 0x00, 0x00, 0x04, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0x01, 0xbb})
	f.Add([]byte{0x05, 0x05, 0x00, 0x01})
	f.Add([]byte{0x05, 0x00, 0x01, 0x01})
	f.Add([]byte{0x05, 0x00, 0x00, 0x03, 0xff})
	f.Add([]byte{0x04, 0x00, 0x00, 0x01})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		_, addr, err := socks5ReadReply(fuzzConn{bytes.NewReader(data)})
		if err != nil {
			return
		}
		if addr == "" {
			t.Fatal("parsed reply without error but bind address is empty")
		}
	})
}

// FuzzFrameMgrRecvFrame drives the tunnel's inner frame codec plus the receive
// state machine with arbitrary protobuf bytes, the way a peer's MyMsg.Data is
// decoded and fed to FrameMgr. Out-of-order, duplicate, negative and
// far-future ids must not panic, hang or corrupt the reassembly buffer.
func FuzzFrameMgrRecvFrame(f *testing.F) {
	seedFrame := func(fr *network.Frame) {
		b, err := proto.Marshal(fr)
		if err != nil {
			f.Fatalf("marshal seed: %v", err)
		}
		f.Add(b)
	}
	userData := func(payload []byte) *network.FrameData {
		return &network.FrameData{Type: int32(network.FrameData_USER_DATA), Data: payload}
	}
	seedFrame(&network.Frame{Type: int32(network.Frame_DATA), Id: 0, Data: userData([]byte("hello"))})
	seedFrame(&network.Frame{Type: int32(network.Frame_DATA), Id: 0, Data: userData(nil)})
	seedFrame(&network.Frame{Type: int32(network.Frame_ACK), Id: 0, Dataid: []int32{0}})
	seedFrame(&network.Frame{Type: int32(network.Frame_ACK), Id: 1, Dataid: []int32{0, 1, 2}})
	seedFrame(&network.Frame{Type: int32(network.Frame_REQ), Id: 0, Dataid: []int32{1, 2, 3}})
	seedFrame(&network.Frame{Type: int32(network.Frame_DATA), Id: -1, Data: userData([]byte("x"))})
	seedFrame(&network.Frame{Type: int32(network.Frame_DATA), Id: 999999, Data: userData([]byte("x"))})
	seedFrame(&network.Frame{Type: int32(network.Frame_DATA), Id: 3, Data: userData(bytes.Repeat([]byte("z"), 4096))})
	seedFrame(&network.Frame{Type: int32(network.Frame_DATA), Id: 4, Data: &network.FrameData{Type: int32(network.FrameData_CLOSE)}})
	seedFrame(&network.Frame{Type: int32(network.Frame_DATA), Id: 5, Data: &network.FrameData{Type: int32(network.FrameData_HB)}})
	seedFrame(&network.Frame{Type: 9, Id: 0})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		fr := &network.Frame{}
		if err := proto.Unmarshal(data, fr); err != nil {
			return
		}
		// processPacket drops DATA frames without a payload submessage before
		// they reach FrameMgr; mirror that guard so the target stays on the
		// production pipeline instead of re-finding that known rejection.
		// ACK/REQ frames legitimately carry only Dataid.
		if fr.Type == int32(network.Frame_DATA) && fr.Data == nil {
			return
		}

		fm := network.NewFrameMgr(888, 1000, 64*1024, 64, 50, 0, 0)
		fm.Connect()
		fm.OnRecvFrame(fr)
		fm.Update()

		if size := fm.GetRecvBufferSize(); size > 0 {
			buf := fm.GetRecvReadLineBuffer()
			if len(buf) > size {
				t.Fatalf("read line buffer %d exceeds recv buffer size %d", len(buf), size)
			}
			fm.SkipRecvBuffer(len(buf))
			if fm.GetRecvBufferSize() < 0 {
				t.Fatal("recv buffer size went negative")
			}
		}
	})
}

// FuzzFrameMgrDecompress feeds arbitrary bytes through the compressed-payload
// path: a peer controls both the payload and the Compress flag, so the zstd
// decoder is a direct remote entry point.
func FuzzFrameMgrDecompress(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0x28, 0xb5, 0x2f, 0xfd, 0x20, 0x00, 0x01, 0x00, 0x00, 'h', 'i'})
	f.Add(bytes.Repeat([]byte{0xff}, 64))
	f.Add(bytes.Repeat([]byte{0x00}, 255))
	if enc := common.CompressDataZstd([]byte("compressed seed payload")); len(enc) > 0 {
		f.Add(enc)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		fr := &network.Frame{
			Type: int32(network.Frame_DATA),
			Id:   0,
			Data: &network.FrameData{
				Type:     int32(network.FrameData_USER_DATA),
				Compress: true,
				Data:     data,
			},
		}

		fm := network.NewFrameMgr(888, 1000, 64*1024, 64, 50, 0, 0)
		fm.Connect()
		fm.OnRecvFrame(fr)
		fm.Update()

		if size := fm.GetRecvBufferSize(); size > 0 {
			fm.SkipRecvBuffer(size)
		}
	})
}
