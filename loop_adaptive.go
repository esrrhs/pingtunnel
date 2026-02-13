package pingtunnel

import "time"

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
