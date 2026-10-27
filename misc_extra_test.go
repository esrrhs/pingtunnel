package pingtunnel

import (
	"container/list"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/esrrhs/gohome/network"
)

func TestIcmpDstAddr(t *testing.T) {
	old := icmpDatagram
	defer setICMPDatagram(old)

	ip := &net.IPAddr{IP: net.IPv4(1, 2, 3, 4)}
	setICMPDatagram(true)
	if _, ok := icmpDstAddr(ip).(*net.UDPAddr); !ok {
		t.Error("datagram mode must produce a UDPAddr")
	}
	setICMPDatagram(false)
	if _, ok := icmpDstAddr(ip).(*net.IPAddr); !ok {
		t.Error("raw mode must keep the IPAddr")
	}
}

func TestIcmpSrcToIPAddr(t *testing.T) {
	orig := &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}
	if got := icmpSrcToIPAddr(orig); got != orig {
		t.Error("*net.IPAddr must pass through")
	}
	udp := &net.UDPAddr{IP: net.IPv4(2, 2, 2, 2), Zone: "en0"}
	got := icmpSrcToIPAddr(udp)
	if got.IP.String() != "2.2.2.2" || got.Zone != "en0" {
		t.Errorf("udp src = %v", got)
	}
	fallback := icmpSrcToIPAddr(&net.TCPAddr{})
	if fallback.IP == nil || !fallback.IP.IsUnspecified() {
		t.Errorf("unknown addr type must fall back to zero IP, got %v", fallback)
	}
}

func TestNotifyActivity(t *testing.T) {
	notifyActivity(nil) // nil channel is a no-op

	ch := make(chan struct{}, 1)
	notifyActivity(ch)
	select {
	case <-ch:
	default:
		t.Fatal("notify must deliver on a free channel")
	}
	notifyActivity(ch) // full channel must not block
}

func TestAdaptiveLoopWait(t *testing.T) {
	a := newAdaptiveLoopWait(0, time.Millisecond) // min<=0 snaps to 1ms
	if a.min != time.Millisecond || a.max != time.Millisecond {
		t.Fatalf("bounds = %v %v", a.min, a.max)
	}
	if got := a.miss(); got != time.Millisecond {
		t.Errorf("first miss = %v", got)
	}

	b := newAdaptiveLoopWait(2*time.Millisecond, 5*time.Millisecond)
	if b.max < b.min {
		t.Fatal("max must be clamped to min")
	}
	var total time.Duration
	for i := 0; i < 4; i++ {
		total += b.miss()
	}
	if want := (2 + 4 + 5 + 5) * time.Millisecond; total != want {
		t.Errorf("misses = %v, want %v", total, want)
	}
	b.hit()
	if got := b.miss(); got != 2*time.Millisecond {
		t.Errorf("after hit miss = %v, want 2ms", got)
	}
}

func TestUnderlayShareEmitFrames(t *testing.T) {
	frame := &network.Frame{}

	// Nil receiver: send everything directly.
	var nilShare *underlayShare
	sent := 0
	frames := list.New()
	frames.PushBack(frame)
	frames.PushBack(frame)
	nilShare.emitFrames(frames, func(*network.Frame) int { sent++; return 1 })
	if sent != 2 {
		t.Fatalf("nil share sent %d", sent)
	}

	// Degenerate arguments are no-ops.
	s := newUnderlayShare()
	s.emitFrames(nil, func(*network.Frame) int { sent++; return 1 })
	s.emitFrames(list.New(), func(*network.Frame) int { sent++; return 1 })
	s.emitFrames(frames, nil)
	if sent != 2 {
		t.Fatalf("no-op calls must not send, sent %d", sent)
	}

	// A skipped frame (send returns 0) still consumes quantum budget.
	big := list.New()
	for i := 0; i < 3; i++ {
		big.PushBack(frame)
	}
	n := 0
	s.emitFrames(big, func(*network.Frame) int { n++; return 0 })
	if n != 3 {
		t.Fatalf("all frames must be attempted, got %d", n)
	}
}

func TestBBControlPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("RecvAck with negative size must panic")
		}
	}()
	bb := newBBControl()
	bb.RecvAck(1, -1)
}

func TestBBControlCanSendPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("CanSend with negative size must panic")
		}
	}()
	bb := newBBControl()
	bb.CanSend(1, -1)
}

func TestBBControlUpdateStates(t *testing.T) {
	bb := newBBControl()
	bb.Init()

	// Idle with in-flight data: shrink toward the lost floor each round. The
	// idle branch returns early, below the healthy-interval floor.
	bb.CanSend(1, 100)
	bb.Update()
	if bb.maxfly >= bbMaxflyFloor {
		t.Fatalf("idle update must shrink maxfly below the floor, got %d", bb.maxfly)
	}

	// Growing phase: a healthy init interval regrows the window back to at
	// least the floor.
	bb.RecvAck(1, 2_000_000)
	bb.Update()
	if bb.status != bbStatusInit || bb.maxfly < bbMaxflyFloor {
		t.Fatalf("healthy init interval must grow: status=%d maxfly=%d", bb.status, bb.maxfly)
	}

	// A weak interval moves to proportional mode.
	bb.RecvAck(1, 1000)
	bb.Update()
	if bb.status != bbStatusProp {
		t.Fatalf("status = %d, want prop", bb.status)
	}

	if bb.Info() == "" {
		t.Fatal("Info must not be empty")
	}
}

// TestBBControlPropFloorClamp builds a proportional-mode controller whose
// in-flight bytes equal its window, so the NearlyEqual branch lowers maxfly
// below the floor and the floor clamps it back.
func TestBBControlPropFloorClamp(t *testing.T) {
	bb := newBBControl()
	bb.Init()
	bb.status = bbStatusProp
	bb.maxfly = 100000
	bb.rateflywin.PushBack(1.0)
	bb.flyedwin.PushBack(100)
	bb.CanSend(1, 100000) // flyingdata == maxfly -> NearlyEqual
	bb.RecvAck(1, 50)
	bb.Update()

	if bb.maxfly != bbMaxflyFloor {
		t.Fatalf("maxfly = %d, want floor clamp", bb.maxfly)
	}
}

func TestLoadConfigWrongFieldType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad-type.json")
	if err := os.WriteFile(path, []byte(`{"type":123}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("type field of wrong JSON type must fail")
	}
}
