package main

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	// Drain concurrently: fn's output can exceed the OS pipe buffer (the
	// usage text does on Windows, whose default is ~4KB), which would block
	// the write side forever if nothing read until fn returned.
	out := make(chan []byte, 1)
	go func() {
		var sb bytes.Buffer
		buf := make([]byte, 4096)
		for {
			n, rerr := r.Read(buf)
			sb.Write(buf[:n])
			if rerr != nil {
				break
			}
		}
		out <- sb.Bytes()
	}()

	fn()
	os.Stdout = old
	_ = w.Close()
	return string(<-out)
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

// TestRunClientRunError covers the client startup path. Unprivileged
// processes end it at the ICMP listener error; privileged ones (root on
// Linux, the admin user on Windows runners) get a serving client, so the
// pre-closed stop channel makes both shapes return promptly — one full
// startup plus graceful shutdown either way.
func TestRunClientRunError(t *testing.T) {
	var code int
	captureStdout(t, func() {
		code = run([]string{
			"-type", "client", "-l", "127.0.0.1:0", "-s", "127.0.0.1",
			"-t", "127.0.0.1:9", "-password", "12345678", "-nolog", "-quiet",
		}, closedStop())
	})
	if code != 0 {
		t.Errorf("client run exit = %d, want 0", code)
	}
}

// TestRunServerRunError covers the server startup path — ICMP listener
// error when unprivileged, serve-until-shutdown (via the closed stop
// channel) when privileged.
func TestRunServerRunError(t *testing.T) {
	var code int
	captureStdout(t, func() {
		code = run([]string{
			"-type", "server", "-password", "12345678", "-nolog", "-quiet",
			"-icmp_l", "127.0.0.1",
		}, closedStop())
	})
	if code != 0 {
		t.Errorf("server run exit = %d, want 0", code)
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

// closedStop returns an already-closed channel: serveAndStop sees shutdown
// as immediately requested, which lets the tests below drive the serve loop
// without signals or real ICMP listeners.
func closedStop() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// TestRunConfigFileMerge covers a fully-populated config file: every
// cfg.* value must land in the corresponding flag unless it was set on the
// command line. The closed stop channel makes the run return instead of
// blocking where the process would serve (root) or fail the ICMP listener
// (unprivileged), so no subtest can hang.
func TestRunConfigFileMerge(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "full.json")
	cfgJSON := `{"type":"server","listen":"127.0.0.1:14450","target":"127.0.0.1:80","server":"127.0.0.1",` +
		`"icmp_listen":"127.0.0.1","timeout":30,"password":"12345678","encrypt":"aes128","udp":true,` +
		`"congestion":"bb","nolog":true,"quiet":true,"loglevel":"warn","socks5":true,"s5user":"u",` +
		`"s5pass":"p","maxconn":10,"profile":65530,"conntt":3000,"forward":"socks5://127.0.0.1:1080"}`
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	var code int
	captureStdout(t, func() {
		code = run([]string{"-c", cfgPath}, closedStop())
	})
	if code != 0 {
		t.Errorf("run with valid full config exit = %d, want 0", code)
	}
}

// TestRunAutoLoadConfig covers the positional "pingtunnel config.json"
// auto-detection: with no flags at all, a config.json in the working
// directory is loaded.
func TestRunAutoLoadConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("config.json", []byte(
		`{"type":"server","password":"12345678","icmp_listen":"127.0.0.1","nolog":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var code int
	captureStdout(t, func() {
		code = run(nil, closedStop())
	})
	if code != 0 {
		t.Errorf("run with auto-loaded config.json exit = %d, want 0", code)
	}
}

func TestServeAndStopCallsStopFn(t *testing.T) {
	stopped := false
	code := serveAndStop(0, closedStop(), func() { stopped = true })
	if code != 0 {
		t.Errorf("serveAndStop exit = %d, want 0", code)
	}
	if !stopped {
		t.Error("stopFn not called after shutdown")
	}
}

func TestServeAndStopNilStopFn(t *testing.T) {
	if code := serveAndStop(0, closedStop(), nil); code != 0 {
		t.Errorf("serveAndStop with nil stopFn exit = %d, want 0", code)
	}
}

// TestServeAndStopProfileServes drives the -profile branch: the pprof
// listener must actually answer on the chosen port before shutdown.
func TestServeAndStopProfileServes(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	stopped := false
	code := serveAndStop(port, closedStop(), func() { stopped = true })
	if code != 0 {
		t.Errorf("serveAndStop exit = %d, want 0", code)
	}
	if !stopped {
		t.Error("stopFn not called after shutdown")
	}

	var served bool
	for i := 0; i < 100 && !served; i++ {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/debug/pprof/", port))
		if err == nil {
			resp.Body.Close()
			served = resp.StatusCode == http.StatusOK
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !served {
		t.Errorf("profile listener on 127.0.0.1:%d never served /debug/pprof/", port)
	}
}
