package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func Cohere() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.Cohere,
			Name:        "Cohere",
			Mark:        "CO",
			Icon:        "cohere",
			SearchTerms: []string{"cohere ai", "rerank"},
			Description: "Cohere text rerank API",
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
				CatalogProviderID: "cohere",
				EndpointPolicy:    spec.EndpointFixedWithOverride,
				FixedBaseURL:      "https://api.cohere.ai/v2",
			},
			Routes: []spec.Route{
				spec.NewRoute(protocol.Rerank, execution.OperationRerank, execution.RouteNative),
				spec.NewRoute(protocol.Rerank, execution.OperationProbe, execution.RouteNative),
			},
		},
	}
}
