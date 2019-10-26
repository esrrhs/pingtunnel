package pingtunnel

import (
	"container/list"
	"github.com/esrrhs/go-engine/src/common"
	"github.com/esrrhs/go-engine/src/loggo"
	"github.com/esrrhs/go-engine/src/rbuffergo"
	"sync"
	"time"
)

type FrameMgr struct {
	sendb *rbuffergo.RBuffergo
	recvb *rbuffergo.RBuffergo

	recvlock      sync.Locker
	windowsize    int
	resend_timems int

	sendwin  *list.List
	sendlist *list.List
	sendid   int

	recvwin  *list.List
	recvlist *list.List
	recvid   int

	close        bool
	remoteclosed bool
	closesend    bool

	lastPingTime int64
	rttns        int64

	reqmap  map[int32]int64
	sendmap map[int32]int64
}

func NewFrameMgr(buffersize int, windowsize int, resend_timems int) *FrameMgr {

	sendb := rbuffergo.New(buffersize, false)
	recvb := rbuffergo.New(buffersize, false)

	fm := &FrameMgr{sendb: sendb, recvb: recvb,
		recvlock:   &sync.Mutex{},
		windowsize: windowsize, resend_timems: resend_timems,
		sendwin: list.New(), sendlist: list.New(), sendid: 0,
		recvwin: list.New(), recvlist: list.New(), recvid: 0,
		close: false, remoteclosed: false, closesend: false,
		lastPingTime: time.Now().UnixNano(), rttns: (int64)(resend_timems * 1000),
		reqmap: make(map[int32]int64), sendmap: make(map[int32]int64)}

	return fm
}

func (fm *FrameMgr) GetSendBufferLeft() int {
	left := fm.sendb.Capacity() - fm.sendb.Size()
	return left
}

func (fm *FrameMgr) WriteSendBuffer(data []byte) {
	fm.sendb.Write(data)
	loggo.Debug("WriteSendBuffer %d %d", fm.sendb.Size(), len(data))
}

func (fm *FrameMgr) Update() {
	fm.cutSendBufferToWindow()

	fm.sendlist.Init()

	tmpreq, tmpack, tmpackto := fm.preProcessRecvList()
	fm.processRecvList(tmpreq, tmpack, tmpackto)

	fm.combineWindowToRecvBuffer()

	fm.calSendList()

	fm.ping()
}

func (fm *FrameMgr) cutSendBufferToWindow() {

	sendall := false

	if fm.sendb.Size() < FRAME_MAX_SIZE {
		sendall = true
	}

	for fm.sendb.Size() >= FRAME_MAX_SIZE && fm.sendwin.Len() < fm.windowsize {
		f := &Frame{Type: (int32)(Frame_DATA), Resend: false, Sendtime: 0,
			Id:   (int32)(fm.sendid),
			Data: make([]byte, FRAME_MAX_SIZE)}
		fm.sendb.Read(f.Data)

		fm.sendid++
		if fm.sendid >= FRAME_MAX_ID {
			fm.sendid = 0
		}

		fm.sendwin.PushBack(f)
		loggo.Debug("cut frame push to send win %d %d %d", f.Id, FRAME_MAX_SIZE, fm.sendwin.Len())
	}

	if sendall && fm.sendb.Size() > 0 && fm.sendwin.Len() < fm.windowsize {
		f := &Frame{Type: (int32)(Frame_DATA), Resend: false, Sendtime: 0,
			Id:   (int32)(fm.sendid),
			Data: make([]byte, fm.sendb.Size())}
		fm.sendb.Read(f.Data)

		fm.sendid++
		if fm.sendid >= FRAME_MAX_ID {
			fm.sendid = 0
		}

		fm.sendwin.PushBack(f)
		loggo.Debug("cut small frame push to send win %d %d %d", f.Id, len(f.Data), fm.sendwin.Len())
	}

	if fm.sendb.Empty() && fm.close && !fm.closesend && fm.sendwin.Len() < fm.windowsize {
		f := &Frame{Type: (int32)(Frame_DATA), Resend: false, Sendtime: 0,
			Id:   (int32)(fm.sendid),
			Data: make([]byte, 0)}
		fm.sendwin.PushBack(f)

		fm.sendid++
		if fm.sendid >= FRAME_MAX_ID {
			fm.sendid = 0
		}

		fm.closesend = true
		loggo.Debug("close frame push to send win %d %d", f.Id, fm.sendwin.Len())
	}
}

func (fm *FrameMgr) calSendList() {
	cur := time.Now().UnixNano()
	for e := fm.sendwin.Front(); e != nil; e = e.Next() {
		f := e.Value.(*Frame)
		if f.Resend || cur-f.Sendtime > int64(fm.resend_timems*(int)(time.Millisecond)) {
			oldsend := fm.sendmap[f.Id]
			if cur-oldsend > fm.rttns {
				f.Sendtime = cur
				fm.sendlist.PushBack(f)
				f.Resend = false
				fm.sendmap[f.Id] = cur
				loggo.Debug("push frame to sendlist %d %d", f.Id, len(f.Data))
			}
		}
	}
}

