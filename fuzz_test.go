package pingtunnel

import (
	"bytes"
	"google.golang.org/protobuf/proto"
	"testing"
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

// FuzzCryptoDecrypt drives the AEAD decrypt path with arbitrary ciphertext
// (wrong nonces, truncated frames, mutated tags): it must never panic, only
// return an error or plaintext.
func FuzzCryptoDecrypt(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0xab}, 64))

	f.Fuzz(func(t *testing.T, data []byte) {
		for _, mode := range []EncryptionMode{AES128, AES256, CHACHA20} {
			cfg, err := NewCryptoConfig(mode, "fuzz-key-12345678")
			if err != nil {
				t.Fatalf("NewCryptoConfig(%v): %v", mode, err)
			}
			if _, err := cfg.Decrypt(data); err != nil {
				continue
			}
		}
	})
}
