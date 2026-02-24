package encryption

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerateMasterKey tests master key generation.
func TestGenerateMasterKey(t *testing.T) {
	key1, err := GenerateMasterKey()
	require.NoError(t, err)
	assert.NotEmpty(t, key1)

	// Key should be 64 hex characters (32 bytes * 2)
	assert.Len(t, key1, 64)

	// Should be valid hex
	decoded, err := hex.DecodeString(key1)
	require.NoError(t, err)
	assert.Len(t, decoded, 32)

	// Generate second key - should be different
	key2, err := GenerateMasterKey()
	require.NoError(t, err)
	assert.NotEqual(t, key1, key2, "generated keys should be unique")
}

// TestNewEncryptor_ValidKey tests creating an encryptor with a valid key.
func TestNewEncryptor_ValidKey(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	require.NoError(t, err)

	encryptor, err := NewEncryptor(masterKey)
	require.NoError(t, err)
	assert.NotNil(t, encryptor)
	assert.True(t, encryptor.IsEnabled())
	assert.Len(t, encryptor.kek, 32)
}

// TestNewEncryptor_EmptyKey tests that empty key returns encryption disabled error.
func TestNewEncryptor_EmptyKey(t *testing.T) {
	encryptor, err := NewEncryptor("")
	assert.ErrorIs(t, err, ErrEncryptionDisabled)
	assert.Nil(t, encryptor)
}

// TestNewEncryptor_InvalidHexKey tests that invalid hex returns appropriate error.
func TestNewEncryptor_InvalidHexKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{
			name: "non-hex characters",
			key:  "gggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg",
		},
		{
			name: "mixed valid/invalid",
			key:  "0123456789abcdefGHIJKLMNOPQRSTUVWXYZ",
		},
		{
			name: "special characters",
			key:  "0123456789abcdef!@#$%^&*()",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encryptor, err := NewEncryptor(tt.key)
			assert.ErrorIs(t, err, ErrInvalidHexKey)
			assert.Nil(t, encryptor)
		})
	}
}

// TestNewEncryptor_InvalidKeyLength tests keys with wrong length.
func TestNewEncryptor_InvalidKeyLength(t *testing.T) {
	tests := []struct {
		name   string
		keyLen int
	}{
		{name: "16 bytes (128 bit)", keyLen: 16},
		{name: "24 bytes (192 bit)", keyLen: 24},
		{name: "31 bytes (too short)", keyLen: 31},
		{name: "33 bytes (too long)", keyLen: 33},
		{name: "64 bytes (512 bit)", keyLen: 64},
		{name: "8 bytes", keyLen: 8},
		{name: "1 byte", keyLen: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := make([]byte, tt.keyLen)
			_, err := io.ReadFull(rand.Reader, key)
			require.NoError(t, err)

			encryptor, err := NewEncryptor(hex.EncodeToString(key))
			assert.ErrorIs(t, err, ErrInvalidKeyLength)
			assert.Nil(t, encryptor)
		})
	}
}

// TestEncryptDecrypt_RoundTrip tests successful encryption and decryption.
func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	require.NoError(t, err)

	encryptor, err := NewEncryptor(masterKey)
	require.NoError(t, err)

	tests := []struct {
		name      string
		plaintext []byte
	}{
		{
			name:      "simple text",
			plaintext: []byte("hello world"),
		},
		{
			name:      "json data",
			plaintext: []byte(`{"key":"value","number":42,"nested":{"foo":"bar"}}`),
		},
		{
			name:      "binary data",
			plaintext: []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0xFD},
		},
		{
			name:      "unicode text",
			plaintext: []byte("Hello 世界 🌍 مرحبا"),
		},
		{
			name:      "newlines and special chars",
			plaintext: []byte("line1\nline2\r\nline3\ttabbed\x00null"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encrypt
			encrypted, err := encryptor.Encrypt(tt.plaintext)
			require.NoError(t, err)
			require.NotNil(t, encrypted)

			// Verify metadata
			assert.Equal(t, ModeServer, encrypted.EncryptionMode)
			assert.Equal(t, int64(len(tt.plaintext)), encrypted.OriginalSize)
			assert.NotEmpty(t, encrypted.Ciphertext)
			assert.NotEmpty(t, encrypted.EncryptedDEK)
			assert.NotEmpty(t, encrypted.Nonce)

			// Ciphertext should be different from plaintext
			assert.NotEqual(t, tt.plaintext, encrypted.Ciphertext)

			// Decrypt
			decrypted, err := encryptor.Decrypt(encrypted)
			require.NoError(t, err)

			// Verify decryption
			assert.Equal(t, tt.plaintext, decrypted)
		})
	}
}

