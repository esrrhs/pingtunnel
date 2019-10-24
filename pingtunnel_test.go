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
	my.Data = make([]byte, 3)
	dst, _ := proto.Marshal(my)
	fmt.Println("dst = ", dst)

	my1 := &MyMsg{}
	proto.Unmarshal(dst, my1)
	fmt.Println("my1 = ", my1)

	proto.Unmarshal(dst[0:4], my1)
	fmt.Println("my1 = ", my1)

	fm := FrameMgr{}
	fm.recvid = 0
	fm.windowsize = 100
	lr := &Frame{}
	rr := &Frame{}
	lr.Id = 1
	rr.Id = 2
	fmt.Println("fm.compareId(lr, rr)  = ", fm.compareId(lr, rr))

	lr.Id = 99
	rr.Id = 8
	fmt.Println("fm.compareId(lr, rr)  = ", fm.compareId(lr, rr))

	fm.recvid = 9000
	lr.Id = 9998
	rr.Id = 9999
	fmt.Println("fm.compareId(lr, rr)  = ", fm.compareId(lr, rr))

	fm.recvid = 9000
	lr.Id = 9998
	rr.Id = 8
	fmt.Println("fm.compareId(lr, rr)  = ", fm.compareId(lr, rr))

	fm.recvid = 0
	lr.Id = 9998
	rr.Id = 8
	fmt.Println("fm.compareId(lr, rr)  = ", fm.compareId(lr, rr))
}
