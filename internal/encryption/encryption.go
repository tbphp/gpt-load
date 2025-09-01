package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"gpt-load/internal/types"
	"gpt-load/internal/utils"
	"io"
)

// Service defines the encryption interface
type Service interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// NewService creates encryption service
func NewService(configManager types.ConfigManager) (Service, error) {
	key := configManager.GetEncryptionKey()
	if key == "" {
		return &noopService{}, nil
	}

	// Derive AES-256 key from user input and validate strength
	aesKey := utils.DeriveAESKey(key)
	utils.ValidatePasswordStrength(key, "ENCRYPTION_KEY")

	return &aesService{key: aesKey}, nil
}

// aesService implements AES-256-GCM encryption
type aesService struct {
	key []byte
}

func (s *aesService) Encrypt(plaintext string) (string, error) {
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

func (s *aesService) Decrypt(ciphertext string) (string, error) {
	data, err := hex.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("invalid hex data: %w", err)
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
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, encrypted := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, encrypted, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed: %w", err)
	}

	return string(plaintext), nil
}

// noopService disables encryption
type noopService struct{}

func (s *noopService) Encrypt(plaintext string) (string, error) {
	return plaintext, nil
}

func (s *noopService) Decrypt(ciphertext string) (string, error) {
	return ciphertext, nil
}
