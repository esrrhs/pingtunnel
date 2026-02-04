//go:build !android

package pingtunnel

import "golang.org/x/net/icmp"

func listenICMP(addr string) (*icmp.PacketConn, error) {
	setICMPDatagram(false)
	return icmp.ListenPacket("ip4:icmp", addr)
}
