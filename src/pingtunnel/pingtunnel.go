package pingtunnel

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"io"
)

type MSGID int

const (
	REGISTER         MSGID = 1
)

const (
	protocolICMP     = 1
)

type Msg struct {
	TYPE int
	ID   string // identifier
	Data []byte // data
}

func (p *Msg) Len(proto int) int {
	if p == nil {
		return 0
	}
	return 4 + 32 + len(p.Data)
}

func (p *Msg) Marshal(proto int) ([]byte, error) {
	b := make([]byte, p.Len(proto))
	binary.BigEndian.PutUint32(b, uint32(p.TYPE))
	copy(b[4:], p.ID)
	copy(b[4+32:], p.Data)
	return b, nil
}

func UniqueId() string {
	b := make([]byte, 48)

	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return ""
	}
	return GetMd5String(base64.URLEncoding.EncodeToString(b))
}

func GetMd5String(s string) string {
	h := md5.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

type RegisterData struct {
	localaddr string
}
