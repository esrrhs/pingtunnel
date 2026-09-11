package pingtunnel

import (
	"testing"
	"time"

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

func TestClientNextPingInterval(t *testing.T) {
	client := &Client{}
	now := time.Now()

	// 1. When idle and no activity: interval should be 10s
	client.lastActivityUnixNano.Store(now.Add(-60 * time.Second).UnixNano())
	if interval := client.nextPingInterval(now); interval != 10*time.Second {
		t.Fatalf("expected 10s interval for cold/idle client, got %v", interval)
	}

	// 2. When warm activity (within 30s): interval should be 3s
	client.lastActivityUnixNano.Store(now.Add(-15 * time.Second).UnixNano())
	if interval := client.nextPingInterval(now); interval != 3*time.Second {
		t.Fatalf("expected 3s interval for warm client, got %v", interval)
	}

	// 3. When hot activity (within 5s): interval should be 1s
	client.lastActivityUnixNano.Store(now.Add(-2 * time.Second).UnixNano())
	if interval := client.nextPingInterval(now); interval != time.Second {
		t.Fatalf("expected 1s interval for hot client, got %v", interval)
	}

	// 4. When has active connections: interval should always be 1s regardless of lastActivity
	client.lastActivityUnixNano.Store(now.Add(-100 * time.Second).UnixNano())
	client.localIdToConnMap.Store("conn-1", &ClientConn{})
	if interval := client.nextPingInterval(now); interval != time.Second {
		t.Fatalf("expected 1s interval when active connection exists, got %v", interval)
	}
}


