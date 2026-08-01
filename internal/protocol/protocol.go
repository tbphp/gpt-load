// Package protocol defines protocol identifiers shared by runtime domains.
package protocol

type Protocol string

const (
	OpenAICompletions Protocol = "openai-completions"
	OpenAIResponses   Protocol = "openai-responses"
	Anthropic         Protocol = "anthropic"
	Gemini            Protocol = "gemini"
)

func (p Protocol) Valid() bool {
	switch p {
	case OpenAICompletions, OpenAIResponses, Anthropic, Gemini:
		return true
	default:
		return false
	}
}

func (p Protocol) DataPlaneEnabled() bool {
	switch p {
	case OpenAICompletions, OpenAIResponses, Anthropic, Gemini:
		return true
	default:
		return false
	}
}

func (p Protocol) SupportsModelOptionalRequests() bool {
	return p == OpenAIResponses
}

func DataPlaneProtocols() []Protocol {
	return []Protocol{
		OpenAICompletions,
		OpenAIResponses,
		Anthropic,
		Gemini,
	}
}
