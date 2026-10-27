package pingtunnel

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// startFakeProxy accepts connections and hands each to handler in its own
// goroutine. The listener is closed on test cleanup.
func startFakeProxy(t *testing.T, handler func(conn net.Conn)) *ForwardConfig {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handler(conn)
		}
	}()
	return &ForwardConfig{Scheme: "socks5", Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port}
}

func dialProxyAddress(cfg *ForwardConfig) string { return cfg.Address() }

// TestDialThroughProxyErrors covers the failure branches around the proxy
// dial and the per-scheme handshakes.
func TestDialThroughProxyErrors(t *testing.T) {
	t.Run("dial refused", func(t *testing.T) {
		cfg := &ForwardConfig{Scheme: "socks5", Host: "127.0.0.1", Port: 1}
		if _, err := DialThroughProxy(cfg, "1.2.3.4:80", 2*time.Second); err == nil {
			t.Fatal("dial to refused port must fail")
		}
	})

	t.Run("unsupported scheme", func(t *testing.T) {
		cfg := startFakeProxy(t, func(conn net.Conn) { conn.Close() })
		cfg.Scheme = "ftp"
		_, err := DialThroughProxy(cfg, "1.2.3.4:80", 2*time.Second)
		if err == nil || !strings.Contains(err.Error(), "unsupported proxy scheme") {
			t.Fatalf("err = %v", err)
		}
	})

	cases := []struct {
		name    string
		handler func(conn net.Conn)
		scheme  string
		wantErr string
	}{
		{
			name: "socks5 requires auth",
			handler: func(conn net.Conn) {
				_, _ = conn.Write([]byte{0x05, 0xFF})
				conn.Close()
			},
			scheme:  "socks5",
			wantErr: "requires authentication",
		},
		{
			name: "socks5 bad version",
			handler: func(conn net.Conn) {
				_, _ = conn.Write([]byte{0x04, 0x00})
				conn.Close()
			},
			scheme:  "socks5",
			wantErr: "unexpected SOCKS version",
		},
		{
			name: "socks5 connect refused code",
			handler: func(conn net.Conn) {
				buf := make([]byte, 32)
				_, _ = conn.Read(buf) // greeting
				_, _ = conn.Write([]byte{0x05, 0x00})
				_, _ = conn.Read(buf) // request
				_, _ = conn.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
				conn.Close()
			},
			scheme:  "socks5",
			wantErr: "SOCKS5 connect failed with code",
		},
		{
			name: "socks5 immediate close",
			handler: func(conn net.Conn) {
				conn.Close()
			},
			scheme:  "socks5",
			wantErr: "SOCKS5 handshake failed",
		},
		{
			name: "http non-200",
			handler: func(conn net.Conn) {
				buf := make([]byte, 512)
				_, _ = conn.Read(buf)
				_, _ = conn.Write([]byte("HTTP/1.1 407 Denied\r\n\r\n"))
				conn.Close()
			},
			scheme:  "http",
			wantErr: "HTTP CONNECT failed with status",
		},
		{
			name: "http garbage status",
			handler: func(conn net.Conn) {
				buf := make([]byte, 512)
				_, _ = conn.Read(buf)
				_, _ = conn.Write([]byte("not a status line\n\n"))
				conn.Close()
			},
			scheme:  "http",
			wantErr: "invalid HTTP response",
		},
		{
			name: "http immediate close",
			handler: func(conn net.Conn) {
				conn.Close()
			},
			scheme:  "http",
			wantErr: "HTTP CONNECT handshake failed",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cfg := startFakeProxy(t, tc.handler)
			cfg.Scheme = tc.scheme
			_, err := DialThroughProxy(cfg, "93.184.216.34:80", 5*time.Second)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestDialThroughProxyHappy covers both handshakes end-to-end and confirms
// data flows to the proxied target afterwards.
func TestDialThroughProxyHappy(t *testing.T) {
	t.Run("socks5", func(t *testing.T) {
		cfg := startFakeProxy(t, func(conn net.Conn) {
			buf := make([]byte, 32)
			if _, err := conn.Read(buf); err != nil {
				return
			}
			if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
				return
			}
			if _, err := conn.Read(buf); err != nil {
				return
			}
			_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 0})
			io.Copy(io.Discard, conn)
		})
		conn, err := DialThroughProxy(cfg, "example.com:80", 5*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		if _, err := conn.Write([]byte("hello")); err != nil {
			t.Fatalf("post-handshake write: %v", err)
		}
	})

	t.Run("http", func(t *testing.T) {
		cfg := startFakeProxy(t, func(conn net.Conn) {
			buf := make([]byte, 512)
			if _, err := conn.Read(buf); err != nil {
				return
			}
			_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nX-Ignored: 1\r\n\r\n"))
			io.Copy(io.Discard, conn)
		})
		cfg.Scheme = "http"
		conn, err := DialThroughProxy(cfg, "example.com:80", 5*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		if _, err := conn.Write([]byte("hello")); err != nil {
			t.Fatalf("post-handshake write: %v", err)
		}
	})
}

// TestDialUDPThroughProxy covers UDP ASSOCIATE success and failure branches.
func TestDialUDPThroughProxy(t *testing.T) {
	if _, err := DialUDPThroughProxy(nil, time.Second); err == nil {
		t.Fatal("nil config must fail")
	}
	_, err := DialUDPThroughProxy(&ForwardConfig{Scheme: "http", Host: "127.0.0.1", Port: 1}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "unsupported proxy scheme for UDP") {
		t.Fatalf("http scheme err = %v", err)
	}
	_, err = DialUDPThroughProxy(&ForwardConfig{Scheme: "socks5", Host: "127.0.0.1", Port: 1}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "failed to connect to proxy") {
		t.Fatalf("refused dial err = %v", err)
	}

	success := func(conn net.Conn, bnd []byte) {
		buf := make([]byte, 512)
		_, _ = conn.Read(buf) // greeting
		_, _ = conn.Write([]byte{0x05, 0x00})
		_, _ = conn.Read(buf) // udp associate request
		_, _ = conn.Write(bnd)
		io.Copy(io.Discard, conn)
	}

	t.Run("success", func(t *testing.T) {
		cfg := startFakeProxy(t, func(conn net.Conn) {
			success(conn, []byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0x30, 0x39})
		})
		assoc, err := DialUDPThroughProxy(cfg, 5*time.Second)
		if err != nil {
			t.Fatalf("associate: %v", err)
		}
		if assoc.RelayAddr == nil || assoc.RelayAddr.Port != 12345 {
			t.Fatalf("relay addr = %v", assoc.RelayAddr)
		}
		if assoc.UDPConn == nil || assoc.ControlConn == nil {
			t.Fatalf("association conns missing")
		}
		assoc.ControlConn.Close()
		assoc.UDPConn.Close()
	})

	t.Run("failure reply code", func(t *testing.T) {
		cfg := startFakeProxy(t, func(conn net.Conn) {
			success(conn, []byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		})
		_, err := DialUDPThroughProxy(cfg, 5*time.Second)
		if err == nil || !strings.Contains(err.Error(), "failed with code") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("bad reply version", func(t *testing.T) {
		cfg := startFakeProxy(t, func(conn net.Conn) {
			success(conn, []byte{0x04, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		})
		_, err := DialUDPThroughProxy(cfg, 5*time.Second)
		if err == nil || !strings.Contains(err.Error(), "unexpected SOCKS version in reply") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("auth required", func(t *testing.T) {
		cfg := startFakeProxy(t, func(conn net.Conn) {
			_, _ = conn.Write([]byte{0x05, 0xFF})
			conn.Close()
		})
		_, err := DialUDPThroughProxy(cfg, 5*time.Second)
		if err == nil || !strings.Contains(err.Error(), "SOCKS5 negotiation failed") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("reply truncated", func(t *testing.T) {
		cfg := startFakeProxy(t, func(conn net.Conn) {
			buf := make([]byte, 512)
			_, _ = conn.Read(buf)
			_, _ = conn.Write([]byte{0x05, 0x00})
			_, _ = conn.Read(buf)
			_, _ = conn.Write([]byte{0x05, 0x00})
			conn.Close()
		})
		_, err := DialUDPThroughProxy(cfg, 5*time.Second)
		if err == nil || !strings.Contains(err.Error(), "failed to read UDP ASSOCIATE reply") {
			t.Fatalf("err = %v", err)
		}
	})
}

// TestSocks5HandshakeUnit drives socks5Handshake over net.Pipe with a
// scripted peer for each error branch.
func TestSocks5HandshakeUnit(t *testing.T) {
	cases := []struct {
		name    string
		target  string
		peer    func(conn net.Conn)
		wantErr string
	}{
		{name: "target without port", target: "no-port", wantErr: "invalid target address"},
		{name: "target with bad port", target: "host:abc", wantErr: "invalid target port"},
		{
			name:   "reply code failure",
			target: "h.com:80",
			peer: func(conn net.Conn) {
				readN(conn, 3)
				conn.Write([]byte{0x05, 0x00})
				readN(conn, 12)
				conn.Write([]byte{0x05, 0x02, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
			},
			wantErr: "connect failed with code",
		},
		{
			name:   "reply bad version",
			target: "h.com:80",
			peer: func(conn net.Conn) {
				readN(conn, 3)
				conn.Write([]byte{0x05, 0x00})
				readN(conn, 12)
				conn.Write([]byte{0x06, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
			},
			wantErr: "unexpected SOCKS version in reply",
		},
		{
			name:   "reply unknown atyp",
			target: "h.com:80",
			peer: func(conn net.Conn) {
				readN(conn, 3)
				conn.Write([]byte{0x05, 0x00})
				readN(conn, 12)
				conn.Write([]byte{0x05, 0x00, 0x00, 0x07})
			},
			wantErr: "unexpected address type",
		},
		{
			name:   "close before greeting reply",
			target: "h.com:80",
			peer: func(conn net.Conn) {
				readN(conn, 3)
				conn.Close()
			},
			wantErr: "failed to read greeting response",
		},
		{
			name:   "close before connect reply",
			target: "h.com:80",
			peer: func(conn net.Conn) {
				readN(conn, 3)
				conn.Write([]byte{0x05, 0x00})
				readN(conn, 12)
				conn.Close()
			},
			wantErr: "failed to read connect response",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			if tc.peer != nil {
				go tc.peer(server)
			}
			err := socks5Handshake(client, tc.target)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}

	t.Run("happy ipv4 reply", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			readN(server, 3)
			server.Write([]byte{0x05, 0x00})
			readN(server, 12)
			server.Write([]byte{0x05, 0x00, 0x00, 0x01, 10, 0, 0, 1, 0x1F, 0x90})
		}()
		if err := socks5Handshake(client, "h.com:80"); err != nil {
			t.Fatalf("handshake: %v", err)
		}
	})

	t.Run("happy domain reply", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			readN(server, 3)
			server.Write([]byte{0x05, 0x00})
			readN(server, 12)
			server.Write([]byte{0x05, 0x00, 0x00, 0x03, 4, 'a', 'b', 'c', 'd', 0, 80})
		}()
		if err := socks5Handshake(client, "h.com:80"); err != nil {
			t.Fatalf("handshake: %v", err)
		}
	})

	t.Run("happy ipv6 reply", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			readN(server, 3)
			server.Write([]byte{0x05, 0x00})
			readN(server, 12)
			server.Write(append([]byte{0x05, 0x00, 0x00, 0x04}, append(net.ParseIP("::1").To16(), 0, 80)...))
		}()
		if err := socks5Handshake(client, "h.com:80"); err != nil {
			t.Fatalf("handshake: %v", err)
		}
	})

	t.Run("method 0xFF", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			readN(server, 3)
			server.Write([]byte{0x05, 0xFF})
		}()
		err := socks5Handshake(client, "h.com:80")
		if err == nil || !strings.Contains(err.Error(), "requires authentication") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("bad method", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			readN(server, 3)
			server.Write([]byte{0x05, 0x02})
		}()
		err := socks5Handshake(client, "h.com:80")
		if err == nil || !strings.Contains(err.Error(), "unexpected SOCKS5 auth method") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("bad greeting version", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			readN(server, 3)
			server.Write([]byte{0x04, 0x00})
		}()
		err := socks5Handshake(client, "h.com:80")
		if err == nil || !strings.Contains(err.Error(), "unexpected SOCKS version") {
			t.Fatalf("err = %v", err)
		}
	})
}

// readN consumes exactly n bytes, buffering any extras for later reads on
// the same conn (net.Pipe delivers synchronously, so small overshoot is
// impossible here).
func readN(conn net.Conn, n int) {
	buf := make([]byte, n)
	_, _ = io.ReadFull(conn, buf)
}

// TestSocks5NegotiateNoAuth covers the greeting negotiation branches.
func TestSocks5NegotiateNoAuth(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			readN(server, 3)
			server.Write([]byte{0x05, 0x00})
		}()
		if err := socks5NegotiateNoAuth(client); err != nil {
			t.Fatalf("negotiate: %v", err)
		}
	})
	t.Run("bad version", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			readN(server, 3)
			server.Write([]byte{0x04, 0x00})
		}()
		if err := socks5NegotiateNoAuth(client); err == nil {
			t.Fatal("bad version must fail")
		}
	})
	t.Run("auth required", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			readN(server, 3)
			server.Write([]byte{0x05, 0xFF})
		}()
		err := socks5NegotiateNoAuth(client)
		if err == nil || !strings.Contains(err.Error(), "requires authentication") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("bad method", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			readN(server, 3)
			server.Write([]byte{0x05, 0x02})
		}()
		if err := socks5NegotiateNoAuth(client); err == nil {
			t.Fatal("bad method must fail")
		}
	})
	t.Run("eof", func(t *testing.T) {
		client, server := net.Pipe()
		client.Close()
		defer server.Close()
		if err := socks5NegotiateNoAuth(server); err == nil {
			t.Fatal("eof must fail")
		}
	})
}

