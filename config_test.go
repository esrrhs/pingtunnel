package pingtunnel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	jsonContent := `{
		"type": "client",
		"listen": ":4455",
		"server": "1.2.3.4",
		"sock5": 1,
		"key": 123456,
		"encrypt": "aes128",
		"encrypt_key": "secretkey",
		"tcp_bs": 2097152,
		"loglevel": "debug"
	}`

	if err := os.WriteFile(configPath, []byte(jsonContent), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

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
	if cfg.Sock5 != 1 {
		t.Errorf("expected sock5 1, got %d", cfg.Sock5)
	}
	if cfg.Key != 123456 {
		t.Errorf("expected key 123456, got %d", cfg.Key)
	}
	if cfg.Encrypt != "aes128" {
		t.Errorf("expected encrypt aes128, got %s", cfg.Encrypt)
	}
	if cfg.TCPBufferSize != 2097152 {
		t.Errorf("expected tcp_bs 2097152, got %d", cfg.TCPBufferSize)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected loglevel debug, got %s", cfg.LogLevel)
	}
}

func TestLoadConfig_NonExistent(t *testing.T) {
	_, err := LoadConfig("non_existent_file.json")
	if err == nil {
		t.Fatalf("expected error for non-existent file, got nil")
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "invalid.json")
	if err := os.WriteFile(configPath, []byte(`{invalid`), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatalf("expected error for invalid json, got nil")
	}
}
