package pingtunnel

import (
	"bytes"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"math/rand"
	"testing"
)

// TestSendICMPEchoMarshalEquivalence pins the hand-rolled ICMP echo builder
// in finalizeICMPEcho to byte-identical output against x/net/icmp's
// Message.Marshal for every shape the send path produces: empty, even and
// odd payload lengths (the checksum's trailing-byte branch), and the id/seq
// range the tunnel uses.
func TestSendICMPEchoMarshalEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	lengths := []int{0, 1, 2, 3, 7, 8, 9, 15, 16, 17, 63, 64, 65, 255, 1024, 1025}
	for _, length := range lengths {
		data := make([]byte, length)
		rng.Read(data)

		ids := []int{0, 1, 255, 256, 65535, 70000}
		seqs := []int{0, 1, 65535, 70000}
		for _, id := range ids {
			for _, seq := range seqs {
				for _, sproto := range []int{SEND_PROTO, RECV_PROTO} {
					want, err := (&icmp.Message{
						Type: (ipv4.ICMPType)(sproto),
						Code: 0,
						Body: &icmp.Echo{ID: id, Seq: seq, Data: data},
					}).Marshal(nil)
					if err != nil {
						t.Fatalf("icmp marshal: %v", err)
					}

					got := make([]byte, 8, 8+len(data))
					got = append(got, data...)
					finalizeICMPEcho(got, sproto, id, seq)

					if !bytes.Equal(got, want) {
						t.Fatalf("len %d id %d seq %d sproto %d: got %x want %x",
							length, id, seq, sproto, got[:16], want[:16])
					}
				}
			}
		}
	}
}

// TestEncryptIntoLayout pins the EncryptInto buffer contract used by the
// send path: the 8-byte ICMP header headroom before dst must survive, the
// nonce must sit intact at dst[len(dst)] when the call returns, and the
// envelope must decrypt back to the payload.
func TestEncryptIntoLayout(t *testing.T) {
	payload := []byte("echo payload that must survive the round trip exactly")
	for _, mode := range []EncryptionMode{AES128, AES256, CHACHA20} {
		cfg, err := NewCryptoConfig(mode, "layout-key-12345678")
		if err != nil {
			t.Fatalf("NewCryptoConfig(%v): %v", mode, err)
		}

		buf := make([]byte, 8, 128)
		copy(buf, []byte{0xAA, 0xBB, 0xCC, 0xDD, 0x01, 0x02, 0x03, 0x04})
		headroom := append([]byte(nil), buf[:8]...)

		out, err := cfg.EncryptInto(buf, payload)
		if err != nil {
			t.Fatalf("EncryptInto(%v): %v", mode, err)
		}
		if len(out) < 8+cfg.Cipher.NonceSize()+len(payload) {
			t.Fatalf("%v: envelope too short: %d", mode, len(out))
		}
		if !bytes.Equal(out[:8], headroom) {
			t.Fatalf("%v: headroom clobbered: %x", mode, out[:8])
		}

		plain, err := cfg.Decrypt(out[8:])
		if err != nil {
			t.Fatalf("%v: decrypt: %v", mode, err)
		}
		if !bytes.Equal(plain, payload) {
			t.Fatalf("%v: payload corrupted", mode)
		}
	}
}
