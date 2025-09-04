package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"gpt-load/internal/types"
	"gpt-load/internal/utils"
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

	// Create cipher block once and reuse
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	return &aesService{
		key:   aesKey,
		block: block,
	}, nil
}

// aesService implements deterministic AES-256-CTR encryption with HMAC
type aesService struct {
	key   []byte
	block cipher.Block
}

// deriveIV generates a deterministic IV from the plaintext
func (s *aesService) deriveIV(plaintext []byte) []byte {
	// Use HMAC to generate deterministic IV
	mac := hmac.New(sha256.New, s.key)
	mac.Write(plaintext)
	hash := mac.Sum(nil)
	// Use first 16 bytes as IV
	return hash[:aes.BlockSize]
}

// computeHMAC calculates HMAC-SHA256 for integrity
func (s *aesService) computeHMAC(data []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write(data)
	return mac.Sum(nil)
}

func (s *aesService) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", fmt.Errorf("plaintext cannot be empty")
	}

	plaintextBytes := []byte(plaintext)

	// Generate deterministic IV from plaintext
	iv := s.deriveIV(plaintextBytes)

	// Create CTR stream
	stream := cipher.NewCTR(s.block, iv)

	// Encrypt (CTR mode doesn't need padding)
	encrypted := make([]byte, len(plaintextBytes))
	stream.XORKeyStream(encrypted, plaintextBytes)

	// Compute HMAC for integrity (IV + ciphertext)
	macData := append(iv, encrypted...)
	mac := s.computeHMAC(macData)

	// Combine: IV + encrypted + HMAC
	result := append(macData, mac...)

	return hex.EncodeToString(result), nil
}

func (s *aesService) Decrypt(ciphertext string) (string, error) {
	data, err := hex.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("invalid hex data: %w", err)
	}

	// Minimum length: IV (16) + at least 1 byte + HMAC (32)
	minLen := aes.BlockSize + 1 + sha256.Size
	if len(data) < minLen {
		return "", fmt.Errorf("ciphertext too short")
	}

	// Split components
	iv := data[:aes.BlockSize]
	macStart := len(data) - sha256.Size
	encrypted := data[aes.BlockSize:macStart]
	receivedMAC := data[macStart:]

	// Verify HMAC (IV + ciphertext)
	macData := data[:macStart]
	expectedMAC := s.computeHMAC(macData)
	if !hmac.Equal(receivedMAC, expectedMAC) {
		return "", fmt.Errorf("HMAC verification failed")
	}

	// Create CTR stream with the IV
	stream := cipher.NewCTR(s.block, iv)

	// Decrypt
	decrypted := make([]byte, len(encrypted))
	stream.XORKeyStream(decrypted, encrypted)

	return string(decrypted), nil
}

// noopService disables encryption
type noopService struct{}

func (s *noopService) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", fmt.Errorf("plaintext cannot be empty")
	}
	return plaintext, nil
}

func (s *noopService) Decrypt(ciphertext string) (string, error) {
	return ciphertext, nil
}