func (fm *FrameMgr) getSendList() *list.List {
	return fm.sendlist
}

func (fm *FrameMgr) OnRecvFrame(f *Frame) {
	fm.recvlock.Lock()
	defer fm.recvlock.Unlock()
	fm.recvlist.PushBack(f)
}

func (fm *FrameMgr) preProcessRecvList() (map[int32]int, map[int32]int, map[int32]*Frame) {
	fm.recvlock.Lock()
	defer fm.recvlock.Unlock()

	tmpreq := make(map[int32]int)
	tmpack := make(map[int32]int)
	tmpackto := make(map[int32]*Frame)
	for e := fm.recvlist.Front(); e != nil; e = e.Next() {
		f := e.Value.(*Frame)
		if f.Type == (int32)(Frame_REQ) {
			for _, id := range f.Dataid {
				tmpreq[id]++
				loggo.Debug("recv req %d %s", f.Id, common.Int32ArrayToString(f.Dataid, ","))
			}
		} else if f.Type == (int32)(Frame_ACK) {
			for _, id := range f.Dataid {
				tmpack[id]++
				loggo.Debug("recv ack %d %s", f.Id, common.Int32ArrayToString(f.Dataid, ","))
			}
		} else if f.Type == (int32)(Frame_DATA) {
			tmpackto[f.Id] = f
			loggo.Debug("recv data %d %d", f.Id, len(f.Data))
		} else if f.Type == (int32)(Frame_PING) {
			fm.processPing(f)
		} else if f.Type == (int32)(Frame_PONG) {
			fm.processPong(f)
		}
	}
	fm.recvlist.Init()
	return tmpreq, tmpack, tmpackto
}

func (fm *FrameMgr) processRecvList(tmpreq map[int32]int, tmpack map[int32]int, tmpackto map[int32]*Frame) {

	for id, _ := range tmpreq {
		for e := fm.sendwin.Front(); e != nil; e = e.Next() {
			f := e.Value.(*Frame)
			if f.Id == id {
				f.Resend = true
				loggo.Debug("choose resend win %d %d", f.Id, len(f.Data))
				break
			}
		}
	}

	for id, _ := range tmpack {
		for e := fm.sendwin.Front(); e != nil; e = e.Next() {
			f := e.Value.(*Frame)
			if f.Id == id {
				fm.sendwin.Remove(e)
				delete(fm.sendmap, f.Id)
				loggo.Debug("remove send win %d %d", f.Id, len(f.Data))
				break
			}
		}
	}

	if len(tmpackto) > 0 {
		tmp := make([]int32, len(tmpackto))
		index := 0
		for id, rf := range tmpackto {
			if fm.addToRecvWin(rf) {
				tmp[index] = id
				index++
				loggo.Debug("add data to win %d %d", rf.Id, len(rf.Data))
			}
		}
		if index > 0 {
			f := &Frame{Type: (int32)(Frame_ACK), Resend: false, Sendtime: 0,
				Id:     0,
				Dataid: tmp[0:index]}
			fm.sendlist.PushBack(f)
			loggo.Debug("send ack %d %s", f.Id, common.Int32ArrayToString(f.Dataid, ","))
		}
	}
}

func (fm *FrameMgr) addToRecvWin(rf *Frame) bool {

	if !fm.isIdInRange((int)(rf.Id), FRAME_MAX_ID) {
		loggo.Debug("recv frame not in range %d %d", rf.Id, fm.recvid)
		if fm.isIdOld((int)(rf.Id), FRAME_MAX_ID) {
			return true
		}
		return false
	}

	for e := fm.recvwin.Front(); e != nil; e = e.Next() {
		f := e.Value.(*Frame)
		if f.Id == rf.Id {
			loggo.Debug("recv frame ignore %d %d", f.Id, len(f.Data))
			return true
		}
	}

	for e := fm.recvwin.Front(); e != nil; e = e.Next() {
		f := e.Value.(*Frame)
		loggo.Debug("start insert recv win %d %d %d", fm.recvid, rf.Id, f.Id)
		if fm.compareId((int)(rf.Id), (int)(f.Id)) < 0 {
			fm.recvwin.InsertBefore(rf, e)
			loggo.Debug("insert recv win %d %d before %d", rf.Id, len(rf.Data), f.Id)
			return true
		}
	}

	fm.recvwin.PushBack(rf)
	loggo.Debug("insert recv win last %d %d", rf.Id, len(rf.Data))
	return true
}

