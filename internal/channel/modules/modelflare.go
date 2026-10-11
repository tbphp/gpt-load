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
			// 只声明实测可用的协议：上游 Responses 非流式返回 400、Gemini 原生不可用，
			// 图片接口需要媒体分组密钥尚未验证，修复或验证后再补。
			Routes: []spec.Route{
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationListModels, execution.RouteNative),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationProbe, execution.RouteNative),
				spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.Anthropic, execution.OperationProbe, execution.RouteNative),
			},
		},
	}
}
