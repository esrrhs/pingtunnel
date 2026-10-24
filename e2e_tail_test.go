package pingtunnel

import (
	"crypto/sha256"
	"io"
	"testing"
	"time"
)

// Tail-behavior tests for "the download conn suddenly disconnects near the
// end". Both failed before the fix: a local half-close tore the whole conn
// down mid-download, and the inactivity gate counted a slow-reading app as a
// dead conn (its tcp-level clause treated a pure download's never-written
// upload direction as idle).

// TestE2E_Lifecycle_HalfCloseDuringDownload: the app sends its request then
// half-closes its write side (CloseWrite — what many raw-socket downloaders
// do), and keeps reading the response. The tunnel must keep relaying the
// response and close only when the target finishes, NOT tear the conn down on
// the half-close EOF.
func TestE2E_Lifecycle_HalfCloseDuringDownload(t *testing.T) {
	total := int64(4) << 20 // 4 MiB response, sent slowly so the half-close lands mid-download
	targetAddr, stopTarget := startStreamVerifyServer(t, int64(2)<<20)
	defer stopTarget()

	opts := basePairOpts()
	opts.direct = false
	opts.timeout = 15
	p := startPair(t, opts)
	defer p.Close()

	conn := socks5Connect(t, p.clientAddr, targetAddr, 10*time.Second)
	defer conn.Close()

	hdr := putStreamHeader('D', total)
	if _, err := conn.Write(hdr[:]); err != nil {
		t.Fatalf("write header: %v", err)
	}

	// Half-close the request side while the response is streaming.
	if cw, ok := conn.(interface{ CloseWrite() error }); ok {
		if err := cw.CloseWrite(); err != nil {
			t.Fatalf("CloseWrite: %v", err)
		}
	}

	// Read the whole response with the slow-reader pacing.
	gotHash := sha256.New()
	wantHash := sha256.New()
	expectedChunk := make([]byte, 64*1024)
	for i := int64(0); i < total/(64*1024); i++ {
		fillPattern(expectedChunk, byte(i))
		_, _ = wantHash.Write(expectedChunk)
	}

	received, err := slowReaderHash(conn, total, int64(2)<<20, gotHash, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("download broken after half-close at %d/%d bytes: %v", received, total, err)
	}
	var gotSum, wantSum [32]byte
	copy(gotSum[:], gotHash.Sum(nil))
	copy(wantSum[:], wantHash.Sum(nil))
	if gotSum != wantSum {
		t.Fatalf("post-half-close download sha256 mismatch")
	}
	t.Logf("download survived half-close (%d bytes)", received)
}

// TestE2E_Lifecycle_ReadStallAtDownloadTail: the app stops reading for a while right
// before the tail arrives (disk flush, UI hiccup). The tail must still be
// delivered afterwards — no inactivity teardown and no lost tail bytes.
func TestE2E_Lifecycle_ReadStallAtDownloadTail(t *testing.T) {
	const total = int64(8) << 20
	targetAddr, stopTarget := startStreamVerifyServer(t, 0)
	defer stopTarget()

	opts := basePairOpts()
	opts.direct = false
	opts.timeout = 5 // aggressive inactivity window; a 5s stall must not kill the conn
	opts.tcpMW = 500 // small window: force the server into backpressure pacing
	p := startPair(t, opts)
	defer p.Close()

	conn := socks5Connect(t, p.clientAddr, targetAddr, 10*time.Second)
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))

	hdr := putStreamHeader('D', total)
	if _, err := conn.Write(hdr[:]); err != nil {
		t.Fatalf("write header: %v", err)
	}

	wantHash := sha256.New()
	expectedChunk := make([]byte, 64*1024)
	for i := int64(0); i < total/(64*1024); i++ {
		fillPattern(expectedChunk, byte(i))
		_, _ = wantHash.Write(expectedChunk)
	}

	buf := make([]byte, 64*1024)
	gotHash := sha256.New()
	var received int64
	// Read 6 MiB, then stall 6s (> the 5s inactivity window), then finish.
	for received < total-int64(2)<<20 {
		n, err := conn.Read(buf)
		if n > 0 {
			_, _ = gotHash.Write(buf[:n])
			received += int64(n)
		}
		if err != nil {
			t.Fatalf("download died at %d/%d before the stall: %v", received, total, err)
		}
	}
	t.Logf("stalling read at %d/%d for 6s (inactivity window is 5s)", received, total)
	time.Sleep(6 * time.Second)
	for received < total {
		n, err := conn.Read(buf)
		if n > 0 {
			_, _ = gotHash.Write(buf[:n])
			received += int64(n)
		}
		if err != nil {
			if err == io.EOF && received == total {
				break
			}
			t.Fatalf("download died during/after the tail stall at %d/%d: %v", received, total, err)
		}
	}
	var gotSum, wantSum [32]byte
	copy(gotSum[:], gotHash.Sum(nil))
	copy(wantSum[:], wantHash.Sum(nil))
	if gotSum != wantSum {
		t.Fatalf("post-stall download sha256 mismatch")
	}
	t.Logf("download survived the tail stall (%d bytes)", received)
}
