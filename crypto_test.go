package pingtunnel

import (
	"bytes"
	"testing"
)

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword(""); err == nil {
		t.Fatalf("empty password accepted")
	}
	if err := ValidatePassword("short"); err == nil {
		t.Fatalf("short password accepted")
	}
	if err := ValidatePassword("long-enough-secret"); err != nil {
		t.Fatalf("valid password rejected: %v", err)
	}
}

func TestDeriveAuthCode(t *testing.T) {
	code := DeriveAuthCode("correct-horse-battery")
	if code <= 0 {
		t.Fatalf("auth code out of sint32 positive range: %d", code)
	}
	if code != DeriveAuthCode("correct-horse-battery") {
		t.Fatalf("auth code is not deterministic")
	}
	other := DeriveAuthCode("other-password")
	if code == other {
		t.Fatalf("different passwords derived the same auth code")
	}
}

// Client and server derive both halves of the credential from the same
// password: the auth code must match, and each side's AEAD must decrypt the
// other's output.
func TestPasswordPairInterop(t *testing.T) {
	password := "shared-secret-for-pair"
	clientCrypto, err := NewCryptoConfig(CHACHA20, password)
	if err != nil {
		t.Fatalf("client crypto: %v", err)
	}
	serverCrypto, err := NewCryptoConfig(CHACHA20, password)
	if err != nil {
		t.Fatalf("server crypto: %v", err)
	}

	msg := []byte("interop probe")
	enc, err := clientCrypto.Encrypt(msg)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	dec, err := serverCrypto.Decrypt(enc)
	if err != nil {
		t.Fatalf("server cannot decrypt client payload: %v", err)
	}
	if !bytes.Equal(dec, msg) {
		t.Fatalf("roundtrip mismatch")
	}

	authCode := DeriveAuthCode(password)
	if authCode != DeriveAuthCode(password) {
		t.Fatalf("auth codes diverged")
	}
}

func TestCryptoConfig_AES128(t *testing.T) {
	key := "MTIzNDU2Nzg5MDEyMzQ1Ng=="
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