// TestSocks5ReadReply covers the reply-parsing branches.
func TestSocks5ReadReply(t *testing.T) {
	t.Run("ok ipv4", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			server.Write([]byte{0x05, 0x00, 0x00, 0x01, 1, 2, 3, 4, 0, 80})
		}()
		rep, addr, err := socks5ReadReply(client)
		if err != nil || rep != 0 || addr != "1.2.3.4:80" {
			t.Fatalf("rep=%d addr=%q err=%v", rep, addr, err)
		}
	})
	t.Run("bad version", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			server.Write([]byte{0x06, 0x00, 0x00, 0x01, 1, 2, 3, 4, 0, 80})
		}()
		if _, _, err := socks5ReadReply(client); err == nil {
			t.Fatal("bad version must fail")
		}
	})
	t.Run("reserved byte", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			server.Write([]byte{0x05, 0x00, 0x01, 0x01, 1, 2, 3, 4, 0, 80})
		}()
		if _, _, err := socks5ReadReply(client); err == nil {
			t.Fatal("reserved byte must fail")
		}
	})
	t.Run("truncated addr", func(t *testing.T) {
		client, server := net.Pipe()
		client.Close()
		defer server.Close()
		if _, _, err := socks5ReadReply(server); err == nil {
			t.Fatal("eof must fail")
		}
	})
}

