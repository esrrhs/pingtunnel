package pingtunnel

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncryptionModeString(t *testing.T) {
	cases := map[EncryptionMode]string{
		NoEncryption:       "none",
		AES128:             "aes128",
		AES256:             "aes256",
		CHACHA20:           "chacha20",
		EncryptionMode(99): "unknown",
	}
	for mode, want := range cases {
		if got := mode.String(); got != want {
			t.Errorf("EncryptionMode(%d).String() = %q, want %q", mode, got, want)
		}
	}
}

func TestParseEncryptionModeAll(t *testing.T) {
	cases := map[string]EncryptionMode{
		"":                  NoEncryption,
		"none":              NoEncryption,
		"aes128":            AES128,
		"aes256":            AES256,
		"chacha20":          CHACHA20,
		"chacha20-poly1305": CHACHA20,
	}
	for in, want := range cases {
		got, err := ParseEncryptionMode(in)
		if err != nil || got != want {
			t.Errorf("ParseEncryptionMode(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseEncryptionMode("rot13"); err == nil || !strings.Contains(err.Error(), "invalid encryption mode") {
		t.Errorf("unknown mode err = %v", err)
	}
}

func TestNewCryptoConfigUnsupportedMode(t *testing.T) {
	if _, err := NewCryptoConfig(EncryptionMode(99), "12345678"); err == nil ||
		!strings.Contains(err.Error(), "unsupported encryption mode") {
		t.Fatalf("err = %v", err)
	}
}

func TestDeriveKeyEmptyPassword(t *testing.T) {
	if _, err := deriveKey("", 32); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err = %v", err)
	}
	key, err := deriveKey("12345678", 32)
	if err != nil || len(key) != 32 {
		t.Fatalf("deriveKey = %v, %v", key, err)
	}
}

func TestCryptoEncryptDecryptPaths(t *testing.T) {
	t.Run("no encryption passthrough", func(t *testing.T) {
		cfg := &CryptoConfig{Mode: NoEncryption}
		plain := []byte("plain")
		out, err := cfg.Encrypt(plain)
		if err != nil || !bytes.Equal(out, plain) {
			t.Fatalf("encrypt = %v, %v", out, err)
		}
		out, err = cfg.Decrypt(plain)
		if err != nil || !bytes.Equal(out, plain) {
			t.Fatalf("decrypt = %v, %v", out, err)
		}
	})

	t.Run("nil cipher errors", func(t *testing.T) {
		cfg := &CryptoConfig{Mode: AES256, Cipher: nil}
		if _, err := cfg.Encrypt([]byte("x")); err == nil || !strings.Contains(err.Error(), "cipher not initialized") {
			t.Fatalf("encrypt err = %v", err)
		}
		if _, err := cfg.Decrypt([]byte("x")); err == nil || !strings.Contains(err.Error(), "cipher not initialized") {
			t.Fatalf("decrypt err = %v", err)
		}
	})

	for _, mode := range []EncryptionMode{AES128, AES256, CHACHA20} {
		cfg, err := NewCryptoConfig(mode, "round-trip-secret")
		if err != nil {
			t.Fatalf("NewCryptoConfig(%v): %v", mode, err)
		}
		plain := []byte("the quick brown fox jumps over the lazy dog")
		ct, err := cfg.Encrypt(plain)
		if err != nil {
			t.Fatalf("%v encrypt: %v", mode, err)
		}
		if bytes.Equal(ct, plain) {
			t.Fatalf("%v ciphertext equals plaintext", mode)
		}
		got, err := cfg.Decrypt(ct)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("%v decrypt = %q, %v", mode, got, err)
		}

		if _, err := cfg.Decrypt([]byte{0x01}); err == nil || !strings.Contains(err.Error(), "too short") {
			t.Fatalf("%v short-ciphertext err = %v", mode, err)
		}
		corrupt := append([]byte(nil), ct...)
		corrupt[len(corrupt)-1] ^= 0xFF
		if _, err := cfg.Decrypt(corrupt); err == nil || !strings.Contains(err.Error(), "decryption failed") {
			t.Fatalf("%v corrupted-ciphertext err = %v", mode, err)
		}
	}
}
