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
	// GeminiDecisions 表示 Google Gemini 原生 Decisions（POST /v1beta/decisions），
	// 与 Jev 格式的 Decisions 是两套请求格式。
	GeminiDecisions Protocol = "gemini-decisions"
	// AudioSpeech 表示各渠道的 POST /v1/audio/speech。网关读取模型与原生流式开关，
	// 保留渠道原生的请求和响应格式。
	AudioSpeech Protocol = "audio-speech"
	// Mistral is the native surface for Mistral-only HTTP APIs. Chat,
	// embeddings, and speech stay on the shared protocols.
	Mistral Protocol = "mistral"
)

func (p Protocol) Valid() bool {
	switch p {
	case OpenAICompletions, OpenAIResponses, OpenAIImages, OpenAIEmbeddings, CodexLive, Rerank, Decisions,
		AudioSpeech, Anthropic, Gemini, GeminiEmbeddings, GeminiDecisions, Mistral:
		return true
	default:
		return false
	}
}

func (p Protocol) DataPlaneEnabled() bool {
	switch p {
	case OpenAICompletions, OpenAIResponses, OpenAIImages, OpenAIEmbeddings, CodexLive, Rerank, Decisions,
		AudioSpeech, Anthropic, Gemini, GeminiEmbeddings, GeminiDecisions, Mistral:
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
		GeminiDecisions,
		Mistral,
	}
}
