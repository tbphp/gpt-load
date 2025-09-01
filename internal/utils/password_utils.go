package utils

import (
	"crypto/sha256"
	"strings"

	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/pbkdf2"
)

// ValidatePasswordStrength validates password strength with fixed minimum length of 16 characters
func ValidatePasswordStrength(password, fieldName string) {
	if len(password) < 16 {
		logrus.Warnf("%s is shorter than 16 characters, consider using a longer password", fieldName)
	}

	lower := strings.ToLower(password)
	weakPatterns := []string{"password", "sk-123456", "123456", "admin", "secret"}

	for _, pattern := range weakPatterns {
		if strings.Contains(lower, pattern) {
			logrus.Warnf("%s contains common weak patterns, consider using a stronger password", fieldName)
			break
		}
	}
}

// DeriveAESKey derives a 32-byte AES key from password using PBKDF2
func DeriveAESKey(password string) []byte {
	salt := []byte("gpt-load-encryption-v1")
	return pbkdf2.Key([]byte(password), salt, 100000, 32, sha256.New)
}
