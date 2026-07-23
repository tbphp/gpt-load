// Package encryption provides mandatory AES-256-GCM encryption and stable
// HMAC fingerprints for stored credentials.
package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"gpt-load/internal/platform/securefile"
	"gpt-load/internal/platform/utils"

	"github.com/sirupsen/logrus"
)

// KeyFileName is the persistent master-key filename within DATA_DIR.
const (
	KeyFileName          = "encryption.key"
	encryptionKeyDomain  = "gpt-load/encryption/aes-256-gcm/v1"
	fingerprintKeyDomain = "gpt-load/encryption/fingerprint-hmac/v1"
)

// Service defines credential encryption and fingerprinting operations.
type Service interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
	Hash(plaintext string) string
}

// NewService creates a mandatory AES-GCM service from non-empty key material.
func NewService(keyMaterial string) (Service, error) {
	if keyMaterial == "" {
		return nil, fmt.Errorf("encryption key material is required")
	}

	rootKey := utils.DeriveAESKey(keyMaterial)
	aesKey := deriveDomainKey(rootKey, encryptionKeyDomain)
	hashKey := deriveDomainKey(rootKey, fingerprintKeyDomain)
	utils.ValidatePasswordStrength(keyMaterial, "ENCRYPTION_KEY")

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	return &aesService{hashKey: hashKey, gcm: gcm}, nil
}

// NewServiceWithKeyFile resolves explicit key material or a persistent keyfile
// before constructing the encryption service.
func NewServiceWithKeyFile(explicitKey, dataDir string) (Service, error) {
	keyMaterial, err := LoadOrCreateKeyMaterial(explicitKey, dataDir)
	if err != nil {
		return nil, err
	}
	return NewService(keyMaterial)
}

// LoadOrCreateKeyMaterial prefers explicit key material. When it is absent, a
// 32-byte random key is loaded from or created at DATA_DIR/encryption.key.
func LoadOrCreateKeyMaterial(explicitKey, dataDir string) (string, error) {
	if explicitKey != "" {
		return explicitKey, nil
	}
	if dataDir == "" {
		return "", fmt.Errorf("DATA_DIR is required when ENCRYPTION_KEY is empty")
	}
	result, err := securefile.LoadOrCreateHex(dataDir, KeyFileName)
	if err != nil {
		return "", err
	}
	if result.Created {
		logrus.WithField("path", result.Path).
			Warn("Generated encryption keyfile; back it up before relying on encrypted credentials")
	}
	return result.Value, nil
}

type aesService struct {
	hashKey []byte
	gcm     cipher.AEAD
}

func deriveDomainKey(rootKey []byte, domain string) []byte {
	mac := hmac.New(sha256.New, rootKey)
	_, _ = mac.Write([]byte(domain))
	return mac.Sum(nil)
}

func (s *aesService) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate encryption nonce: %w", err)
	}
	ciphertext := s.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

func (s *aesService) Decrypt(ciphertext string) (string, error) {
	data, err := hex.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	if len(data) < s.gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, encrypted := data[:s.gcm.NonceSize()], data[s.gcm.NonceSize():]
	plaintext, err := s.gcm.Open(nil, nonce, encrypted, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt ciphertext: %w", err)
	}
	return string(plaintext), nil
}

func (s *aesService) Hash(plaintext string) string {
	if plaintext == "" {
		return ""
	}
	mac := hmac.New(sha256.New, s.hashKey)
	_, _ = mac.Write([]byte(plaintext))
	return hex.EncodeToString(mac.Sum(nil))
}
