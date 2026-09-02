package modules

import (
	"fmt"

	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const azureCredentialValidator spec.CredentialValidatorID = "azure_credential"

func AzureOpenAI() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.AzureOpenAI,
			Name:        "Azure OpenAI",
			Mark:        "AZ",
			Icon:        "azure",
			SearchTerms: []string{"azure", "foundry", "entra"},
			Description: "Azure OpenAI and Microsoft Foundry",
			Connection: spec.Connection{
				Type:            spec.ConnectionAPIKey,
				CredentialInput: "batch_text",
			},
			Params: []spec.Field{{
				Key: "endpoint", Label: "Azure endpoint", InputKind: spec.InputURL,
				Required: true, Normalizer: spec.NormalizeBaseURL,
			}},
			Credentials: []spec.Field{
				{Key: "api_key", Label: "API Key", InputKind: spec.InputSecret, Sensitive: true, Normalizer: spec.NormalizeNonEmpty},
				{Key: "client_id", Label: "Entra client ID", InputKind: spec.InputSecret, Sensitive: true, Normalizer: spec.NormalizeNonEmpty},
				{Key: "client_secret", Label: "Entra client secret", InputKind: spec.InputSecret, Sensitive: true, Normalizer: spec.NormalizeNonEmpty},
				{Key: "tenant_id", Label: "Entra tenant ID", InputKind: spec.InputSecret, Sensitive: true, Normalizer: spec.NormalizeNonEmpty},
			},
			CredentialValidator: azureCredentialValidator,
			Provider: spec.ProviderBinding{
				ProviderKind:      spec.ProviderAzureOpenAI,
				CatalogProviderID: "azure",
				EndpointPolicy:    spec.EndpointCloudParams,
			},
			Routes: []spec.Route{
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationListModels, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationProbe, execution.RouteConverted),
				spec.NewResponsesCreateRoute(execution.RouteConverted, spec.ResponsesStoreHandlingStateless),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationProbe, execution.RouteConverted),
				spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.Anthropic, execution.OperationListModels, execution.RouteConverted),
				spec.NewRoute(protocol.Anthropic, execution.OperationProbe, execution.RouteConverted),
				spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.Gemini, execution.OperationListModels, execution.RouteConverted),
				spec.NewRoute(protocol.Gemini, execution.OperationProbe, execution.RouteConverted),
			},
		},
		Extensions: spec.Extensions{CredentialValidators: map[spec.CredentialValidatorID]spec.CredentialValidator{
			azureCredentialValidator: validateAzureCredential,
		}},
	}
}

func validateAzureCredential(values map[string]string) error {
	hasAPIKey := values["api_key"] != ""
	entraFields := []string{"client_id", "client_secret", "tenant_id"}
	entraCount := 0
	for _, field := range entraFields {
		if values[field] != "" {
			entraCount++
		}
	}
	if hasAPIKey && entraCount > 0 {
		return fmt.Errorf("must use either API key or Entra credentials")
	}
	if !hasAPIKey && entraCount != len(entraFields) {
		return fmt.Errorf("requires an API key or complete Entra credentials")
	}
	return nil
}
