package utils

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// PasswordStrength represents the strength level of a password
type PasswordStrength int

const (
	PasswordWeak PasswordStrength = iota
	PasswordMedium
	PasswordStrong
	PasswordVeryStrong
)

// String returns the string representation of password strength
func (ps PasswordStrength) String() string {
	switch ps {
	case PasswordWeak:
		return "弱"
	case PasswordMedium:
		return "中等"
	case PasswordStrong:
		return "强"
	case PasswordVeryStrong:
		return "非常强"
	default:
		return "未知"
	}
}

// Color returns the color code for UI display
func (ps PasswordStrength) Color() string {
	switch ps {
	case PasswordWeak:
		return "red"
	case PasswordMedium:
		return "orange"
	case PasswordStrong:
		return "yellow"
	case PasswordVeryStrong:
		return "green"
	default:
		return "gray"
	}
}

// PasswordValidationResult represents the result of password validation
type PasswordValidationResult struct {
	Strength   PasswordStrength `json:"strength"`
	Score      int              `json:"score"`
	IsValid    bool             `json:"is_valid"`
	Message    string           `json:"message"`
	Suggestions []string        `json:"suggestions"`
}

// CheckPasswordStrength analyzes password strength and returns validation result
func CheckPasswordStrength(password string) PasswordValidationResult {
	result := PasswordValidationResult{
		Score:       0,
		Suggestions: make([]string, 0),
	}

	// Check minimum length
	if len(password) < 8 {
		result.Suggestions = append(result.Suggestions, "密码长度至少需要8个字符")
	} else if len(password) >= 12 {
		result.Score += 2
	} else {
		result.Score += 1
	}

	// Check for lowercase letters
	if matched, _ := regexp.MatchString("[a-z]", password); matched {
		result.Score += 1
	} else {
		result.Suggestions = append(result.Suggestions, "添加小写字母")
	}

	// Check for uppercase letters
	if matched, _ := regexp.MatchString("[A-Z]", password); matched {
		result.Score += 1
	} else {
		result.Suggestions = append(result.Suggestions, "添加大写字母")
	}

	// Check for digits
	if matched, _ := regexp.MatchString("[0-9]", password); matched {
		result.Score += 1
	} else {
		result.Suggestions = append(result.Suggestions, "添加数字")
	}

	// Check for special characters
	if matched, _ := regexp.MatchString(`[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?~` + "`]", password); matched {
		result.Score += 2
	} else {
		result.Suggestions = append(result.Suggestions, "添加特殊字符 (!@#$%^&* 等)")
	}

	// Bonus for length
	if len(password) >= 16 {
		result.Score += 1
	}

	// Check for common patterns (deduct points)
	commonPatterns := []string{
		"123456", "password", "qwerty", "admin", "root", "test",
		"111111", "000000", "abc123", "password123",
	}

	passwordLower := strings.ToLower(password)
	for _, pattern := range commonPatterns {
		if strings.Contains(passwordLower, pattern) {
			result.Score -= 2
			result.Suggestions = append(result.Suggestions, "避免使用常见密码模式")
			break
		}
	}

	// Determine strength based on score
	switch {
	case result.Score < 3:
		result.Strength = PasswordWeak
	case result.Score < 5:
		result.Strength = PasswordMedium
	case result.Score < 7:
		result.Strength = PasswordStrong
	default:
		result.Strength = PasswordVeryStrong
	}

	// Set validation result
	result.IsValid = result.Strength >= PasswordMedium && len(password) >= 8

	if result.IsValid {
		result.Message = fmt.Sprintf("密码强度: %s", result.Strength.String())
	} else {
		result.Message = "密码强度不足，请按建议修改"
	}

	return result
}

// HashPassword creates a bcrypt hash of the password
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// CheckPasswordHash compares a password with its hash
func CheckPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// GenerateSecureToken generates a secure random token
func GenerateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	// Convert to hex string
	token := hex.EncodeToString(bytes)

	// Truncate to desired length if necessary
	if len(token) > length {
		token = token[:length]
	}

	return token, nil
}

// HashSHA256 creates a SHA256 hash of the input string
func HashSHA256(input string) string {
	hash := sha256.Sum256([]byte(input))
	return hex.EncodeToString(hash[:])
}
