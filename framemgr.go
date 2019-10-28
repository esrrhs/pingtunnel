package pingtunnel

import (
	"bytes"
	"compress/zlib"
	"container/list"
	"github.com/esrrhs/go-engine/src/common"
	"github.com/esrrhs/go-engine/src/loggo"
	"github.com/esrrhs/go-engine/src/rbuffergo"
	"io"
	"strconv"
	"sync"
	"time"
)

type FrameStat struct {
	sendDataNum     int
	recvDataNum     int
	sendReqNum      int
	recvReqNum      int
	sendAckNum      int
	recvAckNum      int
	sendDataNumsMap map[int32]int
	recvDataNumsMap map[int32]int
	sendReqNumsMap  map[int32]int
	recvReqNumsMap  map[int32]int
	sendAckNumsMap  map[int32]int
	recvAckNumsMap  map[int32]int
	sendping        int
	sendpong        int
	recvping        int
	recvpong        int
}

type FrameMgr struct {
	sendb *rbuffergo.RBuffergo
	recvb *rbuffergo.RBuffergo

	recvlock      sync.Locker
	windowsize    int
	resend_timems int
	compress      int

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

	connected bool

	fs            *FrameStat
	openstat      int
	lastPrintStat int64
}

func NewFrameMgr(buffersize int, windowsize int, resend_timems int, compress int, openstat int) *FrameMgr {

	sendb := rbuffergo.New(buffersize, false)
	recvb := rbuffergo.New(buffersize, false)

	fm := &FrameMgr{sendb: sendb, recvb: recvb,
		recvlock:   &sync.Mutex{},
		windowsize: windowsize, resend_timems: resend_timems, compress: compress,
		sendwin: list.New(), sendlist: list.New(), sendid: 0,
		recvwin: list.New(), recvlist: list.New(), recvid: 0,
		close: false, remoteclosed: false, closesend: false,
		lastPingTime: time.Now().UnixNano(), rttns: (int64)(resend_timems * 1000),
		reqmap: make(map[int32]int64), sendmap: make(map[int32]int64),
		connected: false, openstat: openstat, lastPrintStat: time.Now().UnixNano()}
	if openstat > 0 {
		fm.resetStat()
	}
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

	fm.printStat()
}

func (fm *FrameMgr) cutSendBufferToWindow() {

	sendall := false

	if fm.sendb.Size() < FRAME_MAX_SIZE {
		sendall = true
	}

	for fm.sendb.Size() >= FRAME_MAX_SIZE && fm.sendwin.Len() < fm.windowsize {
		fd := &FrameData{Type: (int32)(FrameData_USER_DATA),
			Data: make([]byte, FRAME_MAX_SIZE)}
		fm.sendb.Read(fd.Data)

		if fm.compress > 0 && len(fd.Data) > fm.compress {
			newb := fm.compressData(fd.Data)
			if len(newb) < len(fd.Data) {
				fd.Data = newb
				fd.Compress = true
			}
		}

		f := &Frame{Type: (int32)(Frame_DATA),
			Id:   (int32)(fm.sendid),
			Data: fd}

		fm.sendid++
		if fm.sendid >= FRAME_MAX_ID {
			fm.sendid = 0
		}

		fm.sendwin.PushBack(f)
		loggo.Debug("cut frame push to send win %d %d %d", f.Id, FRAME_MAX_SIZE, fm.sendwin.Len())
	}

	if sendall && fm.sendb.Size() > 0 && fm.sendwin.Len() < fm.windowsize {
		fd := &FrameData{Type: (int32)(FrameData_USER_DATA),
			Data: make([]byte, fm.sendb.Size())}
		fm.sendb.Read(fd.Data)

		if fm.compress > 0 && len(fd.Data) > fm.compress {
			newb := fm.compressData(fd.Data)
			if len(newb) < len(fd.Data) {
				fd.Data = newb
				fd.Compress = true
			}
		}

		f := &Frame{Type: (int32)(Frame_DATA),
			Id:   (int32)(fm.sendid),
			Data: fd}

		fm.sendid++
		if fm.sendid >= FRAME_MAX_ID {
			fm.sendid = 0
		}

		fm.sendwin.PushBack(f)
		loggo.Debug("cut small frame push to send win %d %d %d", f.Id, len(f.Data.Data), fm.sendwin.Len())
	}

	if fm.sendb.Empty() && fm.close && !fm.closesend && fm.sendwin.Len() < fm.windowsize {
		fd := &FrameData{Type: (int32)(FrameData_CLOSE)}

		f := &Frame{Type: (int32)(Frame_DATA),
			Id:   (int32)(fm.sendid),
			Data: fd}

		fm.sendid++
		if fm.sendid >= FRAME_MAX_ID {
			fm.sendid = 0
		}

		fm.sendwin.PushBack(f)
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
				if fm.openstat > 0 {
					fm.fs.sendDataNum++
					fm.fs.sendDataNumsMap[f.Id]++
				}
				loggo.Debug("push frame to sendlist %d %d", f.Id, len(f.Data.Data))
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
			if fm.openstat > 0 {
				fm.fs.recvDataNum++
				fm.fs.recvDataNumsMap[f.Id]++
			}
			loggo.Debug("recv data %d %d", f.Id, len(f.Data.Data))
		} else if f.Type == (int32)(Frame_PING) {
			fm.processPing(f)
		} else if f.Type == (int32)(Frame_PONG) {
			fm.processPong(f)
		} else {
			loggo.Error("error frame type %d", f.Type)
		}
	}
	fm.recvlist.Init()
	return tmpreq, tmpack, tmpackto
}

