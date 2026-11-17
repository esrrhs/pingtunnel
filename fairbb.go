package pingtunnel

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/esrrhs/gohome/common"
	"github.com/esrrhs/gohome/list"
)

// bbControl is the bandwidth-balancing controller used on TCP conns.
//
// It follows gohome's loss-compensated BBCongestion (sliding in-flight window,
// token-bucket pacing, RTT queue-delay congestion detection, and random-loss
// rate compensation) with one additional rule for raw ICMP: a wholly lost
// interval must release the in-flight counter and shrink down toward
// bbLostFloor so a dead/congested ICMP flow yields and still retransmits.
const (
	bbStatusInit = 0
	bbStatusProp = 1

	bbWin         = 5
	bbMaxflyGrow  = 2.1
	bbMaxflyCmp   = float64(1.5)
	bbMaxflyFloor = 1024 * 1024
	// bbLostFloor is how small a flow that is getting nothing through may
	// probe. It stays under the healthy-interval floor so a loser yields
	// instead of immediately refilling the socket.
	bbLostFloor = 64 * 1024
)

var bbPropSeq = []float64{1, 1, 1.5, 1}
var bbPacedPropSeq = []float64{1.20, 1.05, 1.15, 1.05}

type bbControl struct {
	status        int
	maxfly        int
	flyeddata     int
	lastflyeddata int
	flyingdata    int
	totalsent     int
	resendsSent   int
	rateflywin    *list.Rlistgo
	flyedwin      *list.Rlistgo
	propindex     int
	last          time.Time
	lastratewin   float64
	lastflyedwin  int
	rtt           time.Duration
	minRtt        time.Duration
	minRttTime    time.Time
	lastPaceTime  time.Time
	paceTokens    float64
	pacingRate    float64
	pacedWait     bool
}

func newBBControl() *bbControl {
	return &bbControl{}
}

func (bb *bbControl) EnablePacing(initialRtt time.Duration, activeConns int) {
	if initialRtt <= 0 {
		initialRtt = 100 * time.Millisecond
	}
	if activeConns < 1 {
		activeConns = 1
	}
	bb.rtt = initialRtt
	rate := float64(6*1024*1024) / float64(activeConns)
	if rate < 1536*1024 {
		rate = 1536 * 1024
	}
	bb.pacingRate = rate
	initFly := (4 * 1024 * 1024) / activeConns
	if initFly < bbMaxflyFloor {
		initFly = bbMaxflyFloor
	}
	if bb.maxfly < initFly {
		bb.maxfly = initFly
	}
}

func (bb *bbControl) takePacedWait() bool {
	if bb == nil {
		return false
	}
	w := bb.pacedWait
	bb.pacedWait = false
	return w
}

func (bb *bbControl) hasInflight() bool {
	if bb == nil {
		return false
	}
	return bb.flyingdata > 0
}

func (bb *bbControl) Init() {
	now := time.Now()
	bb.status = bbStatusInit
	bb.maxfly = bbMaxflyFloor
	bb.rateflywin = list.NewRList(bbWin)
	bb.flyedwin = list.NewRList(bbWin)
	bb.flyingdata = 0
	bb.flyeddata = 0
	bb.totalsent = 0
	bb.resendsSent = 0
	bb.lastflyeddata = 0
	bb.propindex = 0
	bb.last = now
	bb.rtt = 0
	bb.minRtt = 0
	bb.minRttTime = now
	bb.lastPaceTime = now
	bb.paceTokens = float64(128 * 1024)
	bb.pacingRate = 0
}

func (bb *bbControl) SetRtt(rtt time.Duration) {
	if rtt > 0 {
		bb.rtt = rtt
		now := time.Now()
		if bb.minRtt == 0 || (rtt < bb.minRtt && rtt > 5*time.Millisecond) || now.Sub(bb.minRttTime) > 30*time.Second {
			bb.minRtt = rtt
			bb.minRttTime = now
		}
		if rtt < 5*time.Millisecond {
			if bb.pacingRate < 64*1024*1024 {
				bb.pacingRate = 64 * 1024 * 1024
			}
			if bb.maxfly < 8*1024*1024 {
				bb.maxfly = 8 * 1024 * 1024
			}
			return
		}
		if bb.pacingRate == 0 && bb.maxfly > 0 {
			initialRate := float64(1024*1024) / bb.rtt.Seconds()
			if initialRate < 6*1024*1024 {
				initialRate = 6 * 1024 * 1024
			} else if initialRate > 10*1024*1024 {
				initialRate = 10 * 1024 * 1024
			}
			bb.pacingRate = initialRate
			if bb.maxfly < 4*1024*1024 {
				bb.maxfly = 4 * 1024 * 1024
			}
		}
	}
}

func (bb *bbControl) RecvAck(id int, size int) {
	if size < 0 {
		panic("error size")
	}
	bb.flyeddata += size
	if bb.rtt > 0 {
		bb.flyingdata -= size
		if bb.flyingdata < 0 {
			bb.flyingdata = 0
		}
	}
}

