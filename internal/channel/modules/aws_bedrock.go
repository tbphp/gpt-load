package modules

import (
	"fmt"

	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const bedrockCredentialValidator spec.CredentialValidatorID = "bedrock_credential"

func AWSBedrock() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.AWSBedrock,
			Name:        "AWS Bedrock",
			Mark:        "BR",
			Icon:        "bedrock",
			SearchTerms: []string{"aws", "amazon", "sigv4", "iam"},
			Description: "Amazon Bedrock",
			Connection: spec.Connection{
				Type:            spec.ConnectionAPIKey,
				CredentialInput: "batch_text",
			},
			Params: []spec.Field{{
				Key: "region", Label: "AWS region", InputKind: spec.InputText,
				Required: true, Normalizer: spec.NormalizeCloudIdentifier,
			}},
			Credentials: []spec.Field{
				{Key: "api_key", Label: "Bedrock API Key", InputKind: spec.InputSecret, Sensitive: true, Normalizer: spec.NormalizeNonEmpty},
				{Key: "access_key", Label: "AWS access key", InputKind: spec.InputSecret, Sensitive: true, Normalizer: spec.NormalizeNonEmpty},
				{Key: "secret_key", Label: "AWS secret key", InputKind: spec.InputSecret, Sensitive: true, Normalizer: spec.NormalizeNonEmpty},
				{Key: "session_token", Label: "AWS session token", InputKind: spec.InputSecret, Sensitive: true, Normalizer: spec.NormalizeNonEmpty},
				{Key: "role_arn", Label: "AWS role ARN", InputKind: spec.InputSecret, Sensitive: true, Normalizer: spec.NormalizeNonEmpty},
				{Key: "external_id", Label: "AWS external ID", InputKind: spec.InputSecret, Sensitive: true, Normalizer: spec.NormalizeNonEmpty},
				{Key: "session_name", Label: "AWS role session name", InputKind: spec.InputSecret, Sensitive: true, Normalizer: spec.NormalizeNonEmpty},
			},
			CredentialValidator: bedrockCredentialValidator,
			Provider: spec.ProviderBinding{
				ProviderKind:      spec.ProviderAWSBedrock,
				CatalogProviderID: "amazon-bedrock",
				EndpointPolicy:    spec.EndpointCloudParams,
			},
			Routes: []spec.Route{
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationListModels, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationProbe, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesCreate, execution.RouteConverted),
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
			bedrockCredentialValidator: validateBedrockCredential,
		}},
	}
}

func validateBedrockCredential(values map[string]string) error {
	hasAPIKey := values["api_key"] != ""
	hasAccessKey := values["access_key"] != ""
	hasSecretKey := values["secret_key"] != ""
	hasRole := values["role_arn"] != ""
	hasSigV4Fields := hasAccessKey || hasSecretKey || hasRole || values["session_token"] != "" ||
		values["external_id"] != "" || values["session_name"] != ""
	if hasAPIKey && hasSigV4Fields {
		return fmt.Errorf("must use either API key or SigV4 credentials")
	}
	if hasAPIKey {
		return nil
	}
	if hasAccessKey != hasSecretKey {
		return fmt.Errorf("access_key and secret_key must be provided together")
	}
	if !hasAccessKey && !hasRole {
		return fmt.Errorf("requires an API key, access key pair, or role ARN")
	}
	if values["session_token"] != "" && !hasAccessKey {
		return fmt.Errorf("session_token requires access_key and secret_key")
	}
	if (values["external_id"] != "" || values["session_name"] != "") && !hasRole {
		return fmt.Errorf("role_arn is required for role options")
	}
	return nil
}
