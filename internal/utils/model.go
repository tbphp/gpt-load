package utils

import "strings"

// CleanGeminiModelName removes "models/" prefix from Gemini model names
// This is used for consistent model name handling across Gemini native and OpenAI-compatible formats
func CleanGeminiModelName(model string) string {
	return strings.TrimPrefix(model, "models/")
}
