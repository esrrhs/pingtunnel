package pingtunnel

import (
	"fmt"
	"github.com/golang/protobuf/proto"
	"testing"
)

func Test0001(t *testing.T) {

	my := &MyMsg{}
	my.Id = "12345"
	my.Target = "111:11"
	my.Type = 12
	my.Data = make([]byte, 0)
	dst, _ := proto.Marshal(my)
	fmt.Println("dst = ", dst)

	my1 := &MyMsg{}
	proto.Unmarshal(dst, my1)
	fmt.Println("my1 = ", my1)
	fmt.Println("my1.Data = ", my1.Data)

	proto.Unmarshal(dst[0:4], my1)
	fmt.Println("my1 = ", my1)

	fm := FrameMgr{}
	fm.recvid = 4
	fm.windowsize = 100
	lr := &Frame{}
	rr := &Frame{}
	lr.Id = 1
	rr.Id = 4
	fmt.Println("fm.compareId(lr, rr)  = ", fm.compareId((int)(lr.Id), (int)(rr.Id)))

	lr.Id = 99
	rr.Id = 8
	fmt.Println("fm.compareId(lr, rr)  = ", fm.compareId((int)(lr.Id), (int)(rr.Id)))

	fm.recvid = 9000
	lr.Id = 9998
	rr.Id = 9999
	fmt.Println("fm.compareId(lr, rr)  = ", fm.compareId((int)(lr.Id), (int)(rr.Id)))

	fm.recvid = 9000
	lr.Id = 9998
	rr.Id = 8
	fmt.Println("fm.compareId(lr, rr)  = ", fm.compareId((int)(lr.Id), (int)(rr.Id)))

	fm.recvid = 0
	lr.Id = 9998
	rr.Id = 8
	fmt.Println("fm.compareId(lr, rr)  = ", fm.compareId((int)(lr.Id), (int)(rr.Id)))

	fm.recvid = 0
	fm.windowsize = 5
	fmt.Println("fm.isIdInRange  = ", fm.isIdInRange(4, 10))

	fm.recvid = 0
	fm.windowsize = 5
	fmt.Println("fm.isIdInRange  = ", fm.isIdInRange(5, 10))

	fm.recvid = 4
	fm.windowsize = 5
	fmt.Println("fm.isIdInRange  = ", fm.isIdInRange(1, 10))

	fm.recvid = 7
	fm.windowsize = 5
	fmt.Println("fm.isIdInRange  = ", fm.isIdInRange(1, 10))

	fm.recvid = 7
	fm.windowsize = 5
	fmt.Println("fm.isIdInRange  = ", fm.isIdInRange(2, 10))

	fm.recvid = 7
	fm.windowsize = 5
	fmt.Println("fm.isIdInRange  = ", fm.isIdInRange(9, 10))

	fm.recvid = 10
	fm.windowsize = 10000
	fmt.Println("fm.isIdInRange  = ", fm.isIdInRange(0, FRAME_MAX_ID))

	fm.recvid = 7
	fm.windowsize = 5
	fmt.Println("fm.isIdOld  = ", fm.isIdOld(2, 10))

	fm.recvid = 7
	fm.windowsize = 5
	fmt.Println("fm.isIdOld  = ", fm.isIdOld(1, 10))

	fm.recvid = 3
	fm.windowsize = 5
	fmt.Println("fm.isIdOld  = ", fm.isIdOld(1, 10))

	fm.recvid = 13
	fm.windowsize = 10000
	fmt.Println("fm.isIdOld  = ", fm.isIdOld(9, FRAME_MAX_ID))
}
