package pingtunnel

import "net"

var icmpDatagram bool

func setICMPDatagram(enabled bool) {
	icmpDatagram = enabled
}

func icmpDstAddr(ip *net.IPAddr) net.Addr {
	if icmpDatagram {
		return &net.UDPAddr{IP: ip.IP}
	}
	return ip
}

func icmpSrcToIPAddr(addr net.Addr) *net.IPAddr {
	switch v := addr.(type) {
	case *net.IPAddr:
		return v
	case *net.UDPAddr:
		return &net.IPAddr{IP: v.IP, Zone: v.Zone}
	default:
		return &net.IPAddr{IP: net.IPv4zero}
	}
}