func (bb *bbControl) CanSend(id int, size int) bool {
	if size < 0 {
		panic("error size")
	}
	if bb.flyingdata > bb.maxfly {
		return false
	}
	if bb.rtt > 0 && bb.pacingRate > 0 {
		now := time.Now()
		elapsed := now.Sub(bb.lastPaceTime)
		if elapsed > 0 {
			bb.lastPaceTime = now
			bb.paceTokens += elapsed.Seconds() * bb.pacingRate
			maxBurst := float64(256 * 1024)
			if bb.paceTokens > maxBurst {
				bb.paceTokens = maxBurst
			}
		}
		if bb.paceTokens < float64(size) {
			bb.pacedWait = true
			return false
		}
		bb.paceTokens -= float64(size)
	}
	bb.flyingdata += size
	bb.totalsent += size
	return true
}

func (bb *bbControl) CanSendResend(id int, size int) bool {
	if size < 0 {
		panic("error size")
	}
	if bb.rtt > 0 && bb.pacingRate > 0 {
		now := time.Now()
		elapsed := now.Sub(bb.lastPaceTime)
		if elapsed > 0 {
			bb.lastPaceTime = now
			bb.paceTokens += elapsed.Seconds() * bb.pacingRate
			maxBurst := float64(256 * 1024)
			if bb.paceTokens > maxBurst {
				bb.paceTokens = maxBurst
			}
		}
		if bb.paceTokens < -float64(128*1024) {
			bb.pacedWait = true
			return false
		}
		bb.paceTokens -= float64(size)
	}
	bb.resendsSent += size
	return true
}