func (fm *FrameMgr) processRecvList(tmpreq map[int32]int, tmpack map[int32]int, tmpackto map[int32]*Frame) {

	for id, num := range tmpreq {
		for e := fm.sendwin.Front(); e != nil; e = e.Next() {
			f := e.Value.(*Frame)
			if f.Id == id {
				f.Resend = true
				loggo.Debug("choose resend win %d %d", f.Id, len(f.Data.Data))
				break
			}
		}
		if fm.openstat > 0 {
			fm.fs.recvReqNum += num
			fm.fs.recvReqNumsMap[id] += num
		}
	}

	for id, num := range tmpack {
		for e := fm.sendwin.Front(); e != nil; e = e.Next() {
			f := e.Value.(*Frame)
			if f.Id == id {
				fm.sendwin.Remove(e)
				delete(fm.sendmap, f.Id)
				loggo.Debug("remove send win %d %d", f.Id, len(f.Data.Data))
				break
			}
		}
		if fm.openstat > 0 {
			fm.fs.recvAckNum += num
			fm.fs.recvAckNumsMap[id] += num
		}
	}

	if len(tmpackto) > 0 {
		tmp := make([]int32, len(tmpackto))
		index := 0
		for id, rf := range tmpackto {
			if fm.addToRecvWin(rf) {
				tmp[index] = id
				index++
				if fm.openstat > 0 {
					fm.fs.sendAckNum++
					fm.fs.sendAckNumsMap[id]++
				}
				loggo.Debug("add data to win %d %d", rf.Id, len(rf.Data.Data))
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
			loggo.Debug("recv frame ignore %d %d", f.Id, len(f.Data.Data))
			return true
		}
	}

	for e := fm.recvwin.Front(); e != nil; e = e.Next() {
		f := e.Value.(*Frame)
		loggo.Debug("start insert recv win %d %d %d", fm.recvid, rf.Id, f.Id)
		if fm.compareId((int)(rf.Id), (int)(f.Id)) < 0 {
			fm.recvwin.InsertBefore(rf, e)
			loggo.Debug("insert recv win %d %d before %d", rf.Id, len(rf.Data.Data), f.Id)
			return true
		}
	}

	fm.recvwin.PushBack(rf)
	loggo.Debug("insert recv win last %d %d", rf.Id, len(rf.Data.Data))
	return true
}

func (fm *FrameMgr) processRecvFrame(f *Frame) bool {
	if f.Data.Type == (int32)(FrameData_USER_DATA) {
		left := fm.recvb.Capacity() - fm.recvb.Size()
		if left >= len(f.Data.Data) {
			src := f.Data.Data
			if f.Data.Compress {
				err, old := fm.deCompressData(src)
				if err != nil {
					loggo.Error("recv frame deCompressData error %d", f.Id)
					return false
				}
				if left < len(old) {
					return false
				}
				loggo.Debug("deCompressData recv frame %d %d %d",
					f.Id, len(src), len(old))
				src = old
			}

			fm.recvb.Write(src)
			loggo.Debug("combined recv frame to recv buffer %d %d",
				f.Id, len(src))
			return true
		}
		return false
	} else if f.Data.Type == (int32)(FrameData_CLOSE) {
		fm.remoteclosed = true
		loggo.Debug("recv remote close frame %d", f.Id)
		return true
	} else if f.Data.Type == (int32)(FrameData_CONN) {
		fm.sendConnectRsp()
		fm.connected = true
		loggo.Debug("recv remote conn frame %d", f.Id)
		return true
	} else if f.Data.Type == (int32)(FrameData_CONNRSP) {
		fm.connected = true
		loggo.Debug("recv remote conn rsp frame %d", f.Id)
		return true
	} else {
		loggo.Error("recv frame type error %d", f.Data.Type)
		return false
	}
}

func (fm *FrameMgr) combineWindowToRecvBuffer() {

	for {
		done := false
		for e := fm.recvwin.Front(); e != nil; e = e.Next() {
			f := e.Value.(*Frame)
			if f.Id == (int32)(fm.recvid) {
				delete(fm.reqmap, f.Id)
				if fm.processRecvFrame(f) {
					fm.recvwin.Remove(e)
					done = true
					loggo.Debug("process recv frame ok %d %d",
						f.Id, len(f.Data.Data))
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
			if fm.openstat > 0 {
				fm.fs.sendReqNum++
				fm.fs.sendReqNumsMap[(int32)(id)]++
			}
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
		fm.lastPingTime = cur
		f := &Frame{Type: (int32)(Frame_PING), Resend: false, Sendtime: cur,
			Id: 0}
		fm.sendlist.PushBack(f)
		loggo.Debug("send ping %d", cur)
		if fm.openstat > 0 {
			fm.fs.sendping++
		}
	}
}

func (fm *FrameMgr) processPing(f *Frame) {
	rf := &Frame{Type: (int32)(Frame_PONG), Resend: false, Sendtime: f.Sendtime,
		Id: 0}
	fm.sendlist.PushBack(rf)
	if fm.openstat > 0 {
		fm.fs.recvping++
		fm.fs.sendpong++
	}
	loggo.Debug("recv ping %d", f.Sendtime)
}

func (fm *FrameMgr) processPong(f *Frame) {
	cur := time.Now().UnixNano()
	if cur > f.Sendtime {
		rtt := cur - f.Sendtime
		fm.rttns = (fm.rttns + rtt) / 2
		if fm.openstat > 0 {
			fm.fs.recvpong++
		}
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

func (fm *FrameMgr) IsConnected() bool {
	return fm.connected
}

func (fm *FrameMgr) Connect() {
	fd := &FrameData{Type: (int32)(FrameData_CONN)}

	f := &Frame{Type: (int32)(Frame_DATA),
		Id:   (int32)(fm.sendid),
		Data: fd}

	fm.sendid++
	if fm.sendid >= FRAME_MAX_ID {
		fm.sendid = 0
	}

	fm.sendwin.PushBack(f)
	loggo.Debug("start connect")
}

func (fm *FrameMgr) sendConnectRsp() {
	fd := &FrameData{Type: (int32)(FrameData_CONNRSP)}

	f := &Frame{Type: (int32)(Frame_DATA),
		Id:   (int32)(fm.sendid),
		Data: fd}

	fm.sendid++
	if fm.sendid >= FRAME_MAX_ID {
		fm.sendid = 0
	}

	fm.sendwin.PushBack(f)
	loggo.Debug("send connect rsp")
}

func (fm *FrameMgr) compressData(src []byte) []byte {
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	w.Write(src)
	w.Close()
	return b.Bytes()
}

func (fm *FrameMgr) deCompressData(src []byte) (error, []byte) {
	b := bytes.NewReader(src)
	r, err := zlib.NewReader(b)
	if err != nil {
		return err, nil
	}
	var out bytes.Buffer
	io.Copy(&out, r)
	r.Close()
	return nil, out.Bytes()
}

func (fm *FrameMgr) resetStat() {
	fm.fs = &FrameStat{}
	fm.fs.sendDataNumsMap = make(map[int32]int)
	fm.fs.recvDataNumsMap = make(map[int32]int)
	fm.fs.sendReqNumsMap = make(map[int32]int)
	fm.fs.recvReqNumsMap = make(map[int32]int)
	fm.fs.sendAckNumsMap = make(map[int32]int)
	fm.fs.recvAckNumsMap = make(map[int32]int)
}

func (fm *FrameMgr) printStat() {
	if fm.openstat > 0 {
		cur := time.Now().UnixNano()
		if cur-fm.lastPrintStat > (int64)(time.Second) {
			fm.lastPrintStat = cur
			fs := fm.fs
			loggo.Info("\nsendDataNum %d\nrecvDataNum %d\nsendReqNum %d\nrecvReqNum %d\nsendAckNum %d\nrecvAckNum %d\n"+
				"sendDataNumsMap %s\nrecvDataNumsMap %s\nsendReqNumsMap %s\nrecvReqNumsMap %s\nsendAckNumsMap %s\nrecvAckNumsMap %s\n"+
				"sendping %d\nrecvping %d\nsendpong %d\nrecvpong %d\n",
				fs.sendDataNum, fs.recvDataNum,
				fs.sendReqNum, fs.recvReqNum,
				fs.sendAckNum, fs.recvAckNum,
				fm.printStatMap(&fs.sendDataNumsMap), fm.printStatMap(&fs.recvDataNumsMap),
				fm.printStatMap(&fs.sendReqNumsMap), fm.printStatMap(&fs.recvReqNumsMap),
				fm.printStatMap(&fs.sendAckNumsMap), fm.printStatMap(&fs.recvAckNumsMap),
				fs.sendping, fs.recvping,
				fs.sendpong, fs.recvpong)
			fm.resetStat()
		}
	}
}

func (fm *FrameMgr) printStatMap(m *map[int32]int) string {
	tmp := make(map[int]int)
	for _, v := range *m {
		tmp[v]++
	}
	max := 0
	for k, _ := range tmp {
		if k > max {
			max = k
		}
	}
	var ret string
	for i := 1; i <= max; i++ {
		ret += strconv.Itoa(i) + "->" + strconv.Itoa(tmp[i]) + ","
	}
	if len(ret) <= 0 {
		ret = "none"
	}
	return ret
}
