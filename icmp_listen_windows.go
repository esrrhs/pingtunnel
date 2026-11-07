//go:build windows

package pingtunnel

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"unsafe"

	"github.com/esrrhs/gohome/loggo"
	"golang.org/x/net/icmp"
)

const (
	// SIO_RCVALL = _WSAIOW(IOC_VENDOR, 1)
	sioRCVALL = syscall.IOC_IN | syscall.IOC_VENDOR | 1
	// RCVALL_IPLEVEL receives all IPv4 packets for the bound interface
	// without putting the NIC into promiscuous mode.
	rcvallIPLevel = 3
)

// listenICMP is the client path: a normal raw ICMP socket. The client only
// needs to receive Echo Replies to its own requests, which Windows delivers
// reliably; SIO_RCVALL is not required and would force a concrete bind IP.
func listenICMP(addr string) (icmpPacketConn, error) {
	setICMPDatagram(false)
	return icmp.ListenPacket("ip4:icmp", addr)
}

// listenICMPServer opens the server-side Windows raw ICMP socket.
//
// Windows' default raw-socket delivery for a wildcard (0.0.0.0) bind is
// unreliable: inbound Echo Requests are often not delivered at all
// (golang/go#38427, golang/go#33117). Binding a concrete local IP and
// enabling SIO_RCVALL at the IP level makes the socket receive them;
// go-gost/x and Safing Portmaster use the same workaround in production.
//
// When -icmp_l is left at 0.0.0.0 we pick the source address the routing
// table would use for an external destination. A multi-homed server should
// set -icmp_l to the address clients actually target.
func listenICMPServer(addr string) (icmpPacketConn, error) {
	setICMPDatagram(false)

	bind, err := windowsBindAddr(addr)
	if err != nil {
		return nil, err
	}

	pc, err := net.ListenPacket("ip4:icmp", bind)
	if err != nil {
		return nil, err
	}
	sc, ok := pc.(syscall.Conn)
	if !ok {
		loggo.Error("windows icmp: packet conn %T does not expose SyscallConn (falling back to plain raw socket)", pc)
		return pc, nil
	}
	rawConn, err := sc.SyscallConn()
	if err != nil {
		loggo.Error("windows icmp: SyscallConn on %s failed: %s (falling back to plain raw socket)", bind, err)
		return pc, nil
	}

	var ioctlErr error
	if ctrlErr := rawConn.Control(func(fd uintptr) {
		level := uint32(rcvallIPLevel)
		var returned uint32
		ioctlErr = syscall.WSAIoctl(syscall.Handle(fd), sioRCVALL,
			(*byte)(unsafe.Pointer(&level)), uint32(unsafe.Sizeof(level)),
			nil, 0, &returned, nil, 0)
	}); ctrlErr != nil {
		ioctlErr = ctrlErr
	}
	if ioctlErr != nil {
		// The socket still works as a plain raw socket. Keep it, but make
		// the failure visible: without SIO_RCVALL inbound Echo Requests may
		// never arrive (this is exactly the Windows wildcard-binding bug).
		loggo.Error("windows icmp: WSAIoctl(SIO_RCVALL, IPLEVEL) on %s failed: %s (falling back to plain raw socket)", bind, ioctlErr)
		return pc, nil
	}
	loggo.Info("windows icmp: bound %s with SIO_RCVALL(IPLEVEL)", bind)
	return pc, nil
}

// windowsBindAddr turns a wildcard listen address into a concrete local
// IPv4 address. SIO_RCVALL rejects INADDR_ANY, so a concrete address is
// mandatory. An explicit non-wildcard address is validated rather than
// silently falling back to the default route.
func windowsBindAddr(addr string) (string, error) {
	if addr != "" {
		ip := net.ParseIP(addr)
		if ip == nil {
			resolved, err := net.ResolveIPAddr("ip4", addr)
			if err != nil {
				return "", fmt.Errorf("windows icmp: invalid listen address %q: %w", addr, err)
			}
			ip = resolved.IP
		}
		if !ip.IsUnspecified() {
			ip4 := ip.To4()
			if ip4 == nil {
				return "", fmt.Errorf("windows icmp: listen address %q is not an IPv4 address", addr)
			}
			return ip4.String(), nil
		}
	}

	// UDP dial does not send a packet; it only asks the routing table for
	// the local source address of the default route.
	if conn, err := net.Dial("udp4", "8.8.8.8:53"); err == nil {
		la, ok := conn.LocalAddr().(*net.UDPAddr)
		_ = conn.Close()
		if ok && la.IP != nil && !la.IP.IsUnspecified() && !la.IP.IsLoopback() {
			return la.IP.String(), nil
		}
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("windows icmp: cannot enumerate interfaces: %w", err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ip4 := ipnet.IP.To4(); ip4 != nil && !ip4.IsLoopback() {
				return ip4.String(), nil
			}
		}
	}
	return "", errors.New("windows icmp: no non-loopback IPv4 address found; set -icmp_l to the local IP clients target")
}
