package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func Jev() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.Jev,
			Name:        "Jev",
			Mark:        "JV",
			Icon:        "jev",
			SearchTerms: []string{"typesafe", "system one", "decisions", "vercel", "ai gateway"},
			Description: "TypeSafe Jev official API and Vercel AI Gateway",
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
				ProviderKind:      spec.ProviderJev,
				CatalogProviderID: "jev",
				EndpointPolicy:    spec.EndpointFixedWithOverride,
				FixedBaseURL:      "https://api.typesafe.ai/v1",
				DefaultBaseURLs:   []string{"https://api.typesafe.ai/v1", "https://ai-gateway.vercel.sh/typesafe/v1"},
			},
			Routes: []spec.Route{
				spec.NewRoute(protocol.Decisions, execution.OperationDecisionsCreate, execution.RouteNative),
				spec.NewRoute(protocol.Decisions, execution.OperationListModels, execution.RouteNative),
				spec.NewRoute(protocol.Decisions, execution.OperationProbe, execution.RouteNative),
			},
		},
	}
}
