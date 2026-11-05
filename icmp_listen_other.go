//go:build !android && !windows

package pingtunnel

import "golang.org/x/net/icmp"

func listenICMP(addr string) (icmpPacketConn, error) {
	setICMPDatagram(false)
	return icmp.ListenPacket("ip4:icmp", addr)
}

func listenICMPServer(addr string) (icmpPacketConn, error) {
	return listenICMP(addr)
}