// TestHTTPConnectHandshakeUnit covers the status-line and header branches.
func TestHTTPConnectHandshakeUnit(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			readN(server, 45)
			server.Write([]byte("HTTP/1.0 200 Connection established\r\n\r\n"))
		}()
		if err := httpConnectHandshake(client, "h.com:80"); err != nil {
			t.Fatalf("handshake: %v", err)
		}
	})
	t.Run("non-200", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			readN(server, 45)
			server.Write([]byte("HTTP/1.1 403 Forbidden\r\n\r\n"))
		}()
		err := httpConnectHandshake(client, "h.com:80")
		if err == nil || !strings.Contains(err.Error(), "403") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("garbage", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			readN(server, 45)
			server.Write([]byte("garbage line\n\r\n"))
		}()
		if err := httpConnectHandshake(client, "h.com:80"); err == nil {
			t.Fatal("garbage status must fail")
		}
	})
	t.Run("eof on status", func(t *testing.T) {
		client, server := net.Pipe()
		client.Close()
		defer server.Close()
		if err := httpConnectHandshake(server, "h.com:80"); err == nil {
			t.Fatal("eof must fail")
		}
	})
	t.Run("eof on headers", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			readN(server, 45)
			server.Write([]byte("HTTP/1.1 200 OK\r\n"))
			server.Close()
		}()
		if err := httpConnectHandshake(client, "h.com:80"); err == nil {
			t.Fatal("eof on headers must fail")
		}
	})
}

// TestSocks5SendCommandError covers the encode-failure branch.
func TestSocks5SendCommandError(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	if err := socks5SendCommand(client, socks5CmdConnect, "no-port"); err == nil {
		t.Fatal("bad target must fail")
	}
}

// TestFakeProxyDialAddressSanity keeps the helper honest.
func TestFakeProxyDialAddressSanity(t *testing.T) {
	cfg := startFakeProxy(t, func(conn net.Conn) { conn.Close() })
	if got := dialProxyAddress(cfg); !strings.HasPrefix(got, "127.0.0.1:") {
		t.Fatalf("address = %q", got)
	}
}
