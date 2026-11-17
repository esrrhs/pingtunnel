//go:build !android && !windows

package pingtunnel

import (
	"net"
	"runtime"

	"golang.org/x/net/icmp"
)

func listenICMP(addr string) (icmpPacketConn, error) {
	setICMPDatagram(false)
	if runtime.GOOS == "linux" {
		c, err := net.ListenPacket("ip4:icmp", addr)
		if err != nil {
			return nil, err
		}
		if ipConn, ok := c.(*net.IPConn); ok {
			_ = ipConn.SetReadBuffer(8 * 1024 * 1024)
			_ = ipConn.SetWriteBuffer(8 * 1024 * 1024)
		}
		return c, nil
	}
	return icmp.ListenPacket("ip4:icmp", addr)
}

func listenICMPServer(addr string) (icmpPacketConn, error) {
	return listenICMP(addr)
}
