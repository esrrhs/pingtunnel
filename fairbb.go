package pingtunnel

import (
	"fmt"
	"math"
	"strconv"

	"github.com/esrrhs/gohome/common"
	"github.com/esrrhs/gohome/list"
)

// bbControl is the bandwidth-balancing controller used on TCP conns.
//
// It follows gohome's BBCongestion (per-flow max-in-flight, grown while ACKs
// keep up, 1MB floor on a healthy interval) with one correction: a wholly
// lost interval must release the in-flight counter. Stock BB returns early
// when nothing was ACKed and leaves flyingdata pinned above maxfly, so
// CanSend stays false and the flow never retransmits. On a real ICMP socket
// that is fatal — the kernel receive buffer is ~208KB, a close-phase drain
// dumps a megabyte in one loop, one conn's window is dropped, and that conn
// sits silent until the 60s close timeout.
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

type bbControl struct {
	status        int
	maxfly        int
	flyeddata     int
	lastflyeddata int
	flyingdata    int
	rateflywin    *list.Rlistgo
	flyedwin      *list.Rlistgo
	propindex     int
	lastratewin   float64
	lastflyedwin  int
}

func newBBControl() *bbControl {
	return &bbControl{}
}

func (bb *bbControl) Init() {
	bb.status = bbStatusInit
	bb.maxfly = bbMaxflyFloor
	bb.rateflywin = list.NewRList(bbWin)
	bb.flyedwin = list.NewRList(bbWin)
	bb.flyingdata = 0
	bb.flyeddata = 0
	bb.lastflyeddata = 0
	bb.propindex = 0
}

func (bb *bbControl) RecvAck(id int, size int) {
	if size < 0 {
		panic("error size")
	}
	bb.flyeddata += size
}

func (bb *bbControl) CanSend(id int, size int) bool {
	if size < 0 {
		panic("error size")
	}
	if bb.flyingdata > bb.maxfly {
		return false
	}
	bb.flyingdata += size
	return true
}

func (bb *bbControl) Update() {
	if bb.flyeddata <= 0 {
		if bb.flyingdata > 0 {
			bb.flyingdata = 0
			shrunk := int(float64(bb.maxfly) / bbMaxflyGrow)
			if shrunk < bbLostFloor {
				shrunk = bbLostFloor
			}
			bb.maxfly = shrunk
		}
		return
	}

	currate := float64(bb.flyingdata) / float64(bb.flyeddata)
	if currate < 1 {
		currate = 1
	}

	if bb.rateflywin.Full() {
		bb.rateflywin.PopFront()
	}
	bb.rateflywin.PushBack(currate)

	lastratewin := math.MaxFloat64
	for e := bb.rateflywin.FrontInter(); e != nil; e = e.Next() {
		rate := e.Value.(float64)
		if rate < lastratewin {
			lastratewin = rate
		}
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
	bb.lastratewin = lastratewin
	bb.lastflyedwin = lastflyedwin

	if bb.maxfly < bbMaxflyFloor {
		bb.maxfly = bbMaxflyFloor
	}
}

func (bb *bbControl) Info() string {
	return fmt.Sprintf("status %v maxfly %v flyeddata %v lastratewin %v lastflyedwin %v", bb.status, bb.maxfly,
		bb.flyeddata, bb.lastratewin, bb.lastflyedwin)
}
