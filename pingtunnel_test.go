package pingtunnel

import (
	"testing"

	"google.golang.org/protobuf/proto"
)

func Test0001(t *testing.T) {

	my := &MyMsg{}
	my.Id = "12345"
	my.Target = "111:11"
	my.Type = 12
	my.Data = make([]byte, 0)
	dst, err := proto.Marshal(my)
	if err != nil {
		t.Fatalf("proto.Marshal failed: %v", err)
	}

	my1 := &MyMsg{}
	err = proto.Unmarshal(dst, my1)
	if err != nil {
		t.Fatalf("proto.Unmarshal failed: %v", err)
	}
	if my1.Id != my.Id || my1.Target != my.Target || my1.Type != my.Type {
		t.Fatalf("unmarshaled message mismatch: got %+v, want %+v", my1, my)
	}
}

func TestMyMsgRoundTripWithPayload(t *testing.T) {
	original := &MyMsg{
		Id:                  "conn-9876",
		Type:                int32(MyMsg_DATA),
		Target:              "10.0.0.1:8080",
		Data:                []byte("hello pingtunnel"),
		Rproto:              0,
		Magic:               int32(MyMsg_MAGIC),
		Key:                 123456,
		Timeout:             60,
		Tcpmode:             1,
		TcpmodeBuffersize:   1024,
		TcpmodeMaxwin:       100,
		TcpmodeResendTimems: 200,
		TcpmodeCompress:     0,
		TcpmodeStat:         1,
	}

	data, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	decoded := &MyMsg{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Magic != int32(MyMsg_MAGIC) {
		t.Fatalf("expected MAGIC %x, got %x", MyMsg_MAGIC, decoded.Magic)
	}
	if string(decoded.Data) != "hello pingtunnel" {
		t.Fatalf("payload mismatch: %s", string(decoded.Data))
	}
	if decoded.Id != original.Id || decoded.Target != original.Target {
		t.Fatalf("fields mismatch: got %+v, want %+v", decoded, original)
	}
}

func TestMyMsgCorruptedUnmarshal(t *testing.T) {
	corrupted := []byte{0xff, 0xff, 0xff}
	msg := &MyMsg{}
	if err := proto.Unmarshal(corrupted, msg); err == nil {
		t.Fatalf("expected error unmarshaling corrupted data, got nil")
	}
}

