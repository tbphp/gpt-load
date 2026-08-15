package modules

import "fmt"

const azureCredentialValidator CredentialValidatorID = "azure_credential"

func azureOpenAIModule() Module {
	return Module{
		Definition: Definition{
			ID:          AzureOpenAI,
			Name:        "Azure OpenAI",
			Mark:        "AZ",
			Icon:        "azure",
			SearchTerms: []string{"azure", "foundry", "entra"},
			Description: "Azure OpenAI and Microsoft Foundry",
			Connection:  apiKeyConnection(),
			Params: []Field{{
				Key: "endpoint", Label: "Azure endpoint", InputKind: InputURL,
				Required: true, Normalizer: normalizeBaseURL,
			}},
			Credentials: []Field{
				secretField("api_key", "API Key"),
				secretField("client_id", "Entra client ID"),
				secretField("client_secret", "Entra client secret"),
				secretField("tenant_id", "Entra tenant ID"),
			},
			CredentialValidator: azureCredentialValidator,
			Provider: ProviderBinding{
				ProviderKind:      ProviderAzureOpenAI,
				CatalogProviderID: "azure",
				EndpointPolicy:    EndpointCloudParams,
			},
			Routes: allConvertedRoutes(),
		},
		Extensions: Extensions{CredentialValidators: map[CredentialValidatorID]CredentialValidator{
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
