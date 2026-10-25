package pingtunnel

import (
	"container/list"
	"sync"

	"github.com/esrrhs/gohome/network"
)

// underlayShareQuantum is how many payload bytes one conn may write to the
// shared ICMP socket before yielding to the next conn.
//
// The kernel default receive buffer is ~208KB. The close/drain loop used to
// emit a whole congestion window (1MB, then growing) in one tight sendICMP
// loop and then sleep, so one goroutine filled that buffer while the others
// were asleep. Their frames were dropped; if a whole window vanished, the
// flow stopped retransmitting. Active transfers and drains both go through
// this gate so concurrent conns — including one still transferring while
// another is closing — take turns.
const underlayShareQuantum = 8 << 10

// underlayShare is a FIFO handoff around the ICMP socket. A plain mutex lets
// the goroutine that just finished a quantum barge back in before a waiter
// runs, which is the same "one conn dumps its window" failure on a tight loop.
type underlayShare struct {
	mu    sync.Mutex
	queue []chan struct{}
	held  bool
}

func newUnderlayShare() *underlayShare {
	return &underlayShare{}
}

func (s *underlayShare) lock() {
	s.mu.Lock()
	if !s.held {
		s.held = true
		s.mu.Unlock()
		return
	}
	ch := make(chan struct{})
	s.queue = append(s.queue, ch)
	s.mu.Unlock()
	<-ch
}

func (s *underlayShare) unlock() {
	s.mu.Lock()
	if len(s.queue) == 0 {
		s.held = false
		s.mu.Unlock()
		return
	}
	ch := s.queue[0]
	s.queue = s.queue[1:]
	s.mu.Unlock()
	// Handoff: held stays true, the waiter proceeds without re-checking.
	ch <- struct{}{}
}

// emitFrames sends frames the congestion controller already admitted, in
// quanta, releasing the underlay between quanta. send returns the bytes
// just written (0 if the frame was skipped).
func (s *underlayShare) emitFrames(frames *list.List, send func(*network.Frame) int) {
	if frames == nil || frames.Len() == 0 || send == nil {
		return
	}
	if s == nil {
		for e := frames.Front(); e != nil; e = e.Next() {
			send(e.Value.(*network.Frame))
		}
		return
	}
	e := frames.Front()
	for e != nil {
		s.lock()
		func() {
			defer s.unlock()
			budget := underlayShareQuantum
			for e != nil && budget > 0 {
				n := send(e.Value.(*network.Frame))
				if n < 1 {
					n = 1
				}
				budget -= n
				e = e.Next()
			}
		}()
	}
}
