// Package protocol defines protocol identifiers shared by runtime domains.
package protocol

type Protocol string

const (
	OpenAIChatCompletions Protocol = "openai-chat-completions"
	OpenAIResponses       Protocol = "openai-responses"
	Anthropic             Protocol = "anthropic"
	Gemini                Protocol = "gemini"
)

func (p Protocol) Valid() bool {
	switch p {
	case OpenAIChatCompletions, OpenAIResponses, Anthropic, Gemini:
		return true
	default:
		return false
	}
}

func (p Protocol) DataPlaneEnabled() bool {
	switch p {
	case OpenAIChatCompletions, OpenAIResponses, Anthropic, Gemini:
		return true
	default:
		return false
	}
}

func DataPlaneProtocols() []Protocol {
	return []Protocol{
		OpenAIChatCompletions,
		OpenAIResponses,
		Anthropic,
		Gemini,
	}
}
