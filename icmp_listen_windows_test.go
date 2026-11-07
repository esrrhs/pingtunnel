//go:build windows

package pingtunnel

import (
	"net"
	"testing"
)

func TestWindowsBindAddrKeepsConcreteIPv4(t *testing.T) {
	got, err := windowsBindAddr("10.1.2.3")
	if err != nil {
		t.Fatalf("windowsBindAddr: %v", err)
	}
	if got != "10.1.2.3" {
		t.Fatalf("windowsBindAddr = %q, want 10.1.2.3", got)
	}
}

func TestWindowsBindAddrResolvesWildcard(t *testing.T) {
	got, err := windowsBindAddr("0.0.0.0")
	if err != nil {
		t.Skipf("cannot resolve a concrete IPv4 in this environment: %v", err)
	}
	ip := net.ParseIP(got)
	if ip == nil || ip.To4() == nil || ip.IsUnspecified() {
		t.Fatalf("windowsBindAddr(0.0.0.0) = %q, want a concrete IPv4", got)
	}
}
