// Package encryption provides server-side encryption at rest using AES-256-GCM.
//
// Architecture:
//   - Each blob gets a unique DEK (Data Encryption Key)
//   - DEKs are encrypted with the master KEK (Key Encryption Key)
//   - Blobs are encrypted with their DEK using AES-256-GCM
//   - The encrypted DEK and nonce are stored alongside the blob
//
// This provides:
//   - Per-blob unique keys (compromise of one doesn't expose others)
//   - Fast key rotation (just re-encrypt DEKs, not blobs)
//   - Protection against storage system compromise
package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// EncryptionMode represents the encryption state of a blob.
type EncryptionMode string

const (
	// ModeNone indicates plaintext storage (no encryption).
	ModeNone EncryptionMode = "none"
	// ModeServer indicates server-side encryption at rest.
	ModeServer EncryptionMode = "server"
	// ModeE2E indicates end-to-end encryption (client-encrypted, server cannot decrypt).
	ModeE2E EncryptionMode = "e2e"
)

// EncryptedBlob contains an encrypted blob and its metadata.
type EncryptedBlob struct {
	Ciphertext      []byte // Encrypted blob data
	EncryptedDEK    string // Base64-encoded DEK encrypted with KEK
	Nonce           string // Base64-encoded 12-byte nonce
	OriginalSize    int64  // Size before encryption
	EncryptionMode  EncryptionMode
}

// Encryptor handles blob encryption and decryption.
type Encryptor struct {
	kek    []byte        // Key Encryption Key (master key)
	kekGCM cipher.AEAD   // GCM cipher for encrypting DEKs
}

var (
	ErrInvalidKeyLength   = errors.New("key must be 32 bytes (256 bits)")
	ErrInvalidHexKey      = errors.New("key must be valid hex-encoded 32 bytes")
	ErrDecryptionFailed   = errors.New("decryption failed: invalid ciphertext or key")
	ErrEncryptionDisabled = errors.New("encryption is not enabled")
)

// NewEncryptor creates a new Encryptor with the given master key.
// The key should be 32 bytes (256 bits) hex-encoded.
func NewEncryptor(masterKeyHex string) (*Encryptor, error) {
	if masterKeyHex == "" {
		return nil, ErrEncryptionDisabled
	}

	kek, err := hex.DecodeString(masterKeyHex)
	if err != nil {
		return nil, ErrInvalidHexKey
	}

	if len(kek) != 32 {
		return nil, ErrInvalidKeyLength
	}

	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	kekGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM cipher: %w", err)
	}

	return &Encryptor{
		kek:    kek,
		kekGCM: kekGCM,
	}, nil
}

// GenerateMasterKey generates a new random 32-byte master key and returns it hex-encoded.
func GenerateMasterKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", fmt.Errorf("failed to generate random key: %w", err)
	}
	return hex.EncodeToString(key), nil
}

// Encrypt encrypts plaintext data using server-side encryption.
// Returns the encrypted blob with all necessary metadata for decryption.
func (e *Encryptor) Encrypt(plaintext []byte) (*EncryptedBlob, error) {
	// Generate a unique DEK for this blob
	dek := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, fmt.Errorf("failed to generate DEK: %w", err)
	}

	// Create cipher for data encryption
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, fmt.Errorf("failed to create data cipher: %w", err)
	}

	dataGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create data GCM: %w", err)
	}

	// Generate nonce for data encryption
	dataNonce := make([]byte, dataGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, dataNonce); err != nil {
		return nil, fmt.Errorf("failed to generate data nonce: %w", err)
	}

	// Encrypt the data
	ciphertext := dataGCM.Seal(nil, dataNonce, plaintext, nil)

	// Encrypt the DEK with the KEK
	kekNonce := make([]byte, e.kekGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, kekNonce); err != nil {
		return nil, fmt.Errorf("failed to generate KEK nonce: %w", err)
	}

	// The encrypted DEK includes both the nonce and the encrypted key
	encryptedDEK := e.kekGCM.Seal(kekNonce, kekNonce, dek, nil)

	return &EncryptedBlob{
		Ciphertext:     ciphertext,
		EncryptedDEK:   base64.StdEncoding.EncodeToString(encryptedDEK),
		Nonce:          base64.StdEncoding.EncodeToString(dataNonce),
		OriginalSize:   int64(len(plaintext)),
		EncryptionMode: ModeServer,
	}, nil
}

// Decrypt decrypts an encrypted blob using its metadata.
func (e *Encryptor) Decrypt(encrypted *EncryptedBlob) ([]byte, error) {
	if encrypted.EncryptionMode == ModeNone {
		// Not encrypted, return as-is
		return encrypted.Ciphertext, nil
	}

	if encrypted.EncryptionMode == ModeE2E {
		// E2E encrypted, server cannot decrypt - return ciphertext
		return encrypted.Ciphertext, nil
	}

	// Decode the encrypted DEK
	encryptedDEK, err := base64.StdEncoding.DecodeString(encrypted.EncryptedDEK)
	if err != nil {
		return nil, fmt.Errorf("failed to decode encrypted DEK: %w", err)
	}

	// The encrypted DEK contains: nonce (12 bytes) + encrypted key + auth tag
	if len(encryptedDEK) < e.kekGCM.NonceSize() {
		return nil, ErrDecryptionFailed
	}

	kekNonce := encryptedDEK[:e.kekGCM.NonceSize()]
	encryptedKey := encryptedDEK[e.kekGCM.NonceSize():]

	// Decrypt the DEK
	dek, err := e.kekGCM.Open(nil, kekNonce, encryptedKey, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	// Create cipher for data decryption
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, fmt.Errorf("failed to create data cipher: %w", err)
	}

	dataGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create data GCM: %w", err)
	}

	// Decode the data nonce
	dataNonce, err := base64.StdEncoding.DecodeString(encrypted.Nonce)
	if err != nil {
		return nil, fmt.Errorf("failed to decode nonce: %w", err)
	}

	// Decrypt the data
	plaintext, err := dataGCM.Open(nil, dataNonce, encrypted.Ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	return plaintext, nil
}

// IsEnabled returns true if the encryptor is configured and enabled.
func (e *Encryptor) IsEnabled() bool {
	return e != nil && len(e.kek) == 32
}
