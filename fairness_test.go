package pingtunnel

import (
	"container/list"
	"testing"
	"time"

	"github.com/esrrhs/gohome/network"
)

func fillBB(bb *bbControl, packetSize int) int {
	sent := 0
	for bb.CanSend(0, packetSize) {
		sent += packetSize
		if sent > 64<<20 {
			break
		}
	}
	return sent
}

func TestBBControlRecoversFromLostWindow(t *testing.T) {
	bb := newBBControl()
	bb.Init()

	const packetSize = 1000
	first := fillBB(bb, packetSize)
	if first < 1024*1024 {
		t.Fatalf("initial window = %d, want at least 1MB", first)
	}
	if bb.CanSend(0, packetSize) {
		t.Fatal("window should be closed once maxfly is exceeded")
	}

	// No ACKs: the whole interval was lost. Stock BB leaves CanSend false
	// forever; the flow must be allowed to retransmit, and at a smaller window.
	bb.Update()
	second := fillBB(bb, packetSize)
	if second <= 0 {
		t.Fatal("lost window pinned the flow; CanSend stayed false")
	}
	if second >= first {
		t.Fatalf("lost window did not yield: first %d second %d", first, second)
	}
	if second < bbLostFloor {
		t.Fatalf("shrunk below lost-window floor: %d", second)
	}
}

func TestBBControlGrowsWhenAcked(t *testing.T) {
	bb := newBBControl()
	bb.Init()

	const packetSize = 1000
	sent := fillBB(bb, packetSize)
	bb.RecvAck(0, sent)
	bb.Update()
	if !bb.CanSend(0, packetSize) {
		t.Fatal("CanSend should allow more data after the interval was ACKed")
	}
	// A fully delivered interval grows the window (and never applies the
	// lost-window shrink).
	if bb.maxfly <= bbMaxflyFloor {
		t.Fatalf("maxfly = %d, want growth above the 1MB floor", bb.maxfly)
	}
}

func TestUnderlayShareInterleavesConns(t *testing.T) {
	const (
		conns  = 2
		frames = 40
	)
	share := newUnderlayShare()
	order := make(chan int, conns*frames)
	errc := make(chan error, conns)
	ready := make(chan struct{})
	started := make(chan struct{}, conns)

	for id := 0; id < conns; id++ {
		go func(id int) {
			q := list.New()
			for i := 0; i < frames; i++ {
				q.PushBack(&network.Frame{Id: int32(i)})
			}
			started <- struct{}{}
			<-ready
			share.emitFrames(q, func(f *network.Frame) int {
				order <- id
				// Hold the quantum long enough that the other conn queues
				// behind this handoff instead of losing the startup race.
				time.Sleep(200 * time.Microsecond)
				return 1000
			})
			errc <- nil
		}(id)
	}
	for i := 0; i < conns; i++ {
		<-started
	}
	close(ready)
	for i := 0; i < conns; i++ {
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	}
	close(order)

	var seq []int
	for id := range order {
		seq = append(seq, id)
	}
	if len(seq) != conns*frames {
		t.Fatalf("emitted %d frames, want %d", len(seq), conns*frames)
	}
	// A turn keeps sending while budget > 0, so a 1000-byte frame still goes
	// out when a few hundred bytes of quantum remain. That is one turn, not two.
	perTurn := 0
	for budget := underlayShareQuantum; budget > 0; budget -= 1000 {
		perTurn++
	}
	seen := map[int]bool{}
	window := perTurn * 2
	for _, id := range seq[:window] {
		seen[id] = true
	}
	if len(seen) != conns {
		t.Fatalf("first %d sends came from %v; one conn hogged the underlay", window, seen)
	}
	run, maxRun, cur := 1, 1, seq[0]
	for _, id := range seq[1:] {
		if id == cur {
			run++
			if run > maxRun {
				maxRun = run
			}
		} else {
			cur = id
			run = 1
		}
	}
	if maxRun > perTurn {
		t.Fatalf("longest exclusive run = %d frames, one quantum is %d", maxRun, perTurn)
	}
}
