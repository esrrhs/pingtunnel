package pingtunnel

import (
	"bytes"
	"crypto/rand"
	"google.golang.org/protobuf/proto"
	"io"
	"net"
	"testing"
	"time"
)

// The benchmarks here are the performance baseline for regression tracking
// (go test -bench . -run '^$'). The tunnel ones ride the same fake-underlay
// e2e harness as the functional tests: a full client/server pair over the
// in-process ICMP hub against a loopback echo target.

// benchPayload returns n bytes of random data; random (not zeros) keeps the
// compression path honest for pair opts that enable it.
func benchPayload(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// BenchmarkTunnelThroughput measures bulk transfer through a full TCP-mode
// pair as a ping-pong: each op pushes a 64KiB block to the echo target and
// reads it back, so ns/op covers one round trip of 128KiB total traffic.
func BenchmarkTunnelThroughput(b *testing.B) {
	p := startPair(b, basePairOpts())
	conn, err := net.DialTimeout("tcp", p.clientAddr, 10*time.Second)
	if err != nil {
		b.Fatalf("dial tunnel: %v", err)
	}
	defer conn.Close()

	payload := benchPayload(64 * 1024)
	buf := make([]byte, len(payload))
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := conn.Write(payload); err != nil {
			b.Fatalf("write: %v", err)
		}
		if _, err := io.ReadFull(conn, buf); err != nil {
			b.Fatalf("read back: %v", err)
		}
		if !bytes.Equal(buf, payload) {
			b.Fatal("echo payload mismatch")
		}
	}
}

// BenchmarkTunnelEchoRTT measures small-packet latency through the same
// pair: one 64B round trip per op.
func BenchmarkTunnelEchoRTT(b *testing.B) {
	p := startPair(b, basePairOpts())
	conn, err := net.DialTimeout("tcp", p.clientAddr, 10*time.Second)
	if err != nil {
		b.Fatalf("dial tunnel: %v", err)
	}
	defer conn.Close()

	payload := benchPayload(64)
	buf := make([]byte, len(payload))
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := conn.Write(payload); err != nil {
			b.Fatalf("write: %v", err)
		}
		if _, err := io.ReadFull(conn, buf); err != nil {
			b.Fatalf("read back: %v", err)
		}
	}
}

// BenchmarkCrypto covers the wire crypto primitives on a 1KiB frame across
// every usable mode.
func BenchmarkCrypto(b *testing.B) {
	payload := benchPayload(1024)
	for _, mode := range []EncryptionMode{AES128, AES256, CHACHA20} {
		b.Run(mode.String(), func(b *testing.B) {
			cfg, err := NewCryptoConfig(mode, "benchmark-key-12345678")
			if err != nil {
				b.Fatalf("NewCryptoConfig: %v", err)
			}
			encrypted, err := cfg.Encrypt(payload)
			if err != nil {
				b.Fatalf("Encrypt: %v", err)
			}
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := cfg.Encrypt(payload); err != nil {
					b.Fatalf("Encrypt: %v", err)
				}
				if _, err := cfg.Decrypt(encrypted); err != nil {
					b.Fatalf("Decrypt: %v", err)
				}
			}
		})
	}
}

// BenchmarkDecrypt measures the receive-side AEAD open on a 900B envelope
// (typical encrypted frame). The buffer is reused and pre-filled before each
// open so the measurement isolates decrypt cost from harness allocation.
func BenchmarkDecrypt(b *testing.B) {
	payload := benchPayload(900)
	for _, mode := range []EncryptionMode{AES128, AES256, CHACHA20} {
		b.Run(mode.String(), func(b *testing.B) {
			cfg, err := NewCryptoConfig(mode, "benchmark-key-12345678")
			if err != nil {
				b.Fatalf("NewCryptoConfig: %v", err)
			}
			enc, err := cfg.Encrypt(payload)
			if err != nil {
				b.Fatalf("Encrypt: %v", err)
			}
			buf := make([]byte, len(enc))
			b.SetBytes(int64(len(enc)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				copy(buf, enc)
				if _, err := cfg.DecryptInPlace(buf); err != nil {
					b.Fatalf("DecryptInPlace: %v", err)
				}
			}
		})
	}
}

// BenchmarkMyMsgRoundTrip measures the protobuf framing that every tunnel
// packet goes through (marshal + unmarshal of a 1KiB USER_DATA message).
func BenchmarkMyMsgRoundTrip(b *testing.B) {
	m := &MyMsg{
		Id:   "benchmark-client",
		Type: int32(MyMsg_DATA),
		Data: benchPayload(1024),
	}
	b.SetBytes(1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wire, err := proto.Marshal(m)
		if err != nil {
			b.Fatalf("Marshal: %v", err)
		}
		out := &MyMsg{}
		if err := proto.Unmarshal(wire, out); err != nil {
			b.Fatalf("Unmarshal: %v", err)
		}
	}
}

// BenchmarkSendICMPAllocs isolates the per-packet send path: at steady
// state the pooled buffers make it allocation-free end to end (protobuf
// marshal, AEAD seal, ICMP header build).
func BenchmarkSendICMPAllocs(b *testing.B) {
	rec := &fakeICMPConn{}
	crypto, err := NewCryptoConfig(CHACHA20, "bench-key-12345678")
	if err != nil {
		b.Fatalf("NewCryptoConfig: %v", err)
	}
	server := &net.IPAddr{IP: net.IPv4(1, 2, 3, 4)}
	payload := benchPayload(1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sendICMP(1234, i, rec, server, "", "conn-1", (uint32)(MyMsg_DATA), payload,
			SEND_PROTO, RECV_PROTO, 7, 1, 1024, 100, 10, 0, 0, 60, crypto)
	}
}
