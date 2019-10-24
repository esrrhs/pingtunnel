package pingtunnel

import (
	"container/list"
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
}

func NewFrameMgr(buffersize int, windowsize int, resend_timems int) *FrameMgr {

	sendb := rbuffergo.New(buffersize, false)
	recvb := rbuffergo.New(buffersize, false)

	fm := &FrameMgr{sendb: sendb, recvb: recvb,
		recvlock:   &sync.Mutex{},
		windowsize: windowsize, resend_timems: resend_timems,
		sendwin: list.New(), sendlist: list.New(), sendid: 0,
		recvwin: list.New(), recvlist: list.New(), recvid: 0}

	return fm
}

func (fm *FrameMgr) GetSendBufferLeft() int {
	left := fm.sendb.Capacity() - fm.sendb.Size()
	return left
}

func (fm *FrameMgr) WriteSendBuffer(data []byte) {
	fm.sendb.Write(data)
}

func (fm *FrameMgr) Update() {
	fm.cutSendBufferToWindow()

	fm.sendlist.Init()

	tmpreq, tmpack, tmpackto := fm.preProcessRecvList()
	fm.processRecvList(tmpreq, tmpack, tmpackto)

	fm.combineWindowToRecvBuffer()

	fm.calSendList()
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
	}
}

func (fm *FrameMgr) calSendList() {
	cur := time.Now().UnixNano()
	for e := fm.sendwin.Front(); e != nil; e = e.Next() {
		f := e.Value.(*Frame)
		if f.Resend || cur-f.Sendtime > int64(fm.resend_timems*1000) {
			f.Sendtime = cur
			fm.sendlist.PushBack(f)
			f.Resend = false
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
			}
		} else if f.Type == (int32)(Frame_ACK) {
			for _, id := range f.Dataid {
				tmpack[id]++
			}
		} else if f.Type == (int32)(Frame_DATA) {
			tmpackto[f.Id] = f
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
				break
			}
		}
	}

	for id, _ := range tmpack {
		for e := fm.sendwin.Front(); e != nil; e = e.Next() {
			f := e.Value.(*Frame)
			if f.Id == id {
				fm.sendwin.Remove(e)
				break
			}
		}
	}

	if len(tmpackto) > 0 {
		f := &Frame{Type: (int32)(Frame_ACK), Resend: false, Sendtime: 0,
			Id:     0,
			Dataid: make([]int32, len(tmpackto))}
		index := 0
		for id, rf := range tmpackto {
			f.Dataid[index] = id
			index++
			fm.addToRecvWin(rf)
		}
		fm.sendlist.PushBack(f)
	}
}

func (fm *FrameMgr) addToRecvWin(rf *Frame) {

	begin := fm.recvid
	end := fm.recvid + fm.windowsize
	id := (int)(rf.Id)
	if id < begin {
		id += FRAME_MAX_ID
	}
	if id > end || id < begin {
		return
	}

	for e := fm.recvwin.Front(); e != nil; e = e.Next() {
		f := e.Value.(*Frame)
		if f.Id == rf.Id {
			return
		}
	}

	for e := fm.recvwin.Front(); e != nil; e = e.Next() {
		f := e.Value.(*Frame)
		if fm.compareId(rf, f) < 0 {
			fm.recvwin.InsertBefore(rf, e)
			return
		}
	}

	fm.recvwin.PushBack(rf)
}

func (fm *FrameMgr) compareId(lf *Frame, rf *Frame) int {

	l := (int)(lf.Id)
	r := (int)(rf.Id)
	if l < fm.recvid {
		l += FRAME_MAX_ID
	}
	if r < fm.recvid {
		r += FRAME_MAX_ID
	}

	return l - r
}

func (fm *FrameMgr) combineWindowToRecvBuffer() {

	id := fm.recvid

	for {
		done := false
		for e := fm.recvwin.Front(); e != nil; e = e.Next() {
			f := e.Value.(*Frame)
			if f.Id == (int32)(id) {
				left := fm.recvb.Capacity() - fm.recvb.Size()
				if left >= len(f.Data) {
					fm.recvb.Write(f.Data)
					fm.recvwin.Remove(e)
					done = true
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
		}
	}

	reqtmp := make(map[int]int)
	e := fm.recvwin.Front()
	id = fm.recvid
	for len(reqtmp) < fm.windowsize && e != nil {
		f := e.Value.(*Frame)
		if f.Id != (int32)(id) {
			reqtmp[id]++
		} else {
			reqtmp[id]++
			e = e.Next()
		}

		id++
		if fm.recvid >= FRAME_MAX_ID {
			fm.recvid = 0
		}
	}

	f := &Frame{Type: (int32)(Frame_REQ), Resend: false, Sendtime: 0,
		Id:     0,
		Dataid: make([]int32, len(reqtmp))}
	index := 0
	for id, _ := range reqtmp {
		f.Dataid[index] = (int32)(id)
		index++
	}
	fm.sendlist.PushBack(f)
}

func (fm *FrameMgr) GetRecvBufferSize() int {
	return fm.recvb.Size()
}

func (fm *FrameMgr) GetRecvReadLineBuffer() []byte {
	return fm.recvb.GetReadLineBuffer()
}

func (fm *FrameMgr) SkipRecvBuffer(size int) {
	fm.recvb.SkipRead(size)
}
