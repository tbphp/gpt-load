package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func Modelflare() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.Modelflare,
			Name:        "Modelflare",
			Mark:        "MF",
			Icon:        "modelflare",
			SearchTerms: []string{"havenbyte"},
			Description: "Native multi-protocol API gateway",
			Partner: spec.Partner{
				Tier:      spec.PartnerPrincipal,
				SignupURL: "https://modelflare.dev/sign-up?utm_source=gptload&utm_campaign=gptload",
			},
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
				FixedBaseURL:   "https://modelflare.dev",
			},
			// 上游 Responses 只支持流式，非流式返回 400，所以不声明 Responses 凭据测试，
			// 分组默认用 Chat Completions 测试。Gemini 生图模型走 Gemini 原生 generateContent。
			// 上游未开放 Embeddings，Responses compact 实测失败，均不声明。
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
