// Package protocol defines protocol identifiers shared by runtime domains.
package protocol

type Protocol string

const (
	OpenAICompletions Protocol = "openai-completions"
	OpenAIResponses   Protocol = "openai-responses"
	OpenAIImages      Protocol = "openai-images"
	OpenAIEmbeddings  Protocol = "openai-embeddings"
	CodexLive         Protocol = "codex-live"
	Rerank            Protocol = "rerank"
	Decisions         Protocol = "decisions"
	Anthropic         Protocol = "anthropic"
	Gemini            Protocol = "gemini"
	GeminiEmbeddings  Protocol = "gemini-embeddings"
	// AudioSpeech is POST /v1/audio/speech on any channel. The gateway reads
	// only the model; each channel keeps its native request and response.
	AudioSpeech Protocol = "audio-speech"
	// Mistral is the native surface for Mistral-only HTTP APIs. Chat,
	// embeddings, and speech stay on the shared protocols.
	Mistral Protocol = "mistral"
)

func (p Protocol) Valid() bool {
	switch p {
	case OpenAICompletions, OpenAIResponses, OpenAIImages, OpenAIEmbeddings, CodexLive, Rerank, Decisions,
		AudioSpeech, Anthropic, Gemini, GeminiEmbeddings, Mistral:
		return true
	default:
		return false
	}
}

func (p Protocol) DataPlaneEnabled() bool {
	switch p {
	case OpenAICompletions, OpenAIResponses, OpenAIImages, OpenAIEmbeddings, CodexLive, Rerank, Decisions,
		AudioSpeech, Anthropic, Gemini, GeminiEmbeddings, Mistral:
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
		OpenAIImages,
		OpenAIEmbeddings,
		CodexLive,
		Rerank,
		Decisions,
		AudioSpeech,
		Anthropic,
		Gemini,
		GeminiEmbeddings,
		Mistral,
	}
}
