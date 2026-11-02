package pingtunnel

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/pbkdf2"
)

// EncryptionMode represents the encryption mode
type EncryptionMode int

const (
	NoEncryption EncryptionMode = iota
	AES128
	AES256
	CHACHA20
)

// CryptoConfig holds encryption configuration
type CryptoConfig struct {
	Mode   EncryptionMode
	Key    []byte
	Cipher cipher.AEAD
}

// NewCryptoConfig creates a new crypto configuration
func NewCryptoConfig(mode EncryptionMode, keyInput string) (*CryptoConfig, error) {
	if mode == NoEncryption {
		return &CryptoConfig{Mode: NoEncryption}, nil
	}

	var keySize int
	switch mode {
	case AES128:
		keySize = 16 // 128 bits
	case AES256:
		keySize = 32 // 256 bits
	case CHACHA20:
		keySize = chacha20poly1305.KeySize // 32 bytes
	default:
		return nil, fmt.Errorf("unsupported encryption mode: %d", mode)
	}

	key, err := deriveKey(keyInput, keySize)
	if err != nil {
		return nil, fmt.Errorf("failed to derive key: %v", err)
	}

	// Create AEAD based on mode
	var aead cipher.AEAD
	switch mode {
	case AES128, AES256:
		// AES-GCM
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, fmt.Errorf("failed to create AES cipher: %v", err)
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("failed to create GCM: %v", err)
		}
		aead = gcm
	case CHACHA20:
		// ChaCha20-Poly1305
		cc20, err := chacha20poly1305.New(key)
		if err != nil {
			return nil, fmt.Errorf("failed to create ChaCha20-Poly1305: %v", err)
		}
		aead = cc20
	default:
		return nil, fmt.Errorf("unsupported encryption mode: %d", mode)
	}

	return &CryptoConfig{
		Mode:   mode,
		Key:    key,
		Cipher: aead,
	}, nil
}

// KDF parameters. Domain-separated salts keep the authentication code and
// the encryption key independent even though both derive from the password.
const (
	pbkdf2Iterations = 600000 // OWASP-recommended count for PBKDF2-HMAC-SHA256
	authSalt         = "pingtunnel-auth-v2"
	encSalt          = "pingtunnel-enc-v2"
	minPasswordLen   = 8
)

// ValidatePassword checks a -password value before it is used anywhere.
func ValidatePassword(password string) error {
	if password == "" {
		return errors.New("password is required (flag -password)")
	}
	if len(password) < minPasswordLen {
		return fmt.Errorf("password is too short: need at least %d characters", minPasswordLen)
	}
	return nil
}

// DeriveAuthCode maps a password onto the protocol's 31-bit authentication
// code (MyMsg.Key). It is not a secret on its own — the AEAD layer already
// authenticates every packet — but provides a cheap first-byte filter before
// attempting packet decryption.
func DeriveAuthCode(password string) int {
	k := pbkdf2.Key([]byte(password), []byte(authSalt), pbkdf2Iterations, 4, sha256.New)
	code := int(uint32(k[0]) | uint32(k[1])<<8 | uint32(k[2])<<16 | uint32(k[3])<<24)
	return code & 0x7fffffff // stay within MyMsg.Key's sint32 positive range
}

// deriveKey derives an encryption key from the password
func deriveKey(password string, keySize int) ([]byte, error) {
	if password == "" {
		return nil, errors.New("encryption key cannot be empty")
	}
	return pbkdf2.Key([]byte(password), []byte(encSalt), pbkdf2Iterations, keySize, sha256.New), nil
}

// Encrypt encrypts the given data
func (c *CryptoConfig) Encrypt(data []byte) ([]byte, error) {
	if c.Mode == NoEncryption {
		return data, nil
	}
	if c.Cipher == nil {
		return nil, errors.New("cipher not initialized")
	}

	// One output buffer, zero intermediate copies: the nonce is generated
	// directly at the head of the buffer and Seal appends the ciphertext
	// right behind it (dst must not overlap plaintext — the nonce region
	// may overlap dst, Seal only writes past dst's length).
	nonceSize := c.Cipher.NonceSize()
	result := make([]byte, nonceSize, nonceSize+len(data)+c.Cipher.Overhead())
	if _, err := rand.Read(result); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %v", err)
	}
	return c.Cipher.Seal(result, result[:nonceSize], data, nil), nil
}

// EncryptInto appends the nonce-ciphertext envelope for data onto dst and
// returns the extended slice. dst must not overlap data. Used on the send
// path to write the envelope straight into a pooled packet buffer.
func (c *CryptoConfig) EncryptInto(dst []byte, data []byte) ([]byte, error) {
	if c.Mode == NoEncryption {
		return append(dst, data...), nil
	}
	if c.Cipher == nil {
		return nil, errors.New("cipher not initialized")
	}

	nonceSize := c.Cipher.NonceSize()
	start := len(dst)
	if need := start + nonceSize + len(data) + c.Cipher.Overhead(); cap(dst) < need {
		grown := make([]byte, start, need)
		copy(grown, dst[:start])
		dst = grown
	}
	// Reserve the nonce slot in dst's length so Seal appends the ciphertext
	// behind it — the nonce region must not sit inside the ciphertext's
	// write region.
	dst = dst[:start+nonceSize]
	if _, err := rand.Read(dst[start:]); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %v", err)
	}
	return c.Cipher.Seal(dst[:start+nonceSize], dst[start:start+nonceSize], data, nil), nil
}

// Decrypt decrypts the given data
func (c *CryptoConfig) Decrypt(data []byte) ([]byte, error) {
	if c.Mode == NoEncryption {
		return data, nil
	}

	if c.Cipher == nil {
		return nil, errors.New("cipher not initialized")
	}

	nonceSize := c.Cipher.NonceSize()
	if len(data) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	// Extract nonce and ciphertext
	nonce := data[:nonceSize]
	ciphertext := data[nonceSize:]

	// Decrypt the data
	plaintext, err := c.Cipher.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %v", err)
	}

	return plaintext, nil
}

// String returns a string representation of the encryption mode
func (m EncryptionMode) String() string {
	switch m {
	case NoEncryption:
		return "none"
	case AES128:
		return "aes128"
	case AES256:
		return "aes256"
	case CHACHA20:
		return "chacha20"
	default:
		return "unknown"
	}
}

// ParseEncryptionMode parses a string into an EncryptionMode
func ParseEncryptionMode(s string) (EncryptionMode, error) {
	switch s {
	case "", "none":
		return NoEncryption, nil
	case "aes128":
		return AES128, nil
	case "aes256":
		return AES256, nil
	case "chacha20", "chacha20-poly1305":
		return CHACHA20, nil
	default:
		return NoEncryption, fmt.Errorf("invalid encryption mode: %s", s)
	}
}
