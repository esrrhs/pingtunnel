package pingtunnel

import (
	"bytes"
	rand "crypto/rand"
	"encoding/binary"
	mathrand "math/rand"
	"net"
	"testing"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"google.golang.org/protobuf/proto"
)

// TestSendICMPEchoMarshalEquivalence pins the hand-rolled ICMP echo builder
// in finalizeICMPEcho to byte-identical output against x/net/icmp's
// Message.Marshal for every shape the send path produces: empty, even and
// odd payload lengths (the checksum's trailing-byte branch), and the id/seq
// range the tunnel uses.
func TestSendICMPEchoMarshalEquivalence(t *testing.T) {
	rng := mathrand.New(mathrand.NewSource(42))
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

// TestWireCompatWithOldVersion proves cross-version interoperability after
// the send-path rework: a packet built exactly the way pre-rework versions
// built it (proto.Marshal -> Seal(nil)+copy envelope -> icmp.Message.Marshal)
// must be decodable by the current receive path, and a packet from the
// current sendICMP must be decodable by the old receive steps. The wire
// format — [8B ICMP echo header][nonce][ciphertext] over the same MyMsg
// protobuf — is unchanged; only allocation strategy differed.
func TestWireCompatWithOldVersion(t *testing.T) {
	payload := []byte("interoperability payload that must survive both directions")

	cfg, err := NewCryptoConfig(AES128, "compat-key-12345678")
	if err != nil {
		t.Fatalf("NewCryptoConfig: %v", err)
	}

	// Direction 1: OLD-STYLE packet -> CURRENT receive path.
	oldMy := &MyMsg{
		Id:     "old-client",
		Type:   (int32)(MyMsg_DATA),
		Data:   payload,
		Rproto: -1,
		Key:    (int32)(7777),
		Magic:  (int32)(MyMsg_MAGIC),
	}
	oldWire, err := proto.Marshal(oldMy) // old: allocating Marshal
	if err != nil {
		t.Fatalf("old marshal: %v", err)
	}
	// Old Encrypt: Seal(nil) then copy into nonce||ciphertext.
	nonce := make([]byte, cfg.Cipher.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("nonce: %v", err)
	}
	ct := cfg.Cipher.Seal(nil, nonce, oldWire, nil)
	oldEnvelope := make([]byte, 0, len(nonce)+len(ct))
	oldEnvelope = append(oldEnvelope, nonce...)
	oldEnvelope = append(oldEnvelope, ct...)
	oldPkt, err := (&icmp.Message{
		Type: (ipv4.ICMPType)(SEND_PROTO),
		Code: 0,
		Body: &icmp.Echo{ID: 4321, Seq: 8765, Data: oldEnvelope},
	}).Marshal(nil)
	if err != nil {
		t.Fatalf("old icmp marshal: %v", err)
	}

	// The current receive steps (recvICMP is unchanged): split the 8-byte
	// header, decrypt the rest, unmarshal MyMsg, check the magic.
	if got := binary.BigEndian.Uint16(oldPkt[4:6]); got != 4321 {
		t.Fatalf("old packet id = %d", got)
	}
	plain, err := cfg.Decrypt(oldPkt[8:])
	if err != nil {
		t.Fatalf("current Decrypt on old-style packet: %v", err)
	}
	newMy := &MyMsg{}
	if err := proto.Unmarshal(plain, newMy); err != nil {
		t.Fatalf("current Unmarshal on old-style packet: %v", err)
	}
	if newMy.Magic != (int32)(MyMsg_MAGIC) || string(newMy.Data) != string(payload) {
		t.Fatalf("old-style packet decoded wrong: %+v", newMy)
	}

	// Direction 2: CURRENT sendICMP packet -> OLD receive steps.
	rec := &fakeICMPConn{}
	server := &net.IPAddr{IP: net.IPv4(1, 2, 3, 4)}
	sendICMP(4321, 8765, rec, server, "", "new-client", (uint32)(MyMsg_DATA), payload,
		SEND_PROTO, RECV_PROTO, 7777, 1, 1024, 100, 10, 0, 0, 60, cfg)
	rec.mu.Lock()
	if len(rec.written) != 1 {
		rec.mu.Unlock()
		t.Fatalf("sendICMP wrote %d packets", len(rec.written))
	}
	newPkt := append([]byte(nil), rec.written[0]...)
	rec.mu.Unlock()

	// Old receive steps, exactly as pre-rework recvICMP did them.
	plain2, err := cfg.Decrypt(newPkt[8:])
	if err != nil {
		t.Fatalf("old Decrypt on current packet: %v", err)
	}
	oldView := &MyMsg{}
	if err := proto.Unmarshal(plain2, oldView); err != nil {
		t.Fatalf("old Unmarshal on current packet: %v", err)
	}
	if oldView.Magic != (int32)(MyMsg_MAGIC) || string(oldView.Data) != string(payload) {
		t.Fatalf("current packet decoded wrong by old steps: %+v", oldView)
	}

	// And the ICMP header the current builder emits must parse via
	// x/net/icmp — the same parser every old version used on the read side.
	msg, err := icmp.ParseMessage(1, newPkt)
	if err != nil {
		t.Fatalf("icmp.ParseMessage on current packet: %v", err)
	}
	echo, ok := msg.Body.(*icmp.Echo)
	if !ok {
		t.Fatalf("current packet body = %T, want *icmp.Echo", msg.Body)
	}
	if echo.ID != 4321 || echo.Seq != 8765 {
		t.Fatalf("echo fields mismatch: id=%d seq=%d", echo.ID, echo.Seq)
	}
	plain3, err := cfg.Decrypt(echo.Data)
	if err != nil {
		t.Fatalf("decrypt via parsed echo body: %v", err)
	}
	check := &MyMsg{}
	if err := proto.Unmarshal(plain3, check); err != nil || string(check.Data) != string(payload) {
		t.Fatalf("payload corrupt through parsed echo body: %v", err)
	}
}
