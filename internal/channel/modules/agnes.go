package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func Agnes() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.Agnes,
			Name:        "Agnes AI",
			Mark:        "AG",
			Icon:        "agnes",
			SearchTerms: []string{"agnes", "video"},
			Description: "Agnes AI video generation API",
			Connection: spec.Connection{
				Type:            spec.ConnectionAPIKey,
				CredentialInput: "batch_text",
			},
			Params: []spec.Field{{
				Key: "base_url", Label: "Base URL", InputKind: spec.InputURL,
				Required: true, Normalizer: spec.NormalizeBaseURL,
			}},
			Credentials: []spec.Field{{
				Key: "api_key", Label: "API Key", InputKind: spec.InputSecret,
				Required: true, Sensitive: true, Normalizer: spec.NormalizeNonEmpty,
			}},
			Provider: spec.ProviderBinding{
				ProviderKind:   spec.ProviderOpenAICompatible,
				EndpointPolicy: spec.EndpointRequiredBaseURL,
			},
			Routes: []spec.Route{
				spec.NewRoute(protocol.OpenAIVideos, execution.OperationVideosCreate, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIVideos, execution.OperationVideosRetrieve, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIVideos, execution.OperationVideosContent, execution.RouteNative),
			},
		},
	}
}
