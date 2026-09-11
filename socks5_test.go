package pingtunnel

import (
	"bytes"
	"testing"
)

func TestReadSocks5RequestConnect(t *testing.T) {
	reqBytes := []byte{
		0x05, 0x01, 0x00, 0x03, 0x0b,
		'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'c', 'o', 'm',
		0x01, 0xbb,
	}

	req, err := readSocks5Request(bytes.NewReader(reqBytes))
	if err != nil {
		t.Fatalf("readSocks5Request failed: %v", err)
	}
	if req.Command != socks5CmdConnect {
		t.Fatalf("unexpected command: %d", req.Command)
	}
	if req.Address != "example.com:443" {
		t.Fatalf("unexpected address: %s", req.Address)
	}
}

func TestReadSocks5RequestUDPAssociate(t *testing.T) {
	reqBytes := []byte{
		0x05, 0x03, 0x00, 0x01,
		127, 0, 0, 1,
		0xd4, 0x31, // 54321
	}

	req, err := readSocks5Request(bytes.NewReader(reqBytes))
	if err != nil {
		t.Fatalf("readSocks5Request failed: %v", err)
	}
	if req.Command != socks5CmdUDPAssociate {
		t.Fatalf("unexpected command: %d", req.Command)
	}
	if req.Address != "127.0.0.1:54321" {
		t.Fatalf("unexpected address: %s", req.Address)
	}
}

func TestReadSocks5RequestInvalidVersion(t *testing.T) {
	reqBytes := []byte{0x04, 0x01, 0x00, 0x01, 127, 0, 0, 1, 0x00, 0x35}
	_, err := readSocks5Request(bytes.NewReader(reqBytes))
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestSocks5UDPDatagramRoundTrip(t *testing.T) {
	target := "8.8.8.8:53"
	payload := []byte{0xde, 0xad, 0xbe, 0xef}

	packet, err := buildSocks5UDPDatagram(target, payload)
	if err != nil {
		t.Fatalf("buildSocks5UDPDatagram failed: %v", err)
	}

	parsedTarget, parsedPayload, err := parseSocks5UDPDatagram(packet)
	if err != nil {
		t.Fatalf("parseSocks5UDPDatagram failed: %v", err)
	}
	if parsedTarget != target {
		t.Fatalf("unexpected target: %s", parsedTarget)
	}
	if !bytes.Equal(parsedPayload, payload) {
		t.Fatalf("unexpected payload: %v", parsedPayload)
	}
}

func TestSocks5UDPDatagramRejectFragment(t *testing.T) {
	packet := []byte{
		0x00, 0x00, 0x01, // FRAG != 0
		0x01, 1, 1, 1, 1, 0x00, 0x35,
		0xaa,
	}

	_, _, err := parseSocks5UDPDatagram(packet)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestEncodeAndParseSocks5Address(t *testing.T) {
	testCases := []string{
		"127.0.0.1:8080",
		"192.168.1.1:53",
		"[::1]:1080",
		"google.com:443",
		"example.org:80",
	}

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			encoded, err := encodeSocks5Address(tc)
			if err != nil {
				t.Fatalf("encodeSocks5Address(%q) failed: %v", tc, err)
			}

			parsed, consumed, err := parseSocks5Address(encoded)
			if err != nil {
				t.Fatalf("parseSocks5Address failed: %v", err)
			}
			if consumed != len(encoded) {
				t.Fatalf("consumed %d, expected %d", consumed, len(encoded))
			}
			if parsed != tc {
				t.Fatalf("roundtrip mismatch: got %s, want %s", parsed, tc)
			}
		})
	}
}

func TestWriteSocks5Reply(t *testing.T) {
	var buf bytes.Buffer
	err := writeSocks5Reply(&buf, socks5ReplySucceeded, "127.0.0.1:1080")
	if err != nil {
		t.Fatalf("writeSocks5Reply failed: %v", err)
	}

	replyBytes := buf.Bytes()
	if len(replyBytes) < 4 {
		t.Fatalf("reply too short: %d", len(replyBytes))
	}
	if replyBytes[0] != socks5Version {
		t.Fatalf("unexpected version: %d", replyBytes[0])
	}
	if replyBytes[1] != socks5ReplySucceeded {
		t.Fatalf("unexpected reply code: %d", replyBytes[1])
	}
	if replyBytes[2] != 0x00 {
		t.Fatalf("unexpected reserved byte: %d", replyBytes[2])
	}
}

