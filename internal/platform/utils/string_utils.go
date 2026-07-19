package utils

import "fmt"

// MaskAPIKey masks an API key for safe logging.
func MaskAPIKey(key string) string {
	length := len(key)
	if length == 0 {
		return ""
	}
	if length <= 8 {
		return "****"
	}
	return fmt.Sprintf("%s****%s", key[:4], key[length-4:])
}
