package pingtunnel

import (
	"container/list"
	"github.com/esrrhs/go-engine/src/rbuffergo"
	"math/rand"
	"sync"
	"time"
)

type FrameMgr struct {
	sendb         *rbuffergo.RBuffergo
	recvb         *rbuffergo.RBuffergo
	sendlock      sync.Locker
	recvlock      sync.Locker
	windowsize    int
	resend_timems int
	win           *list.List
	sendid        int
	sendlist      *list.List
}

func NewFrameMgr(buffersize int, windowsize int, resend_timems int) *FrameMgr {

	sendb := rbuffergo.New(buffersize, false)
	recvb := rbuffergo.New(buffersize, false)

	fm := &FrameMgr{sendb: sendb, recvb: recvb,
		sendlock: &sync.Mutex{}, recvlock: &sync.Mutex{},
		windowsize: windowsize, win: list.New(), sendid: rand.Int() % (FRAME_MAX_ID + 1),
		resend_timems: resend_timems, sendlist: list.New()}

	return fm
}

func (fm *FrameMgr) GetSendBufferLeft() int {
	left := fm.sendb.Capacity() - fm.sendb.Size()
	return left
}

func (fm *FrameMgr) WriteSendBuffer(data []byte) {
	fm.sendlock.Lock()
	defer fm.sendlock.Unlock()
	fm.sendb.Write(data)
}

func (fm *FrameMgr) Update() {
	fm.cutSendBufferToWindow()
	fm.calSendList()
}

func (fm *FrameMgr) cutSendBufferToWindow() {
	fm.sendlock.Lock()
	defer fm.sendlock.Unlock()

	sendall := false

	if fm.sendb.Size() < FRAME_MAX_SIZE {
		sendall = true
	}

	for fm.sendb.Size() > FRAME_MAX_SIZE && fm.win.Len() < fm.windowsize {
		f := Frame{resend: false, sendtime: 0,
			id: fm.sendid, size: FRAME_MAX_SIZE,
			data: make([]byte, FRAME_MAX_SIZE)}
		fm.sendb.Read(f.data)

		fm.sendid++
		if fm.sendid > FRAME_MAX_ID {
			fm.sendid = 0
		}

		fm.win.PushBack(f)
	}

	if sendall && fm.sendb.Size() > 0 && fm.win.Len() < fm.windowsize {
		f := Frame{resend: false, sendtime: 0,
			id: fm.sendid, size: fm.sendb.Size(),
			data: make([]byte, fm.sendb.Size())}
		fm.sendb.Read(f.data)

		fm.sendid++
		if fm.sendid > FRAME_MAX_ID {
			fm.sendid = 0
		}

		fm.win.PushBack(f)
	}
}

func (fm *FrameMgr) calSendList() {
	cur := time.Now().UnixNano()

	fm.sendlist.Init()

	for e := fm.win.Front(); e != nil; e = e.Next() {
		f := e.Value.(Frame)
		if f.resend || cur-f.sendtime > int64(fm.resend_timems*1000) {
			f.sendtime = cur
			fm.sendlist.PushBack(&f)
		}
	}
}

func (fm *FrameMgr) getSendList() *list.List {
	return fm.sendlist
}
