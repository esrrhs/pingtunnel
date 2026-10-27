package pingtunnel

import (
	"bytes"
	"errors"
	"net"
	"strings"
	"testing"
)

// errReader fails every Read with a fixed error.
type errReader struct{ err error }

func (r errReader) Read(p []byte) (int, error) { return 0, r.err }

func TestReadSocks5Request(t *testing.T) {
	cases := []struct {
		name    string
		input   []byte
		wantCmd byte
		wantAdr string
		wantErr string
	}{
		{
			name:    "ipv4 connect",
			input:   []byte{0x05, 0x01, 0x00, 0x01, 127, 0, 0, 1, 0x1F, 0x90},
			wantCmd: 0x01,
			wantAdr: "127.0.0.1:8080",
		},
		{
			name:    "domain udp associate",
			input:   append(append([]byte{0x05, 0x03, 0x00, 0x03, 9}, []byte("localhost")...), 0x00, 0x35),
			wantCmd: 0x03,
			wantAdr: "localhost:53",
		},
		{
			name:    "ipv6",
			input:   append([]byte{0x05, 0x01, 0x00, 0x04}, append(net.ParseIP("::1").To16(), 0x00, 0x50)...),
			wantCmd: 0x01,
			wantAdr: "[::1]:80",
		},
		{name: "short header", input: []byte{0x05, 0x01}, wantErr: "read socks5 request header"},
		{name: "bad version", input: []byte{0x04, 0x01, 0x00, 0x01, 0, 0, 0, 1, 0, 80}, wantErr: "unsupported socks version"},
		{name: "reserved byte set", input: []byte{0x05, 0x01, 0x01, 0x01, 0, 0, 0, 1, 0, 80}, wantErr: "invalid socks reserved byte"},
		{name: "truncated ipv4", input: []byte{0x05, 0x01, 0x00, 0x01, 127, 0}, wantErr: "read ipv4 address"},
		{name: "truncated ipv6", input: []byte{0x05, 0x01, 0x00, 0x04, 1, 2, 3}, wantErr: "read ipv6 address"},
		{name: "truncated domain length", input: []byte{0x05, 0x01, 0x00, 0x03}, wantErr: "read domain length"},
		{name: "empty domain", input: []byte{0x05, 0x01, 0x00, 0x03, 0x00}, wantErr: "invalid empty domain"},
		{name: "truncated domain body", input: append([]byte{0x05, 0x01, 0x00, 0x03, 0x04}, []byte("ab")...), wantErr: "read domain address"},
		{name: "unsupported atyp", input: []byte{0x05, 0x01, 0x00, 0x09}, wantErr: "unsupported socks5 address type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := readSocks5Request(bytes.NewReader(tc.input))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if req.Command != tc.wantCmd || req.Address != tc.wantAdr {
				t.Fatalf("got %+v, want cmd=%d adr=%q", req, tc.wantCmd, tc.wantAdr)
			}
		})
	}

	_, err := readSocks5Request(errReader{err: errors.New("boom")})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("reader error must propagate, got %v", err)
	}
}

func TestParseSocks5UDPDatagram(t *testing.T) {
	build := buildSocks5UDPDatagram

	good, err := build("127.0.0.1:9999", []byte("payload"))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	addr, payload, err := parseSocks5UDPDatagram(good)
	if err != nil || addr != "127.0.0.1:9999" || string(payload) != "payload" {
		t.Fatalf("roundtrip: %q %q %v", addr, payload, err)
	}

	cases := [][]byte{
		[]byte{0x00, 0x00},
		{0x00, 0x01, 0x00, 0x01, 127, 0, 0, 1, 0, 80}, // reserved high byte set
		{0x00, 0x00, 0x01, 0x01, 127, 0, 0, 1, 0, 80}, // fragmented
		{0x00, 0x00, 0x00, 0x09, 1, 2, 3},             // unsupported atyp
		{0x00, 0x00, 0x00, 0x01, 127, 0},              // truncated ipv4
	}
	for _, pkt := range cases {
		if _, _, err := parseSocks5UDPDatagram(pkt); err == nil {
			t.Fatalf("packet %v must fail to parse", pkt)
		}
	}
}

