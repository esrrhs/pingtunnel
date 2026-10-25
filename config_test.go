package pingtunnel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	return path
}

func TestLoadConfig(t *testing.T) {
	configPath := writeConfig(t, `{
		"type": "client",
		"listen": ":4455",
		"server": "1.2.3.4",
		"socks5": true,
		"udp": true,
		"password": "correct-horse-battery",
		"encrypt": "aes256",
		"congestion": "bb",
		"loglevel": "debug",
		"quiet": true,
		"nolog": true
	}`)

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Type != "client" {
		t.Errorf("expected type client, got %s", cfg.Type)
	}
	if cfg.Listen != ":4455" {
		t.Errorf("expected listen :4455, got %s", cfg.Listen)
	}
	if cfg.Server != "1.2.3.4" {
		t.Errorf("expected server 1.2.3.4, got %s", cfg.Server)
	}
	if !cfg.Socks5 {
		t.Errorf("expected socks5 true, got %v", cfg.Socks5)
	}
	if !cfg.UDP {
		t.Errorf("expected udp true, got %v", cfg.UDP)
	}
	if cfg.Password != "correct-horse-battery" {
		t.Errorf("expected password, got %s", cfg.Password)
	}
	if cfg.Encrypt != "aes256" {
		t.Errorf("expected encrypt aes256, got %s", cfg.Encrypt)
	}
	if cfg.Congestion != "bb" {
		t.Errorf("expected congestion bb, got %s", cfg.Congestion)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected loglevel debug, got %s", cfg.LogLevel)
	}
	if !cfg.Quiet {
		t.Errorf("expected quiet true, got %v", cfg.Quiet)
	}
	if !cfg.NoLog {
		t.Errorf("expected nolog true, got %v", cfg.NoLog)
	}
}

func TestLoadConfig_RejectsLegacyFields(t *testing.T) {
	cases := map[string]string{
		"key":         `"key": 123456`,
		"encrypt_key": `"encrypt_key": "secret"`,
		"sock5":       `"sock5": 1`,
		"tcp":         `"tcp": 1`,
		"tcp_bs":      `"tcp_bs": 1048576`,
		"tcp_mw":      `"tcp_mw": 20000`,
		"tcp_rst":     `"tcp_rst": 400`,
		"tcp_gz":      `"tcp_gz": 1024`,
		"tcp_stat":    `"tcp_stat": 1`,
		"maxprt":      `"maxprt": 100`,
		"maxprb":      `"maxprb": 1000`,
		"noprint":     `"noprint": 1`,
	}
	for field, snippet := range cases {
		jsonContent := `{"type": "client", ` + snippet + `}`
		configPath := writeConfig(t, jsonContent)
		_, err := LoadConfig(configPath)
		if err == nil {
			t.Fatalf("legacy field %q accepted", field)
		}
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("error for %q should mention the field, got: %v", field, err)
		}
	}
}

func TestLoadConfig_NonExistent(t *testing.T) {
	_, err := LoadConfig("non_existent_file.json")
	if err == nil {
		t.Fatalf("expected error for non-existent file, got nil")
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	configPath := writeConfig(t, `{invalid`)
	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatalf("expected error for invalid json, got nil")
	}
}
