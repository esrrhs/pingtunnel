package pingtunnel

import (
	"testing"
)

func TestParseForwardURL(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantNil   bool
		wantErr   bool
		wantScheme string
		wantHost   string
		wantPort   int
	}{
		{
			name:    "empty string returns nil",
			input:   "",
			wantNil: true,
			wantErr: false,
		},
		{
			name:       "valid socks5 URL",
			input:      "socks5://localhost:2080",
			wantNil:    false,
			wantErr:    false,
			wantScheme: "socks5",
			wantHost:   "localhost",
			wantPort:   2080,
		},
		{
			name:       "valid http URL",
			input:      "http://127.0.0.1:8080",
			wantNil:    false,
			wantErr:    false,
			wantScheme: "http",
			wantHost:   "127.0.0.1",
			wantPort:   8080,
		},
		{
			name:       "valid socks5 with IP",
			input:      "socks5://192.168.1.1:1080",
			wantNil:    false,
			wantErr:    false,
			wantScheme: "socks5",
			wantHost:   "192.168.1.1",
			wantPort:   1080,
		},
		{
			name:    "unsupported scheme",
			input:   "https://localhost:8080",
			wantErr: true,
		},
		{
			name:    "missing port",
			input:   "socks5://localhost",
			wantErr: true,
		},
		{
			name:    "missing host",
			input:   "socks5://:8080",
			wantErr: true,
		},
		{
			name:    "invalid port",
			input:   "socks5://localhost:abc",
			wantErr: true,
		},
		{
			name:    "invalid URL format",
			input:   "not-a-valid-url",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseForwardURL(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseForwardURL(%q) expected error, got nil", tt.input)
				}
				return
			}

			if err != nil {
				t.Errorf("ParseForwardURL(%q) unexpected error: %v", tt.input, err)
				return
			}

			if tt.wantNil {
				if got != nil {
					t.Errorf("ParseForwardURL(%q) expected nil, got %+v", tt.input, got)
				}
				return
			}

			if got == nil {
				t.Errorf("ParseForwardURL(%q) expected non-nil, got nil", tt.input)
				return
			}

			if got.Scheme != tt.wantScheme {
				t.Errorf("ParseForwardURL(%q).Scheme = %q, want %q", tt.input, got.Scheme, tt.wantScheme)
			}
			if got.Host != tt.wantHost {
				t.Errorf("ParseForwardURL(%q).Host = %q, want %q", tt.input, got.Host, tt.wantHost)
			}
			if got.Port != tt.wantPort {
				t.Errorf("ParseForwardURL(%q).Port = %d, want %d", tt.input, got.Port, tt.wantPort)
			}
		})
	}
}

func TestForwardConfigAddress(t *testing.T) {
	cfg := &ForwardConfig{
		Scheme: "socks5",
		Host:   "localhost",
		Port:   2080,
	}

	got := cfg.Address()
	want := "localhost:2080"
	if got != want {
		t.Errorf("ForwardConfig.Address() = %q, want %q", got, want)
	}
}
