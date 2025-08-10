package pingtunnel

import (
	"bytes"
	"testing"
)

func TestCryptoConfig_AES128(t *testing.T) {
	// Test with a base64 encoded key
	key := "MTIzNDU2Nzg5MDEyMzQ1Ng==" // "1234567890123456" in base64
	config, err := NewCryptoConfig(AES128, key)
	if err != nil {
		t.Fatalf("Failed to create crypto config: %v", err)
	}

	testData := []byte("Hello, World! This is a test message for encryption.")
	
	// Test encryption
	encrypted, err := config.Encrypt(testData)
	if err != nil {
		t.Fatalf("Failed to encrypt data: %v", err)
	}

	// Encrypted data should be different from original
	if bytes.Equal(testData, encrypted) {
		t.Fatal("Encrypted data should be different from original")
	}

	// Test decryption
	decrypted, err := config.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Failed to decrypt data: %v", err)
	}

	// Decrypted data should match original
	if !bytes.Equal(testData, decrypted) {
		t.Fatalf("Decrypted data doesn't match original. Got: %s, Expected: %s", string(decrypted), string(testData))
	}
}

func TestCryptoConfig_AES256(t *testing.T) {
	// Test with a passphrase (will be derived using PBKDF2)
	config, err := NewCryptoConfig(AES256, "my-secret-passphrase")
	if err != nil {
		t.Fatalf("Failed to create crypto config: %v", err)
	}

	testData := []byte("This is a longer test message to verify AES-256 encryption works correctly with derived keys.")
	
	// Test encryption
	encrypted, err := config.Encrypt(testData)
	if err != nil {
		t.Fatalf("Failed to encrypt data: %v", err)
	}

	// Test decryption
	decrypted, err := config.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Failed to decrypt data: %v", err)
	}

	// Decrypted data should match original
	if !bytes.Equal(testData, decrypted) {
		t.Fatalf("Decrypted data doesn't match original. Got: %s, Expected: %s", string(decrypted), string(testData))
	}
}

func TestCryptoConfig_ChaCha20(t *testing.T) {
	// Test with a passphrase (PBKDF2 to 32 bytes)
	config, err := NewCryptoConfig(CHACHA20, "another-secret-passphrase")
	if err != nil {
		t.Fatalf("Failed to create crypto config: %v", err)
	}

	testData := []byte("Testing ChaCha20-Poly1305 AEAD for encryption and decryption correctness.")

	encrypted, err := config.Encrypt(testData)
	if err != nil {
		t.Fatalf("Failed to encrypt data: %v", err)
	}

	if bytes.Equal(testData, encrypted) {
		t.Fatal("Encrypted data should be different from original")
	}

	decrypted, err := config.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Failed to decrypt data: %v", err)
	}

	if !bytes.Equal(testData, decrypted) {
		t.Fatalf("Decrypted data doesn't match original. Got: %s, Expected: %s", string(decrypted), string(testData))
	}
}

func TestCryptoConfig_NoEncryption(t *testing.T) {
	config, err := NewCryptoConfig(NoEncryption, "")
	if err != nil {
		t.Fatalf("Failed to create crypto config: %v", err)
	}

	testData := []byte("This should not be encrypted")
	
	// Test "encryption" (should return original data)
	encrypted, err := config.Encrypt(testData)
	if err != nil {
		t.Fatalf("Failed to encrypt data: %v", err)
	}

	if !bytes.Equal(testData, encrypted) {
		t.Fatal("No encryption should return original data")
	}

	// Test "decryption" (should return original data)
	decrypted, err := config.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Failed to decrypt data: %v", err)
	}

	if !bytes.Equal(testData, decrypted) {
		t.Fatal("No encryption should return original data")
	}
}

func TestParseEncryptionMode(t *testing.T) {
	tests := []struct {
		input    string
		expected EncryptionMode
		hasError bool
	}{
		{"", NoEncryption, false},
		{"none", NoEncryption, false},
		{"aes128", AES128, false},
		{"aes256", AES256, false},
		{"chacha20", CHACHA20, false},
		{"chacha20-poly1305", CHACHA20, false},
		{"invalid", NoEncryption, true},
	}

	for _, test := range tests {
		result, err := ParseEncryptionMode(test.input)
		if test.hasError && err == nil {
			t.Fatalf("Expected error for input %s, but got none", test.input)
		}
		if !test.hasError && err != nil {
			t.Fatalf("Unexpected error for input %s: %v", test.input, err)
		}
		if result != test.expected {
			t.Fatalf("For input %s, expected %v, got %v", test.input, test.expected, result)
		}
	}
}

func TestKeyDerivation(t *testing.T) {
	// Test with valid base64 key
	validBase64 := "MTIzNDU2Nzg5MDEyMzQ1Ng==" // 16 bytes when decoded
	key, err := deriveKey(validBase64, 16)
	if err != nil {
		t.Fatalf("Failed to derive key from valid base64: %v", err)
	}
	if len(key) != 16 {
		t.Fatalf("Expected key length 16, got %d", len(key))
	}

	// Test with passphrase (should use PBKDF2)
	passphrase := "my-secret-passphrase"
	key2, err := deriveKey(passphrase, 32)
	if err != nil {
		t.Fatalf("Failed to derive key from passphrase: %v", err)
	}
	if len(key2) != 32 {
		t.Fatalf("Expected key length 32, got %d", len(key2))
	}

	// Same passphrase should produce same key (deterministic)
	key3, err := deriveKey(passphrase, 32)
	if err != nil {
		t.Fatalf("Failed to derive key from passphrase (second time): %v", err)
	}
	if !bytes.Equal(key2, key3) {
		t.Fatal("Same passphrase should produce same derived key")
	}
}
