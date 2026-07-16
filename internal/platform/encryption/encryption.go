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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gpt-load/internal/platform/utils"

	"github.com/sirupsen/logrus"
)

// KeyFileName is the persistent master-key filename within DATA_DIR.
const (
	KeyFileName             = "encryption.key"
	encodedKeyMaterialBytes = 64
	temporaryFileAttempts   = 10
	encryptionKeyDomain     = "gpt-load/encryption/aes-256-gcm/v1"
	fingerprintKeyDomain    = "gpt-load/encryption/fingerprint-hmac/v1"
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
	return loadOrCreateKeyMaterial(explicitKey, dataDir, syncParentDirectory)
}

func loadOrCreateKeyMaterial(explicitKey, dataDir string, syncDirectory func(string) error) (string, error) {
	if explicitKey != "" {
		return explicitKey, nil
	}

	if dataDir == "" {
		return "", fmt.Errorf("DATA_DIR is required when ENCRYPTION_KEY is empty")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", fmt.Errorf("create data directory: %w", err)
	}

	path := filepath.Join(dataDir, KeyFileName)
	if material, err := loadDurableKeyMaterial(path, syncDirectory); err == nil {
		return material, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	randomKey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, randomKey); err != nil {
		return "", fmt.Errorf("generate encryption key: %w", err)
	}
	material := hex.EncodeToString(randomKey)

	if err := persistGeneratedKeyMaterial(path, material, publishSecureKeyFile, syncDirectory); err != nil {
		if errors.Is(err, os.ErrExist) {
			return loadDurableKeyMaterial(path, syncDirectory)
		}
		return "", err
	}

	logrus.WithField("path", path).Warn("Generated encryption keyfile; back it up before relying on encrypted credentials")
	return material, nil
}

func persistGeneratedKeyMaterial(
	path string,
	material string,
	publish func(temporaryPath, finalPath string) error,
	syncDirectory func(string) error,
) error {
	file, temporaryPath, err := createSecureTemporaryKeyFile(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("create temporary encryption keyfile: %w", err)
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	contents := material + "\n"
	if written, err := file.WriteString(contents); err != nil || written != len(contents) {
		cleanupKeyFile(file, temporaryPath)
		if err == nil {
			err = io.ErrShortWrite
		}
		return fmt.Errorf("write temporary encryption keyfile: %w", err)
	}
	if err := file.Sync(); err != nil {
		cleanupKeyFile(file, temporaryPath)
		return fmt.Errorf("sync temporary encryption keyfile: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary encryption keyfile: %w", err)
	}
	if err := secureKeyFile(temporaryPath); err != nil {
		return fmt.Errorf("secure temporary encryption keyfile: %w", err)
	}
	if err := publish(temporaryPath, path); err != nil {
		return fmt.Errorf("publish encryption keyfile: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove temporary encryption keyfile: %w", err)
	}
	removeTemporary = false
	if err := syncDirectory(path); err != nil {
		return fmt.Errorf("sync encryption keyfile directory: %w", err)
	}
	return nil
}

func createSecureTemporaryKeyFile(dataDir string) (*os.File, string, error) {
	for range temporaryFileAttempts {
		suffix := make([]byte, 16)
		if _, err := io.ReadFull(rand.Reader, suffix); err != nil {
			return nil, "", fmt.Errorf("generate temporary keyfile name: %w", err)
		}
		path := filepath.Join(dataDir, "."+KeyFileName+"."+hex.EncodeToString(suffix)+".tmp")
		file, err := createSecureKeyFile(path)
		if err == nil {
			return file, path, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("could not allocate a unique temporary keyfile")
}

func loadDurableKeyMaterial(path string, syncDirectory func(string) error) (string, error) {
	material, err := readKeyFile(path)
	if err != nil {
		return "", err
	}
	if err := syncDirectory(path); err != nil {
		return "", fmt.Errorf("sync encryption keyfile directory: %w", err)
	}
	return material, nil
}

func readKeyFile(path string) (string, error) {
	if err := requireRegularKeyFile(path); err != nil {
		return "", err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	material := strings.TrimSpace(string(contents))
	if len(material) != encodedKeyMaterialBytes {
		return "", fmt.Errorf("encryption keyfile %s must contain exactly 64 hex characters", path)
	}
	decoded, err := hex.DecodeString(material)
	if err != nil {
		return "", fmt.Errorf("encryption keyfile %s contains invalid hex: %w", path, err)
	}
	if len(decoded) != 32 {
		return "", fmt.Errorf("encryption keyfile %s must decode to 32 bytes", path)
	}
	if err := secureKeyFile(path); err != nil {
		return "", fmt.Errorf("secure encryption keyfile: %w", err)
	}
	return material, nil
}

func requireRegularKeyFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("encryption keyfile %s must be a regular file", path)
	}
	return nil
}

func cleanupKeyFile(file *os.File, path string) {
	_ = file.Close()
	_ = os.Remove(path)
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
