package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func Mistral() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.Mistral,
			Name:        "Mistral",
			Mark:        "MI",
			Icon:        "mistral",
			SearchTerms: []string{"mistral ai", "codestral", "voxtral", "ocr"},
			Description: "Chat, embeddings, OCR, audio, voices, moderation, batch, agents, and conversations",
			Connection: spec.Connection{
				Type:            spec.ConnectionAPIKey,
				CredentialInput: "batch_text",
			},
			Params: []spec.Field{{
				Key: "base_url", Label: "Base URL", InputKind: spec.InputURL,
				Normalizer: spec.NormalizeBaseURL,
			}},
			Credentials: []spec.Field{{
				Key: "api_key", Label: "API Key", InputKind: spec.InputSecret,
				Required: true, Sensitive: true, Normalizer: spec.NormalizeNonEmpty,
			}},
			Provider: spec.ProviderBinding{
				ProviderKind:      spec.ProviderOpenAICompatible,
				CatalogProviderID: "mistral",
				EndpointPolicy:    spec.EndpointFixedWithOverride,
				FixedBaseURL:      "https://api.mistral.ai/v1",
			},
			Routes: []spec.Route{
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationListModels, execution.RouteNative),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationProbe, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIEmbeddings, execution.OperationEmbeddingsCreate, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIEmbeddings, execution.OperationProbe, execution.RouteNative),
				spec.NewResponsesCreateRoute(execution.RouteConverted, spec.ResponsesStoreHandlingStateless),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationProbe, execution.RouteConverted),
				spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.Anthropic, execution.OperationListModels, execution.RouteConverted),
				spec.NewRoute(protocol.Anthropic, execution.OperationProbe, execution.RouteConverted),
				spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.Gemini, execution.OperationListModels, execution.RouteConverted),
				spec.NewRoute(protocol.Gemini, execution.OperationProbe, execution.RouteConverted),
				spec.NewRoute(protocol.Mistral, execution.OperationMistralOCR, execution.RouteNative),
				spec.NewRoute(protocol.Mistral, execution.OperationMistralFIM, execution.RouteNative),
				spec.NewRoute(protocol.Mistral, execution.OperationMistralAudioTranscription, execution.RouteNative),
				spec.NewRoute(protocol.Mistral, execution.OperationMistralAudioSpeech, execution.RouteNative),
				spec.NewRoute(protocol.Mistral, execution.OperationMistralModeration, execution.RouteNative),
				spec.NewRoute(protocol.Mistral, execution.OperationMistralChatModeration, execution.RouteNative),
				spec.NewRoute(protocol.Mistral, execution.OperationMistralClassification, execution.RouteNative),
				spec.NewRoute(protocol.Mistral, execution.OperationMistralFiles, execution.RouteNative),
				spec.NewRoute(protocol.Mistral, execution.OperationMistralBatch, execution.RouteNative),
				spec.NewRoute(protocol.Mistral, execution.OperationMistralAgents, execution.RouteNative),
				spec.NewRoute(protocol.Mistral, execution.OperationMistralConversations, execution.RouteNative),
				spec.NewRoute(protocol.Mistral, execution.OperationMistralVoices, execution.RouteNative),
				spec.NewRoute(protocol.Mistral, execution.OperationMistralRealtimeTranscription, execution.RouteNative),
			},
		},
	}
}
