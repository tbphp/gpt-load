// Package protocol defines protocol identifiers shared by runtime domains.
package protocol

type Protocol string

const (
	OpenAI         Protocol = "openai"
	Anthropic      Protocol = "anthropic"
	Gemini         Protocol = "gemini"
	OpenAIResponse Protocol = "openai-response"
)

func (p Protocol) Valid() bool {
	switch p {
	case OpenAI, Anthropic, Gemini, OpenAIResponse:
		return true
	default:
		return false
	}
}
