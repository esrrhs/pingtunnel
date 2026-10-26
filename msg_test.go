package pingtunnel

import (
	"strings"
	"testing"
)

// TestMyMsgGetters pins the generated accessors on both a fully populated
// message and a nil receiver (protobuf nil getters return zero values).
func TestMyMsgGetters(t *testing.T) {
	data := []byte{1, 2, 3}
	m := &MyMsg{
		Id:                  "conn-1",
		Type:                int32(MyMsg_DATA),
		Target:              "127.0.0.1:80",
		Data:                data,
		Rproto:              8,
		Magic:               int32(MyMsg_MAGIC),
		Key:                 12345,
		Timeout:             60,
		Tcpmode:             1,
		TcpmodeBuffersize:   1024,
		TcpmodeMaxwin:       2048,
		TcpmodeResendTimems: 400,
		TcpmodeCompress:     1,
		TcpmodeStat:         1,
	}

	if m.GetId() != "conn-1" {
		t.Errorf("GetId = %q", m.GetId())
	}
	if m.GetType() != int32(MyMsg_DATA) {
		t.Errorf("GetType = %d", m.GetType())
	}
	if m.GetTarget() != "127.0.0.1:80" {
		t.Errorf("GetTarget = %q", m.GetTarget())
	}
	if string(m.GetData()) != string(data) {
		t.Errorf("GetData = %v", m.GetData())
	}
	if m.GetRproto() != 8 {
		t.Errorf("GetRproto = %d", m.GetRproto())
	}
	if m.GetMagic() != int32(MyMsg_MAGIC) {
		t.Errorf("GetMagic = %d", m.GetMagic())
	}
	if m.GetKey() != 12345 {
		t.Errorf("GetKey = %d", m.GetKey())
	}
	if m.GetTimeout() != 60 {
		t.Errorf("GetTimeout = %d", m.GetTimeout())
	}
	if m.GetTcpmode() != 1 {
		t.Errorf("GetTcpmode = %d", m.GetTcpmode())
	}
	if m.GetTcpmodeBuffersize() != 1024 {
		t.Errorf("GetTcpmodeBuffersize = %d", m.GetTcpmodeBuffersize())
	}
	if m.GetTcpmodeMaxwin() != 2048 {
		t.Errorf("GetTcpmodeMaxwin = %d", m.GetTcpmodeMaxwin())
	}
	if m.GetTcpmodeResendTimems() != 400 {
		t.Errorf("GetTcpmodeResendTimems = %d", m.GetTcpmodeResendTimems())
	}
	if m.GetTcpmodeCompress() != 1 {
		t.Errorf("GetTcpmodeCompress = %d", m.GetTcpmodeCompress())
	}
	if m.GetTcpmodeStat() != 1 {
		t.Errorf("GetTcpmodeStat = %d", m.GetTcpmodeStat())
	}

	var nilMsg *MyMsg
	if nilMsg.GetId() != "" || nilMsg.GetType() != 0 || nilMsg.GetTarget() != "" ||
		len(nilMsg.GetData()) != 0 || nilMsg.GetRproto() != 0 || nilMsg.GetMagic() != 0 ||
		nilMsg.GetKey() != 0 || nilMsg.GetTimeout() != 0 || nilMsg.GetTcpmode() != 0 ||
		nilMsg.GetTcpmodeBuffersize() != 0 || nilMsg.GetTcpmodeMaxwin() != 0 ||
		nilMsg.GetTcpmodeResendTimems() != 0 || nilMsg.GetTcpmodeCompress() != 0 ||
		nilMsg.GetTcpmodeStat() != 0 {
		t.Errorf("nil getters must return zero values")
	}

	if !strings.Contains(m.String(), "conn-1") {
		t.Errorf("String() = %q, want it to contain the conn id", m.String())
	}
	m.ProtoMessage()

	if !strings.Contains(m.String(), "conn-1") {
		t.Errorf("String() = %q, want it to contain the conn id", m.String())
	}
	if desc, _ := m.Descriptor(); len(desc) == 0 {
		t.Errorf("Descriptor() must return non-empty raw desc")
	}
}

// TestMyMsgTypeEnum covers the generated enum wrapper methods.
func TestMyMsgTypeEnum(t *testing.T) {
	if got := MyMsg_PING.String(); got != "PING" {
		t.Errorf("MyMsg_PING.String() = %q", got)
	}
	if got := MyMsg_DATA.Enum().String(); got != "DATA" {
		t.Errorf("MyMsg_DATA.Enum().String() = %q", got)
	}
	if got := MyMsg_PING.Type(); got == nil {
		t.Errorf("Type() = nil")
	}
	if got := MyMsg_KICK.Number(); got != 2 {
		t.Errorf("MyMsg_KICK.Number() = %d", got)
	}
	if MyMsg_TYPE(99).Enum() == nil {
		t.Errorf("Enum() on any value must be non-nil")
	}
	if MyMsg_TYPE(0).Descriptor() == nil {
		t.Errorf("MyMsg_TYPE Descriptor() = nil")
	}
	if desc, path := MyMsg_TYPE(0).EnumDescriptor(); len(desc) == 0 || len(path) == 0 {
		t.Errorf("MyMsg_TYPE EnumDescriptor() empty")
	}
}
