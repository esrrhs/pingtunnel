package pingtunnel

import (
	"fmt"
	"github.com/esrrhs/pingtunnel"
	"testing"
)

func Test0001(test *testing.T) {

	my := &pingtunnel.MyMsg{
	}
	my.ID = "12345"
	my.TARGET = "111:11"
	my.TYPE = 12
	my.Data = make([]byte, 3)
	dst,_ := my.Marshal(0)
	fmt.Println("dst = ", dst)


	my1 := &pingtunnel.MyMsg{
	}
	my1.Unmarshal(dst)
	fmt.Println("my1 = ", my1)

	my1.Unmarshal(dst[0:4])
	fmt.Println("my1 = ", my1)
}