// TestEncryptDecrypt_EmptyData tests encrypting and decrypting empty data.
func TestEncryptDecrypt_EmptyData(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	require.NoError(t, err)

	encryptor, err := NewEncryptor(masterKey)
	require.NoError(t, err)

	plaintext := []byte{}

	encrypted, err := encryptor.Encrypt(plaintext)
	require.NoError(t, err)
	assert.Equal(t, int64(0), encrypted.OriginalSize)

	decrypted, err := encryptor.Decrypt(encrypted)
	require.NoError(t, err)
	assert.Empty(t, decrypted, "decrypted data should be empty")
}

// TestEncryptDecrypt_LargeData tests encrypting and decrypting large data.
func TestEncryptDecrypt_LargeData(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	require.NoError(t, err)

	encryptor, err := NewEncryptor(masterKey)
	require.NoError(t, err)

	// Test various sizes
	sizes := []int{
		1024,        // 1 KB
		1024 * 1024, // 1 MB
		5 * 1024 * 1024, // 5 MB
	}

	for _, size := range sizes {
		t.Run(formatSize(size), func(t *testing.T) {
			plaintext := make([]byte, size)
			_, err := io.ReadFull(rand.Reader, plaintext)
			require.NoError(t, err)

			encrypted, err := encryptor.Encrypt(plaintext)
			require.NoError(t, err)
			assert.Equal(t, int64(size), encrypted.OriginalSize)

			decrypted, err := encryptor.Decrypt(encrypted)
			require.NoError(t, err)
			assert.Equal(t, plaintext, decrypted)
		})
	}
}

// TestEncrypt_UniqueDEKs tests that each encryption produces unique DEKs.
func TestEncrypt_UniqueDEKs(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	require.NoError(t, err)

	encryptor, err := NewEncryptor(masterKey)
	require.NoError(t, err)

	plaintext := []byte("same data")

	// Encrypt same data multiple times
	encrypted1, err := encryptor.Encrypt(plaintext)
	require.NoError(t, err)

	encrypted2, err := encryptor.Encrypt(plaintext)
	require.NoError(t, err)

	encrypted3, err := encryptor.Encrypt(plaintext)
	require.NoError(t, err)

	// DEKs should be different
	assert.NotEqual(t, encrypted1.EncryptedDEK, encrypted2.EncryptedDEK)
	assert.NotEqual(t, encrypted1.EncryptedDEK, encrypted3.EncryptedDEK)
	assert.NotEqual(t, encrypted2.EncryptedDEK, encrypted3.EncryptedDEK)

	// Nonces should be different
	assert.NotEqual(t, encrypted1.Nonce, encrypted2.Nonce)
	assert.NotEqual(t, encrypted1.Nonce, encrypted3.Nonce)
	assert.NotEqual(t, encrypted2.Nonce, encrypted3.Nonce)

	// Ciphertexts should be different
	assert.NotEqual(t, encrypted1.Ciphertext, encrypted2.Ciphertext)
	assert.NotEqual(t, encrypted1.Ciphertext, encrypted3.Ciphertext)
	assert.NotEqual(t, encrypted2.Ciphertext, encrypted3.Ciphertext)

	// But all should decrypt to same plaintext
	decrypted1, err := encryptor.Decrypt(encrypted1)
	require.NoError(t, err)
	decrypted2, err := encryptor.Decrypt(encrypted2)
	require.NoError(t, err)
	decrypted3, err := encryptor.Decrypt(encrypted3)
	require.NoError(t, err)

	assert.Equal(t, plaintext, decrypted1)
	assert.Equal(t, plaintext, decrypted2)
	assert.Equal(t, plaintext, decrypted3)
}

// TestDecrypt_WrongKey tests that decryption fails with wrong master key.
func TestDecrypt_WrongKey(t *testing.T) {
	masterKey1, err := GenerateMasterKey()
	require.NoError(t, err)

	encryptor1, err := NewEncryptor(masterKey1)
	require.NoError(t, err)

	plaintext := []byte("secret data")
	encrypted, err := encryptor1.Encrypt(plaintext)
	require.NoError(t, err)

	// Try to decrypt with different key
	masterKey2, err := GenerateMasterKey()
	require.NoError(t, err)

	encryptor2, err := NewEncryptor(masterKey2)
	require.NoError(t, err)

	decrypted, err := encryptor2.Decrypt(encrypted)
	assert.ErrorIs(t, err, ErrDecryptionFailed)
	assert.Nil(t, decrypted)
}

