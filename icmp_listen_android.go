//go:build android

package pingtunnel

import "golang.org/x/net/icmp"

func listenICMPServer(addr string) (icmpPacketConn, error) {
	return listenICMP(addr)
}

func listenICMP(addr string) (icmpPacketConn, error) {
	// Try unprivileged ICMP socket on Android.
	if conn, err := icmp.ListenPacket("udp4", addr); err == nil {
		setICMPDatagram(true)
		return conn, nil
	}
	setICMPDatagram(false)
	// Fallback to raw ICMP (will require CAP_NET_RAW).
	return icmp.ListenPacket("ip4:icmp", addr)
}
