package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runningAsRoot would let the raw ICMP listeners in the client/server run
// paths succeed and block forever, so the guarded subtests below skip.
// (Geteuid returns -1 on platforms without the concept.)
func runningAsRoot() bool {
	return os.Geteuid() == 0
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	os.Stdout = old
	_ = w.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, rerr := r.Read(buf)
		sb.Write(buf[:n])
		if rerr != nil {
			break
		}
	}
	return sb.String()
}

func TestRunShowVersion(t *testing.T) {
	out := captureStdout(t, func() {
		if code := run([]string{"-v"}); code != 0 {
			t.Errorf("run(-v) exit = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "pingtunnel") {
		t.Errorf("run(-v) output %q missing version banner", out)
	}

	out = captureStdout(t, func() {
		if code := run([]string{"--version"}); code != 0 {
			t.Errorf("run(--version) exit = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "pingtunnel") {
		t.Errorf("run(--version) output %q missing version banner", out)
	}
}

func TestRunUnknownFlagExits(t *testing.T) {
	if code := run([]string{"-no-such-flag"}); code != 2 {
		t.Errorf("run with unknown flag exit = %d, want 2", code)
	}
}

func TestRunUsageReturnsZero(t *testing.T) {
	cases := [][]string{
		{},
		{"-type", "bogus"},
		{"-type", "client", "-l", ":0"},
		{"-type", "client", "-s", "1.2.3.4"},
		{"-type", "client", "-l", ":0", "-s", "1.2.3.4"},
		{"nosuchfile.json"},
	}
	for _, args := range cases {
		var code int
		captureStdout(t, func() {
			code = run(args)
		})
		if code != 0 {
			t.Errorf("run(%v) exit = %d, want 0", args, code)
		}
	}
}

func TestRunValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "password too short",
			args: []string{"-type", "server", "-password", "short"},
			want: []string{"Invalid password"},
		},
		{
			name: "bad encryption mode",
			args: []string{"-type", "server", "-password", "12345678", "-encrypt", "bogus"},
			want: []string{"Invalid encryption mode"},
		},
		{
			name: "encryption cannot be disabled",
			args: []string{"-type", "server", "-password", "12345678", "-encrypt", "none"},
			want: []string{"Encryption cannot be disabled"},
		},
		{
			name: "bad forward url",
			args: []string{"-type", "server", "-password", "12345678", "-forward", "bad://host:1"},
			want: []string{"Invalid forward URL"},
		},
		{
			name: "client bad listen addr",
			args: []string{"-type", "client", "-l", "not-an-addr", "-s", "127.0.0.1", "-t", "127.0.0.1:80", "-password", "12345678", "-nolog"},
			want: nil,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var code int
			out := captureStdout(t, func() {
				code = run(tc.args)
			})
			if code != 0 {
				t.Errorf("run(%v) exit = %d, want 0", tc.args, code)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("run(%v) output %q missing %q", tc.args, out, want)
				}
			}
		})
	}
}

func TestRunConfigFileErrors(t *testing.T) {
	dir := t.TempDir()

	badJSON := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badJSON, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var code int
	out := captureStdout(t, func() {
		code = run([]string{"-c", badJSON})
	})
	if code != 0 {
		t.Errorf("bad json config exit = %d, want 0", code)
	}
	if !strings.Contains(out, "Failed to load config file") {
		t.Errorf("output %q missing load failure message", out)
	}

	legacy := filepath.Join(dir, "legacy.json")
	if err := os.WriteFile(legacy, []byte(`{"key":"abc"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() {
		code = run([]string{"--config", legacy})
	})
	if code != 0 {
		t.Errorf("legacy config exit = %d, want 0", code)
	}
	if !strings.Contains(out, "no longer supported") {
		t.Errorf("output %q missing legacy field hint", out)
	}

	// Positional form: pingtunnel <config.json>
	out = captureStdout(t, func() {
		code = run([]string{badJSON})
	})
	if code != 0 {
		t.Errorf("positional config exit = %d, want 0", code)
	}
	if !strings.Contains(out, "Failed to load config file") {
		t.Errorf("output %q missing load failure message", out)
	}
}

// TestRunClientRunError covers the full client startup path ending in the
// ICMP listener error every unprivileged process hits; skipped under root,
// where Run would instead block serving forever.
func TestRunClientRunError(t *testing.T) {
	if runningAsRoot() {
		t.Skip("raw ICMP listener succeeds under root; run would block")
	}
	var code int
	captureStdout(t, func() {
		code = run([]string{
			"-type", "client", "-l", "127.0.0.1:0", "-s", "127.0.0.1",
			"-t", "127.0.0.1:9", "-password", "12345678", "-nolog", "-quiet",
		})
	})
	if code != 0 {
		t.Errorf("client run error exit = %d, want 0", code)
	}
}

// TestRunServerRunError covers the server startup path ending in the ICMP
// listener error every unprivileged process hits.
func TestRunServerRunError(t *testing.T) {
	if runningAsRoot() {
		t.Skip("raw ICMP listener succeeds under root; run would block")
	}
	var code int
	captureStdout(t, func() {
		code = run([]string{
			"-type", "server", "-password", "12345678", "-nolog", "-quiet",
			"-icmp_l", "127.0.0.1",
		})
	})
	if code != 0 {
		t.Errorf("server run error exit = %d, want 0", code)
	}
}

func TestRunHelpExitsZero(t *testing.T) {
	var code int
	out := captureStdout(t, func() {
		code = run([]string{"-h"})
	})
	if code != 0 {
		t.Errorf("run(-h) exit = %d, want 0", code)
	}
	if !strings.Contains(out, "Usage") {
		t.Errorf("run(-h) output missing usage: %q", out[:min(len(out), 200)])
	}
}
