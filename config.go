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
	Key              int    `json:"key,omitempty"`
	Encrypt          string `json:"encrypt,omitempty"`
	EncryptKey       string `json:"encrypt_key,omitempty"`
	TCPMode          int    `json:"tcp,omitempty"`
	TCPBufferSize    int    `json:"tcp_bs,omitempty"`
	TCPMaxWin        int    `json:"tcp_mw,omitempty"`
	TCPResendTimeMs  int    `json:"tcp_rst,omitempty"`
	TCPCompress      int    `json:"tcp_gz,omitempty"`
	TCPStat          int    `json:"tcp_stat,omitempty"`
	NoLog            int    `json:"nolog,omitempty"`
	NoPrint          int    `json:"noprint,omitempty"`
	LogLevel         string `json:"loglevel,omitempty"`
	Sock5            int    `json:"sock5,omitempty"`
	Sock5User        string `json:"s5user,omitempty"`
	Sock5Pass        string `json:"s5pass,omitempty"`
	MaxConn          int    `json:"maxconn,omitempty"`
	MaxProcessThread int    `json:"maxprt,omitempty"`
	MaxProcessBuffer int    `json:"maxprb,omitempty"`
	Profile          int    `json:"profile,omitempty"`
	ConnectTimeout   int    `json:"conntt,omitempty"`
	Forward          string `json:"forward,omitempty"`
	Sock5Filter      string `json:"s5filter,omitempty"`
	Sock5FilterFile  string `json:"s5ftfile,omitempty"`
}

// LoadConfig reads and unmarshals a Config JSON file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	cfg := &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config json: %w", err)
	}
	return cfg, nil
}
