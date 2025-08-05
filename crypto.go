package pingtunnel

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"golang.org/x/crypto/pbkdf2"
)

// EncryptionMode represents the AES encryption mode
type EncryptionMode int

const (
	NoEncryption EncryptionMode = iota
	AES128
	AES256
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
	default:
		return nil, fmt.Errorf("unsupported encryption mode: %d", mode)
	}

	key, err := deriveKey(keyInput, keySize)
	if err != nil {
		return nil, fmt.Errorf("failed to derive key: %v", err)
	}

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %v", err)
	}

	// Create GCM mode for authenticated encryption
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %v", err)
	}

	return &CryptoConfig{
		Mode:   mode,
		Key:    key,
		Cipher: gcm,
	}, nil
}

// deriveKey derives an encryption key from the input string
func deriveKey(keyInput string, keySize int) ([]byte, error) {
	if keyInput == "" {
		return nil, errors.New("encryption key cannot be empty")
	}

	// First, try to decode as base64
	if decoded, err := base64.StdEncoding.DecodeString(keyInput); err == nil {
		if len(decoded) == keySize {
			return decoded, nil
		}
	}

	// If not valid base64 or wrong size, use PBKDF2 to derive key
	salt := []byte("pingtunnel-salt") // Fixed salt for deterministic key derivation
	iterations := 10000               // Standard iteration count
	return pbkdf2.Key([]byte(keyInput), salt, iterations, keySize, sha256.New), nil
}

// Encrypt encrypts the given data
func (c *CryptoConfig) Encrypt(data []byte) ([]byte, error) {
	if c.Mode == NoEncryption {
		return data, nil
	}

	if c.Cipher == nil {
		return nil, errors.New("cipher not initialized")
	}

	// Generate a random nonce
	nonce := make([]byte, c.Cipher.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %v", err)
	}

	// Encrypt the data
	ciphertext := c.Cipher.Seal(nil, nonce, data, nil)

	// Prepend nonce to ciphertext
	result := make([]byte, len(nonce)+len(ciphertext))
	copy(result, nonce)
	copy(result[len(nonce):], ciphertext)

	return result, nil
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
	default:
		return NoEncryption, fmt.Errorf("invalid encryption mode: %s", s)
	}
}
