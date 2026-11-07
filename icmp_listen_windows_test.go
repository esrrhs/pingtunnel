//go:build windows

package pingtunnel

import (
	"net"
	"testing"
)

func TestWindowsBindAddrKeepsConcreteIPv4(t *testing.T) {
	for _, want := range []string{"10.1.2.3", "127.0.0.1"} {
		got, err := windowsBindAddr(want)
		if err != nil {
			t.Fatalf("windowsBindAddr(%q): %v", want, err)
		}
		if got != want {
			t.Fatalf("windowsBindAddr(%q) = %q, want %s", want, got, want)
		}
	}
}

func TestWindowsBindAddrRejectsInvalidExplicitAddr(t *testing.T) {
	for _, bad := range []string{"::1", "2001:db8::1", "999.999.999.999", "nosuch-host.invalid"} {
		if got, err := windowsBindAddr(bad); err == nil {
			t.Fatalf("windowsBindAddr(%q) = %q, want error", bad, got)
		}
	}
}

func TestWindowsBindAddrResolvesWildcard(t *testing.T) {
	for _, wildcard := range []string{"0.0.0.0", ""} {
		got, err := windowsBindAddr(wildcard)
		if err != nil {
			t.Skipf("cannot resolve a concrete IPv4 in this environment: %v", err)
		}
		ip := net.ParseIP(got)
		if ip == nil || ip.To4() == nil || ip.IsUnspecified() {
			t.Fatalf("windowsBindAddr(%q) = %q, want a concrete IPv4", wildcard, got)
		}
	}
}
