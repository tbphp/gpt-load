package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func Modelink() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.Modelink,
			Name:        "Modelink (Qiniu Cloud)",
			Mark:        "QN",
			SearchTerms: []string{"qiniu", "七牛云", "七牛", "qnaigc"},
			Description: "Qiniu Cloud's native multi-protocol AI gateway",
			Partner:     spec.Partner{Tier: spec.PartnerFeatured, SignupURL: "https://www.qiniu.com/ai/"},
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
				ProviderKind:   spec.ProviderMultiProtocolGateway,
				EndpointPolicy: spec.EndpointFixedWithOverride,
				FixedBaseURL:   "https://api.qnaigc.com",
				DefaultBaseURLs: []string{
					"https://api.qnaigc.com",
					"https://api.modelink.ai",
				},
			},
			Routes: []spec.Route{
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationListModels, execution.RouteNative),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationProbe, execution.RouteNative),
				spec.NewResponsesCreateRoute(execution.RouteNative, spec.ResponsesStoreHandlingStateless),
				spec.NewRoute(protocol.OpenAIImages, execution.OperationImagesGenerate, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIImages, execution.OperationImagesEdit, execution.RouteNative),
				spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.Anthropic, execution.OperationProbe, execution.RouteNative),
				spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.Gemini, execution.OperationProbe, execution.RouteNative),
			},
		},
	}
}
