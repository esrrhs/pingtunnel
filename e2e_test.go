package pingtunnel

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

// Core forward-proxy scenarios, mirrored from spp's e2e_test.go and adapted to
// pingtunnel's model: one client (fixed target in direct mode, SOCKS5 with
// per-conn targets), one server, ICMP underlay.

func TestE2E_TCP_ForwardEcho(t *testing.T) {
	p := startPair(t, basePairOpts())
	defer p.Close()

	conn, err := waitForPort(p.clientAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial local proxy port: %v", err)
	}
	defer conn.Close()

	echoRoundTrip(t, conn, []byte("hello pingtunnel tcp forward proxy"), 10*time.Second)
}

func TestE2E_TCP_ForwardProxy_LargeData(t *testing.T) {
	opts := basePairOpts()
	opts.tcpGZ = 1024 // zstd-compress frames larger than 1KB
	p := startPair(t, opts)
	defer p.Close()

	conn, err := waitForPort(p.clientAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial local proxy port: %v", err)
	}
	defer conn.Close()

	// 64KB repetitive data: many 888-byte frames plus zstd compression.
	largeMsg := bytes.Repeat([]byte("0123456789abcdefghijklmnopqrstuvwxyzABCDEF"), 1500)
	echoRoundTrip(t, conn, largeMsg, 30*time.Second)

	// Second round trip over the same conn: pattern data does not compress as
	// trivially after warmup and exercises steady-state framing.
	payload := make([]byte, 256*1024)
	fillPattern(payload, 9)
	echoRoundTrip(t, conn, payload, 30*time.Second)
}

func TestE2E_SOCKS5_ForwardProxy(t *testing.T) {
	opts := basePairOpts()
	opts.direct = false
	p := startPair(t, opts)
	defer p.Close()

	conn := socks5Connect(t, p.clientAddr, p.echoAddr, 10*time.Second)
	defer conn.Close()

	echoRoundTrip(t, conn, []byte("hello socks5 tunnel data"), 15*time.Second)

	// A second CONNECT on the same listener keeps serving.
	conn2 := socks5Connect(t, p.clientAddr, p.echoAddr, 10*time.Second)
	defer conn2.Close()
	echoRoundTrip(t, conn2, []byte("second socks5 tunnel data"), 15*time.Second)
}

// TestE2E_AuthFailure: with a wrong key the server drops every packet, the
// tunnel never establishes, and the local proxy conn must fail instead of
// hanging forever (the client gives up connecting after ~5s).
func TestE2E_AuthFailure(t *testing.T) {
	p := startPair(t, basePairOpts())
	defer p.Close()

	// A second client with a key the server does not know.
	badKey := nextE2EKey()
	badAddr := fmt.Sprintf("127.0.0.1:%d", getFreePort(t))
	badClient, err := NewClient(badAddr, "127.0.0.1", p.echoAddr, 60, badKey, "127.0.0.1",
		false, false, 0, nil, "", "", "bb")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := badClient.Run(); err != nil {
		t.Fatalf("bad client Run: %v", err)
	}
	badRunning := true
	defer func() {
		if badRunning {
			badClient.Stop()
		}
	}()

	conn, err := waitForPort(badAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial bad client port: %v", err)
	}
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	msg := []byte("this must never be echoed")
	if _, err := conn.Write(msg); err != nil {
		// Some stacks refuse the write after the client-side failure; fine.
		conn.Close()
		badClient.Stop()
		badRunning = false
		return
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, got); err == nil {
		conn.Close()
		t.Fatalf("wrong-key client echoed data: %q", got)
	}
	conn.Close()
	badClient.Stop()
	badRunning = false

	// The healthy pair must still work: the wrong-key traffic never disturbed it.
	conn2, err := waitForPort(p.clientAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial healthy client port: %v", err)
	}
	defer conn2.Close()
	echoRoundTrip(t, conn2, []byte("healthy after auth failure"), 15*time.Second)
}

// TestE2E_ConcurrentDownloadAndWebBrowse: one bulk connection saturates the
// tunnel while five short "web page" connections must each round-trip.
// TestE2E_ConcurrentDownloadAndWebBrowse: one bulk connection saturates the
// tunnel while five short "web page" connections must each round-trip.
func TestE2E_ConcurrentDownloadAndWebBrowse(t *testing.T) {
	p := startPair(t, basePairOpts())
	defer p.Close()

	stopDownload := make(chan struct{})
	downloadDone := make(chan struct{})
	go func() {
		defer close(downloadDone)
		conn, err := net.Dial("tcp", p.clientAddr)
		if err != nil {
			return
		}
		defer conn.Close()

		chunk := make([]byte, 32*1024)
		fillPattern(chunk, 3)
		go func() {
			sink := make([]byte, 32*1024)
			for {
				if _, err := conn.Read(sink); err != nil {
					return
				}
			}
		}()
		for {
			select {
			case <-stopDownload:
				return
			default:
				if _, err := conn.Write(chunk); err != nil {
					return
				}
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()

	time.Sleep(300 * time.Millisecond)

	for i := 0; i < 5; i++ {
		start := time.Now()
		webConn, exhausted, err := dialResourceRetry(p.clientAddr, 10*time.Second)
		if err != nil {
			if exhausted {
				t.Skipf("local resource exhaustion before web request %d", i)
			}
			t.Fatalf("web request %d connection failed: %v", i, err)
		}

		reqMsg := fmt.Sprintf("GET /page%d HTTP/1.1\r\nHost: example.com\r\n\r\n", i)
		_ = webConn.SetDeadline(time.Now().Add(15 * time.Second))
		if _, err := webConn.Write([]byte(reqMsg)); err != nil {
			webConn.Close()
			t.Fatalf("web request %d write failed: %v", i, err)
		}
		resp := make([]byte, len(reqMsg))
		if _, err := io.ReadFull(webConn, resp); err != nil {
			webConn.Close()
			t.Fatalf("web request %d read response failed: %v", i, err)
		}
		if string(resp) != reqMsg {
			webConn.Close()
			t.Fatalf("web request %d response mismatch", i)
		}
		webConn.Close()

		elapsed := time.Since(start)
		t.Logf("web request %d took %v under bulk load", i, elapsed)
		if elapsed > 15*time.Second {
			t.Errorf("web request %d took too long (%v), pipe may be wedged", i, elapsed)
		}
	}

	close(stopDownload)
	<-downloadDone
	time.Sleep(100 * time.Millisecond)
}