func (fm *FrameMgr) combineWindowToRecvBuffer() {

	for {
		done := false
		for e := fm.recvwin.Front(); e != nil; e = e.Next() {
			f := e.Value.(*Frame)
			if f.Id == (int32)(fm.recvid) {
				left := fm.recvb.Capacity() - fm.recvb.Size()
				if left >= len(f.Data) {
					if len(f.Data) == 0 {
						fm.remoteclosed = true
						loggo.Debug("recv remote close frame %d", f.Id)
					}
					fm.recvb.Write(f.Data)
					fm.recvwin.Remove(e)
					delete(fm.reqmap, f.Id)
					done = true
					loggo.Debug("combined recv frame to recv buffer %d %d", f.Id, len(f.Data))
					break
				}
			}
		}
		if !done {
			break
		} else {
			fm.recvid++
			if fm.recvid >= FRAME_MAX_ID {
				fm.recvid = 0
			}
			loggo.Debug("combined ok add recvid %d ", fm.recvid)
		}
	}

	cur := time.Now().UnixNano()
	reqtmp := make(map[int]int)
	e := fm.recvwin.Front()
	id := fm.recvid
	for len(reqtmp) < fm.windowsize && len(reqtmp)*4 < FRAME_MAX_SIZE/2 && e != nil {
		f := e.Value.(*Frame)
		loggo.Debug("start add req id %d %d %d", fm.recvid, f.Id, id)
		if f.Id != (int32)(id) {
			oldReq := fm.reqmap[f.Id]
			if cur-oldReq > fm.rttns {
				reqtmp[id]++
				fm.reqmap[f.Id] = cur
				loggo.Debug("add req id %d ", id)
			}
		} else {
			e = e.Next()
		}

		id++
		if id >= FRAME_MAX_ID {
			id = 0
		}
	}

	if len(reqtmp) > 0 {
		f := &Frame{Type: (int32)(Frame_REQ), Resend: false, Sendtime: 0,
			Id:     0,
			Dataid: make([]int32, len(reqtmp))}
		index := 0
		for id, _ := range reqtmp {
			f.Dataid[index] = (int32)(id)
			index++
		}
		fm.sendlist.PushBack(f)
		loggo.Debug("send req %d %s", f.Id, common.Int32ArrayToString(f.Dataid, ","))
	}
}

func (fm *FrameMgr) GetRecvBufferSize() int {
	return fm.recvb.Size()
}

func (fm *FrameMgr) GetRecvReadLineBuffer() []byte {
	ret := fm.recvb.GetReadLineBuffer()
	loggo.Debug("GetRecvReadLineBuffer %d %d", fm.recvb.Size(), len(ret))
	return ret
}

func (fm *FrameMgr) SkipRecvBuffer(size int) {
	fm.recvb.SkipRead(size)
	loggo.Debug("SkipRead %d %d", fm.recvb.Size(), size)
}

func (fm *FrameMgr) Close() {
	fm.recvlock.Lock()
	defer fm.recvlock.Unlock()

	fm.close = true
}

func (fm *FrameMgr) IsRemoteClosed() bool {
	return fm.remoteclosed
}

func (fm *FrameMgr) ping() {
	cur := time.Now().UnixNano()
	if cur-fm.lastPingTime > (int64)(time.Second) {
		f := &Frame{Type: (int32)(Frame_PING), Resend: false, Sendtime: cur,
			Id: 0}
		fm.sendlist.PushBack(f)
		loggo.Debug("send ping %d", cur)
		fm.lastPingTime = cur
	}
}

func (fm *FrameMgr) processPing(f *Frame) {
	rf := &Frame{Type: (int32)(Frame_PONG), Resend: false, Sendtime: f.Sendtime,
		Id: 0}
	fm.sendlist.PushBack(rf)
	loggo.Debug("recv ping %d", f.Sendtime)
}

func (fm *FrameMgr) processPong(f *Frame) {
	cur := time.Now().UnixNano()
	if cur > f.Sendtime {
		rtt := cur - f.Sendtime
		fm.rttns = (fm.rttns + rtt) / 2
		loggo.Debug("recv pong %d %dms", rtt, fm.rttns/1000/1000)
	}
}

func (fm *FrameMgr) isIdInRange(id int, maxid int) bool {
	begin := fm.recvid
	end := fm.recvid + fm.windowsize
	if end >= maxid {
		if id >= 0 && id < end-maxid {
			return true
		}
		end = maxid
	}
	if id >= begin && id < end {
		return true
	}
	return false
}

func (fm *FrameMgr) compareId(l int, r int) int {

	if l < fm.recvid {
		l += FRAME_MAX_ID
	}
	if r < fm.recvid {
		r += FRAME_MAX_ID
	}

	return l - r
}

func (fm *FrameMgr) isIdOld(id int, maxid int) bool {
	if id > fm.recvid {
		return false
	}

	end := fm.recvid + fm.windowsize*2
	if end >= maxid {
		if id >= end-maxid && id < fm.recvid {
			return true
		}
	} else {
		if id < fm.recvid {
			return true
		}
	}

	return false
}
