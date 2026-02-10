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
