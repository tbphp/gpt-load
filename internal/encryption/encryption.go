package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
)

// Service defines the interface for encryption and decryption operations.
type Service interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertextHex string) (string, error)
}

type serviceImpl struct {
	key []byte
}

// NewService creates a new encryption service.
// The hexKey must be a 64-character hex-encoded string, representing a 32-byte key.
func NewService(hexKey string) (Service, error) {
	if hexKey == "" {
		// Return a disabled service instead of an error
		return &disabledService{}, nil
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode encryption key: %w. It must be a hex-encoded string", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes long (64 hex characters)")
	}
	return &serviceImpl{key: key}, nil
}

// Encrypt encrypts a plaintext string using AES-256-GCM.
func (s *serviceImpl) Encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a hex-encoded ciphertext string.
func (s *serviceImpl) Decrypt(ciphertextHex string) (string, error) {
	ciphertext, err := hex.DecodeString(ciphertextHex)
	if err != nil {
		return "", fmt.Errorf("failed to decode ciphertext: %w", err)
	}

	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}

	return string(plaintext), nil
}

// disabledService is a no-op implementation of the Service interface.
type disabledService struct{}

func (s *disabledService) Encrypt(plaintext string) (string, error) {
	return plaintext, nil
}

func (s *disabledService) Decrypt(ciphertext string) (string, error) {
	return ciphertext, nil
}