func TestParseSocks5AddressErrors(t *testing.T) {
	if _, _, err := parseSocks5Address(nil); err == nil {
		t.Fatal("empty payload must fail")
	}
	if _, _, err := parseSocks5Address([]byte{0x01, 1, 2, 3}); err == nil {
		t.Fatal("truncated ipv4 must fail")
	}
	if _, _, err := parseSocks5Address([]byte{0x04, 1, 2, 3}); err == nil {
		t.Fatal("truncated ipv6 must fail")
	}
	if _, _, err := parseSocks5Address([]byte{0x03}); err == nil {
		t.Fatal("truncated domain len must fail")
	}
	if _, _, err := parseSocks5Address([]byte{0x03, 9, 'a'}); err == nil {
		t.Fatal("truncated domain body must fail")
	}
	if _, _, err := parseSocks5Address([]byte{0x03, 0, 'a'}); err == nil {
		t.Fatal("empty domain must fail")
	}
	if _, _, err := parseSocks5Address([]byte{0x07, 1, 2, 3, 4, 5, 6}); err == nil {
		t.Fatal("unsupported atyp must fail")
	}

	addr, n, err := parseSocks5Address([]byte{0x03, 3, 'a', 'b', 'c', 0x1F, 0x90})
	if err != nil || addr != "abc:8080" || n != 7 {
		t.Fatalf("domain parse: %q %d %v", addr, n, err)
	}
}

func TestEncodeSocks5AddressErrors(t *testing.T) {
	if _, err := encodeSocks5Address("no-port"); err == nil {
		t.Fatal("missing port must fail")
	}
	if _, err := encodeSocks5Address("host:notaport"); err == nil {
		t.Fatal("bad port must fail")
	}
	if _, err := encodeSocks5Address("host:99999"); err == nil {
		t.Fatal("out-of-range port must fail")
	}
	if _, err := encodeSocks5Address(":80"); err == nil {
		t.Fatal("empty domain must fail")
	}
	long := strings.Repeat("a", 256) + ":80"
	if _, err := encodeSocks5Address(long); err == nil {
		t.Fatal("over-long domain must fail")
	}

	v6, err := encodeSocks5Address("[2001:db8::1]:443")
	if err != nil || v6[0] != socks5AddrIPv6 {
		t.Fatalf("ipv6 encode: %v %v", v6, err)
	}
	dom, err := encodeSocks5Address("example.com:443")
	if err != nil || dom[0] != socks5AddrDomain {
		t.Fatalf("domain encode: %v %v", dom, err)
	}
}

func TestWriteSocks5ReplyEncoding(t *testing.T) {
	var buf bytes.Buffer
	if err := writeSocks5Reply(&buf, socks5ReplySucceeded, ""); err != nil {
		t.Fatalf("default bind addr: %v", err)
	}
	if buf.Len() == 0 || buf.Bytes()[0] != socks5Version || buf.Bytes()[1] != socks5ReplySucceeded {
		t.Fatalf("reply bytes = %v", buf.Bytes())
	}

	buf.Reset()
	if err := writeSocks5Reply(&buf, socks5ReplyGeneralFailure, "127.0.0.1:1080"); err != nil {
		t.Fatalf("explicit bind addr: %v", err)
	}
	if buf.Bytes()[1] != socks5ReplyGeneralFailure {
		t.Fatalf("reply code = %d", buf.Bytes()[1])
	}

	if err := writeSocks5Reply(&buf, socks5ReplySucceeded, "missing-port"); err == nil {
		t.Fatal("bad bind addr must fail to encode")
	}
}
