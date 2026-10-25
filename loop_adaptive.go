package pingtunnel

import "time"

// closePeerFlushGrace is how long a close loop keeps pumping after its own
// close marker is acknowledged while the peer has only half-closed.
//
// The peer emits its CLOSE only after it observes ours. A tight loop used to
// leave on the same iteration the ACK of our marker arrived, so that CLOSE
// was never acknowledged and the peer held the conn — and a maxconn slot —
// until the 60s hard timeout. The grace is idle pumping (congestion control
// still gates anything left to send); a peer that never fully closes still
// ends the conn when it expires.
const closePeerFlushGrace = time.Second

// adaptiveLoopWait grows sleep duration on idle loops and resets on activity.
type adaptiveLoopWait struct {
	min time.Duration
	max time.Duration
	cur time.Duration
}

func newAdaptiveLoopWait(min, max time.Duration) adaptiveLoopWait {
	if min <= 0 {
		min = time.Millisecond
	}
	if max < min {
		max = min
	}
	return adaptiveLoopWait{
		min: min,
		max: max,
		cur: min,
	}
}

func (a *adaptiveLoopWait) hit() {
	a.cur = a.min
}

func (a *adaptiveLoopWait) miss() time.Duration {
	d := a.cur
	next := a.cur * 2
	if next > a.max {
		next = a.max
	}
	a.cur = next
	return d
}
