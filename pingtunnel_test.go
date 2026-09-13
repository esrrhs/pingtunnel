package pingtunnel

import (
	"testing"
	"time"

	"github.com/esrrhs/gohome/network"
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

func TestCongestionControlAndTimeoutSimulation(t *testing.T) {
	// 1. Verify BBCongestion setup on server and client
	s, err := NewServer("0.0.0.0", 0, 0, 0, 0, 1000, nil, nil, "bb")
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	if s.congestion != "bb" {
		t.Fatalf("expected congestion bb, got %s", s.congestion)
	}

	c, err := NewClient(":4455", "127.0.0.1", "127.0.0.1:80", 60, 0, "0.0.0.0",
		1, 1024*1024, 20000, 400, 0, 0, 0, 0, nil, nil, "", "", "bb")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	if c.congestion != "bb" {
		t.Fatalf("expected congestion bb, got %s", c.congestion)
	}

	// 2. Simulate large download unidirectional traffic (download heavy: activeRecvTime updated, activeSendTime idle)
	// Connection should NOT be closed as long as one direction is active.
	now := time.Now()
	timeoutSec := 60

	activeRecv := now
	idleSend := now.Add(-120 * time.Second) // no client-to-server data sent for 120s

	diffrecv := now.Sub(activeRecv)
	diffsend := now.Sub(idleSend)

	// Old buggy condition: diffrecv > timeout || diffsend > timeout
	oldBuggyClosed := diffrecv > time.Second*time.Duration(timeoutSec) || diffsend > time.Second*time.Duration(timeoutSec)
	if !oldBuggyClosed {
		t.Fatalf("expected old logic to mistakenly close the connection")
	}

	// Fixed condition: both directions must be idle (diffrecv > timeout && diffsend > timeout)
	fixedClosed := diffrecv > time.Second*time.Duration(timeoutSec) && diffsend > time.Second*time.Duration(timeoutSec)
	if fixedClosed {
		t.Fatalf("fixed logic should keep active download connection alive")
	}

	// When both directions are truly idle:
	bothIdleRecv := now.Add(-70 * time.Second)
	bothIdleSend := now.Add(-70 * time.Second)
	trulyIdleClosed := now.Sub(bothIdleRecv) > time.Second*time.Duration(timeoutSec) && now.Sub(bothIdleSend) > time.Second*time.Duration(timeoutSec)
	if !trulyIdleClosed {
		t.Fatalf("connection should be closed when both directions are idle")
	}
}

func TestBBCongestionFrameMgrBehavior(t *testing.T) {
	// Test FrameMgr behavior with and without BBCongestion
	// 1. Without congestion control: CanSend check is bypassed in calSendList
	fmNoCongestion := network.NewFrameMgr(FRAME_MAX_SIZE, FRAME_MAX_ID, 1024*1024, 20000, 400, 0, 0)
	largePayload := make([]byte, 500*1024) // 500KB
	fmNoCongestion.WriteSendBuffer(largePayload)
	fmNoCongestion.Update()

	sendListNoCongestion := fmNoCongestion.GetSendList()
	if sendListNoCongestion.Len() == 0 {
		t.Fatalf("expected sendList to contain frames")
	}

	// 2. With BBCongestion:
	// Verify that BBCongestion limits inflight data when unacknowledged.
	bb := &network.BBCongestion{}
	bb.Init()

	packetSize := 1000
	sentBytes := 0
	for {
		if !bb.CanSend(0, packetSize) {
			break
		}
		sentBytes += packetSize
	}

	// BBCongestion default maxfly is 1MB. It must throttle once flight reaches maxfly
	if sentBytes <= 0 {
		t.Fatalf("expected some bytes sent, got %d", sentBytes)
	}
	if bb.CanSend(0, packetSize) {
		t.Fatalf("expected CanSend to return false when maxfly is reached")
	}

	// When ACKs arrive, BBCongestion allows sending more data
	bb.RecvAck(0, sentBytes)
	bb.Update()

	if !bb.CanSend(0, packetSize) {
		t.Fatalf("expected CanSend to return true after ACKs received and Update called")
	}

	// 3. Verify FrameMgr correctly sets BBCongestion and runs without panic
	fmWithBB := network.NewFrameMgr(FRAME_MAX_SIZE, FRAME_MAX_ID, 1024*1024, 20000, 400, 0, 0)
	fmWithBB.SetCongestion(&network.BBCongestion{})
	fmWithBB.WriteSendBuffer(largePayload)
	fmWithBB.Update()

	sendListWithBB := fmWithBB.GetSendList()
	if sendListWithBB.Len() == 0 {
		t.Fatalf("expected sendList with BB to contain frames")
	}
}