// TestDecrypt_CorruptedCiphertext tests decryption of corrupted data.
func TestDecrypt_CorruptedCiphertext(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	require.NoError(t, err)

	encryptor, err := NewEncryptor(masterKey)
	require.NoError(t, err)

	plaintext := []byte("original data")
	encrypted, err := encryptor.Encrypt(plaintext)
	require.NoError(t, err)

	tests := []struct {
		name   string
		mutate func(*EncryptedBlob)
	}{
		{
			name: "corrupted ciphertext",
			mutate: func(e *EncryptedBlob) {
				if len(e.Ciphertext) > 0 {
					e.Ciphertext[0] ^= 0xFF
				}
			},
		},
		{
			name: "corrupted ciphertext end",
			mutate: func(e *EncryptedBlob) {
				if len(e.Ciphertext) > 0 {
					e.Ciphertext[len(e.Ciphertext)-1] ^= 0xFF
				}
			},
		},
		{
			name: "truncated ciphertext",
			mutate: func(e *EncryptedBlob) {
				if len(e.Ciphertext) > 5 {
					e.Ciphertext = e.Ciphertext[:len(e.Ciphertext)-5]
				}
			},
		},
		{
			name: "corrupted encrypted DEK",
			mutate: func(e *EncryptedBlob) {
				decoded, _ := base64.StdEncoding.DecodeString(e.EncryptedDEK)
				if len(decoded) > 0 {
					decoded[0] ^= 0xFF
					e.EncryptedDEK = base64.StdEncoding.EncodeToString(decoded)
				}
			},
		},
		{
			name: "invalid base64 encrypted DEK",
			mutate: func(e *EncryptedBlob) {
				e.EncryptedDEK = "not-valid-base64!!!"
			},
		},
		{
			name: "truncated encrypted DEK",
			mutate: func(e *EncryptedBlob) {
				decoded, _ := base64.StdEncoding.DecodeString(e.EncryptedDEK)
				if len(decoded) > 5 {
					e.EncryptedDEK = base64.StdEncoding.EncodeToString(decoded[:len(decoded)-5])
				}
			},
		},
		{
			name: "empty encrypted DEK",
			mutate: func(e *EncryptedBlob) {
				e.EncryptedDEK = ""
			},
		},
		{
			name: "short encrypted DEK",
			mutate: func(e *EncryptedBlob) {
				e.EncryptedDEK = base64.StdEncoding.EncodeToString([]byte{0x01, 0x02})
			},
		},
		{
			name: "corrupted nonce",
			mutate: func(e *EncryptedBlob) {
				decoded, _ := base64.StdEncoding.DecodeString(e.Nonce)
				if len(decoded) > 0 {
					decoded[0] ^= 0xFF
					e.Nonce = base64.StdEncoding.EncodeToString(decoded)
				}
			},
		},
		{
			name: "invalid base64 nonce",
			mutate: func(e *EncryptedBlob) {
				e.Nonce = "invalid-base64###"
			},
		},
		{
			name: "wrong nonce size",
			mutate: func(e *EncryptedBlob) {
				e.Nonce = base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			corrupted := &EncryptedBlob{
				Ciphertext:     make([]byte, len(encrypted.Ciphertext)),
				EncryptedDEK:   encrypted.EncryptedDEK,
				Nonce:          encrypted.Nonce,
				OriginalSize:   encrypted.OriginalSize,
				EncryptionMode: encrypted.EncryptionMode,
			}
			copy(corrupted.Ciphertext, encrypted.Ciphertext)

			tt.mutate(corrupted)

			decrypted, err := encryptor.Decrypt(corrupted)
			assert.Error(t, err, "decryption should fail with corrupted data")
			assert.Nil(t, decrypted)
		})
	}
}