func (bb *bbControl) Update() {
	now := time.Now()
	elapsed := now.Sub(bb.last).Seconds()
	bb.last = now
	if elapsed < 0.2 {
		elapsed = 0.2
	} else if elapsed > 5.0 {
		elapsed = 5.0
	}

	if bb.flyeddata <= 0 {
		if bb.flyingdata > 0 || bb.totalsent > 0 {
			bb.flyingdata = 0
			bb.totalsent = 0
			bb.resendsSent = 0
			shrunk := int(float64(bb.maxfly) / bbMaxflyGrow)
			if shrunk < bbLostFloor {
				shrunk = bbLostFloor
			}
			bb.maxfly = shrunk
			if bb.pacingRate > 2*1024*1024 {
				bb.pacingRate *= 0.7
				if bb.pacingRate < 2*1024*1024 {
					bb.pacingRate = 2 * 1024 * 1024
				}
			}
		}
		return
	}

	if bb.rtt > 0 && bb.totalsent < 128*1024 && bb.flyeddata < 128*1024 {
		bb.flyeddata = 0
		bb.flyingdata = 0
		bb.totalsent = 0
		bb.resendsSent = 0
		return
	}

	sent := bb.totalsent
	if bb.rtt <= 0 || sent <= 0 {
		sent = bb.flyingdata
	}
	currate := float64(sent) / float64(bb.flyeddata)
	if currate < 1 {
		currate = 1
	} else if currate > 4 {
		currate = 4
	}

	if bb.rateflywin.Full() {
		bb.rateflywin.PopFront()
	}
	bb.rateflywin.PushBack(currate)

	lastratewin := math.MaxFloat64
	sumRate := 0.0
	cntRate := 0
	for e := bb.rateflywin.FrontInter(); e != nil; e = e.Next() {
		rate := e.Value.(float64)
		if rate < lastratewin {
			lastratewin = rate
		}
		sumRate += rate
		cntRate++
	}
	avgRate := currate
	if cntRate > 0 {
		avgRate = sumRate / float64(cntRate)
	}

	lossComp := avgRate
	if lossComp < 1.0 {
		lossComp = 1.0
	} else if lossComp > 1.8 {
		lossComp = 1.8
	}

	if bb.rtt > 0 {
		flyedRate := int(float64(bb.flyeddata) / elapsed)
		sentRate := int(float64(sent) / elapsed)
		effectiveFlyed := flyedRate
		if sentRate > 0 && effectiveFlyed > int(float64(sentRate)*1.15) {
			effectiveFlyed = int(float64(sentRate) * 1.15)
		}
		if bb.flyedwin.Full() {
			bb.flyedwin.PopFront()
		}
		bb.flyedwin.PushBack(effectiveFlyed)

		lastflyedwin := 0
		for e := bb.flyedwin.FrontInter(); e != nil; e = e.Next() {
			flyed := e.Value.(int)
			if flyed > lastflyedwin {
				lastflyedwin = flyed
			}
		}

		queueDelay := time.Duration(0)
		if bb.minRtt > 0 && bb.rtt > bb.minRtt {
			queueDelay = bb.rtt - bb.minRtt
		}

		if bb.status == bbStatusInit {
			growFactor := 1.40
			plateau := bb.lastflyeddata > 0 && float64(flyedRate) < float64(bb.lastflyeddata)*1.03 && flyedRate > 6*1024*1024 && queueDelay > 30*time.Millisecond
			severe := queueDelay > 100*time.Millisecond
			if severe || plateau {
				bb.status = bbStatusProp
				deliveryRate := float64(flyedRate) * lossComp
				if deliveryRate < 1024*1024 {
					deliveryRate = 1024 * 1024
				}
				bdp := int(deliveryRate * bb.rtt.Seconds())
				if bdp < 512*1024 {
					bdp = 512 * 1024
				}
				bb.maxfly = int(float64(bdp) * 2.5)
				if bb.maxfly < bbMaxflyFloor {
					bb.maxfly = bbMaxflyFloor
				}
				bb.pacingRate = deliveryRate * 1.0
			} else {
				bb.maxfly = int(float64(bb.maxfly) * growFactor)
				if bb.pacingRate <= 0 {
					bb.pacingRate = 5 * 1024 * 1024
				} else {
					nextRate := bb.pacingRate * growFactor
					if capRate := float64(flyedRate) * lossComp * 1.6; capRate > 2*1024*1024 && nextRate > capRate {
						nextRate = capRate
					}
					bb.pacingRate = nextRate
				}
				minFly := int(bb.pacingRate * bb.rtt.Seconds() * 2.8)
				if minFly < bbMaxflyFloor {
					minFly = bbMaxflyFloor
				}
				if bb.maxfly < minFly {
					bb.maxfly = minFly
				}
			}
			bb.lastflyeddata = flyedRate
		} else if bb.status == bbStatusProp {
			deliveryRate := float64(lastflyedwin)
			if deliveryRate < 512*1024 {
				deliveryRate = 512 * 1024
			}
			wireRate := deliveryRate * lossComp
			bdp := int(wireRate * bb.rtt.Seconds())
			if bdp < 512*1024 {
				bdp = 512 * 1024
			}
			if queueDelay > 110*time.Millisecond {
				bb.pacingRate = wireRate * 0.85
				bb.maxfly = int(float64(bdp) * 2.0)
			} else if queueDelay < 70*time.Millisecond {
				probeFactor := bbPacedPropSeq[bb.propindex%len(bbPacedPropSeq)]
				bb.pacingRate = wireRate * probeFactor
				bb.maxfly = int(float64(bdp) * 3.5)
			} else {
				bb.pacingRate = wireRate * 1.0
				bb.maxfly = int(float64(bdp) * 2.8)
			}
			if bb.pacingRate < 512*1024 {
				bb.pacingRate = 512 * 1024
			}
			if bb.maxfly < bbMaxflyFloor {
				bb.maxfly = bbMaxflyFloor
			}
			bb.propindex = (bb.propindex + 1) % len(bbPacedPropSeq)
		} else {
			panic("error status " + strconv.Itoa(bb.status))
		}

		bb.flyeddata = 0
		bb.flyingdata = 0
		bb.totalsent = 0
		bb.resendsSent = 0
		bb.lastratewin = lastratewin
		bb.lastflyedwin = lastflyedwin
		if bb.maxfly < bbMaxflyFloor {
			bb.maxfly = bbMaxflyFloor
		}
		return
	}

	if bb.flyedwin.Full() {
		bb.flyedwin.PopFront()
	}
	bb.flyedwin.PushBack(bb.flyeddata)

	lastflyedwin := 0
	for e := bb.flyedwin.FrontInter(); e != nil; e = e.Next() {
		flyed := e.Value.(int)
		if flyed > lastflyedwin {
			lastflyedwin = flyed
		}
	}

	if bb.status == bbStatusInit {
		if float64(bb.flyeddata) <= bbMaxflyCmp*float64(bb.lastflyeddata) {
			oldmaxfly := bb.maxfly
			bb.maxfly = int(float64(oldmaxfly) / bbMaxflyGrow)
			bb.status = bbStatusProp
		} else {
			oldmaxfly := bb.maxfly
			bb.maxfly = int(float64(oldmaxfly) * bbMaxflyGrow)
		}
		bb.lastflyeddata = bb.flyeddata
	} else if bb.status == bbStatusProp {
		maxfly := float64(lastflyedwin) * lastratewin
		curmaxfly := int(maxfly)
		if curmaxfly > bb.maxfly {
			bb.maxfly = curmaxfly
		} else if common.NearlyEqual(bb.flyingdata, bb.maxfly) {
			bb.maxfly = curmaxfly
		}
		bb.maxfly = int(float64(bb.maxfly) * bbPropSeq[bb.propindex])
		bb.propindex++
		bb.propindex = bb.propindex % len(bbPropSeq)
	} else {
		panic("error status " + strconv.Itoa(bb.status))
	}

	bb.flyeddata = 0
	bb.flyingdata = 0
	bb.totalsent = 0
	bb.resendsSent = 0
	bb.lastratewin = lastratewin
	bb.lastflyedwin = lastflyedwin

	if bb.maxfly < bbMaxflyFloor {
		bb.maxfly = bbMaxflyFloor
	}
}

func (bb *bbControl) Info() string {
	return fmt.Sprintf("status %v maxfly %v flyeddata %v rtt %v lastratewin %v lastflyedwin %v", bb.status, bb.maxfly,
		bb.flyeddata, bb.rtt, bb.lastratewin, bb.lastflyedwin)
}
