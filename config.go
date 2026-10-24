package pingtunnel

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config represents server or client configuration that can be loaded from a JSON file.
type Config struct {
	Type             string `json:"type,omitempty"`
	Listen           string `json:"listen,omitempty"`
	Target           string `json:"target,omitempty"`
	Server           string `json:"server,omitempty"`
	ICMPListen       string `json:"icmp_listen,omitempty"`
	Timeout          int    `json:"timeout,omitempty"`
	Password         string `json:"password,omitempty"`
	Encrypt          string `json:"encrypt,omitempty"`
	TCPMode          int    `json:"tcp,omitempty"`
	TCPBufferSize    int    `json:"tcp_bs,omitempty"`
	TCPMaxWin        int    `json:"tcp_mw,omitempty"`
	TCPResendTimeMs  int    `json:"tcp_rst,omitempty"`
	TCPCompress      int    `json:"tcp_gz,omitempty"`
	TCPStat          int    `json:"tcp_stat,omitempty"`
	Congestion       string `json:"congestion,omitempty"`
	NoLog            int    `json:"nolog,omitempty"`
	NoPrint          int    `json:"noprint,omitempty"`
	LogLevel         string `json:"loglevel,omitempty"`
	Socks5           bool   `json:"socks5,omitempty"`
	Sock5User        string `json:"s5user,omitempty"`
	Sock5Pass        string `json:"s5pass,omitempty"`
	MaxConn          int    `json:"maxconn,omitempty"`
	MaxProcessThread int    `json:"maxprt,omitempty"`
	MaxProcessBuffer int    `json:"maxprb,omitempty"`
	Profile          int    `json:"profile,omitempty"`
	ConnectTimeout   int    `json:"conntt,omitempty"`
	Forward          string `json:"forward,omitempty"`
}

// legacyConfigFields maps v2 config fields removed in v3 to migration hints.
var legacyConfigFields = map[string]string{
	"key":         `removed in v3: use "password" (one shared secret replaces both "key" and "encrypt_key")`,
	"encrypt_key": `removed in v3: merged into "password"`,
	"sock5":       `renamed in v3: use "socks5": true`,
}

// LoadConfig reads and unmarshals a Config JSON file. Removed v2 fields are
// rejected with a migration hint instead of being silently ignored.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config json: %w", err)
	}
	for field, hint := range legacyConfigFields {
		if _, ok := raw[field]; ok {
			return nil, fmt.Errorf("config field %q is no longer supported: %s", field, hint)
		}
	}

	cfg := &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config json: %w", err)
	}
	return cfg, nil
}