// TestDecrypt_ModeNone tests that ModeNone returns data as-is.
func TestDecrypt_ModeNone(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	require.NoError(t, err)

	encryptor, err := NewEncryptor(masterKey)
	require.NoError(t, err)

	plaintext := []byte("unencrypted data")
	encrypted := &EncryptedBlob{
		Ciphertext:     plaintext,
		EncryptedDEK:   "",
		Nonce:          "",
		OriginalSize:   int64(len(plaintext)),
		EncryptionMode: ModeNone,
	}

	decrypted, err := encryptor.Decrypt(encrypted)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

// TestDecrypt_ModeE2E tests that ModeE2E returns ciphertext as-is.
func TestDecrypt_ModeE2E(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	require.NoError(t, err)

	encryptor, err := NewEncryptor(masterKey)
	require.NoError(t, err)

	ciphertext := []byte("client-encrypted data that server cannot decrypt")
	encrypted := &EncryptedBlob{
		Ciphertext:     ciphertext,
		EncryptedDEK:   "",
		Nonce:          "",
		OriginalSize:   100, // Original size before client encryption
		EncryptionMode: ModeE2E,
	}

	decrypted, err := encryptor.Decrypt(encrypted)
	require.NoError(t, err)
	assert.Equal(t, ciphertext, decrypted, "E2E encrypted data should be returned as-is")
}

// TestIsEnabled tests the IsEnabled method.
func TestIsEnabled(t *testing.T) {
	t.Run("enabled with valid key", func(t *testing.T) {
		masterKey, err := GenerateMasterKey()
		require.NoError(t, err)

		encryptor, err := NewEncryptor(masterKey)
		require.NoError(t, err)
		assert.True(t, encryptor.IsEnabled())
	})

	t.Run("nil encryptor", func(t *testing.T) {
		var encryptor *Encryptor
		assert.False(t, encryptor.IsEnabled())
	})
}

// TestEncryptionMetadata tests that encryption metadata is correct.
func TestEncryptionMetadata(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	require.NoError(t, err)

	encryptor, err := NewEncryptor(masterKey)
	require.NoError(t, err)

	plaintext := []byte("test data for metadata verification")
	encrypted, err := encryptor.Encrypt(plaintext)
	require.NoError(t, err)

	// Verify EncryptedDEK is valid base64
	dekBytes, err := base64.StdEncoding.DecodeString(encrypted.EncryptedDEK)
	require.NoError(t, err)
	assert.Greater(t, len(dekBytes), 0, "EncryptedDEK should decode to non-empty bytes")

	// Verify Nonce is valid base64 and correct size
	nonceBytes, err := base64.StdEncoding.DecodeString(encrypted.Nonce)
	require.NoError(t, err)
	assert.Equal(t, 12, len(nonceBytes), "GCM nonce should be 12 bytes")

	// Verify OriginalSize matches
	assert.Equal(t, int64(len(plaintext)), encrypted.OriginalSize)

	// Verify EncryptionMode
	assert.Equal(t, ModeServer, encrypted.EncryptionMode)

	// Verify Ciphertext size (should be plaintext + auth tag overhead)
	// GCM adds 16 bytes authentication tag
	assert.Equal(t, len(plaintext)+16, len(encrypted.Ciphertext))
}

// TestDifferentEncryptors tests that different encryptors can't decrypt each other's data.
func TestDifferentEncryptors(t *testing.T) {
	key1, err := GenerateMasterKey()
	require.NoError(t, err)

	key2, err := GenerateMasterKey()
	require.NoError(t, err)

	encryptor1, err := NewEncryptor(key1)
	require.NoError(t, err)

	encryptor2, err := NewEncryptor(key2)
	require.NoError(t, err)

	plaintext := []byte("sensitive data")

	// Encrypt with encryptor1
	encrypted, err := encryptor1.Encrypt(plaintext)
	require.NoError(t, err)

	// Try to decrypt with encryptor2 (should fail)
	decrypted, err := encryptor2.Decrypt(encrypted)
	assert.ErrorIs(t, err, ErrDecryptionFailed)
	assert.Nil(t, decrypted)

	// Decrypt with correct encryptor (should succeed)
	decrypted, err = encryptor1.Decrypt(encrypted)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

// TestConcurrentEncryption tests concurrent encryption operations.
func TestConcurrentEncryption(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	require.NoError(t, err)

	encryptor, err := NewEncryptor(masterKey)
	require.NoError(t, err)

	const numGoroutines = 10
	done := make(chan bool, numGoroutines)
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			plaintext := []byte("concurrent test data from goroutine")

			encrypted, err := encryptor.Encrypt(plaintext)
			if err != nil {
				errors <- err
				done <- false
				return
			}

			decrypted, err := encryptor.Decrypt(encrypted)
			if err != nil {
				errors <- err
				done <- false
				return
			}

			if !bytes.Equal(plaintext, decrypted) {
				errors <- assert.AnError
				done <- false
				return
			}

			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < numGoroutines; i++ {
		select {
		case success := <-done:
			assert.True(t, success)
		case err := <-errors:
			t.Errorf("concurrent operation failed: %v", err)
		}
	}
}

// TestEncryptionModes tests all encryption mode constants.
func TestEncryptionModes(t *testing.T) {
	assert.Equal(t, EncryptionMode("none"), ModeNone)
	assert.Equal(t, EncryptionMode("server"), ModeServer)
	assert.Equal(t, EncryptionMode("e2e"), ModeE2E)
}

// Helper function to format size for test names.
func formatSize(bytes int) string {
	const (
		KB = 1024
		MB = 1024 * 1024
	)

	switch {
	case bytes >= MB:
		return fmt.Sprintf("%dMB", bytes/MB)
	case bytes >= KB:
		return fmt.Sprintf("%dKB", bytes/KB)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
