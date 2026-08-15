package modules

import "fmt"

const bedrockCredentialValidator CredentialValidatorID = "bedrock_credential"

func awsBedrockModule() Module {
	return Module{
		Definition: Definition{
			ID:          AWSBedrock,
			Name:        "AWS Bedrock",
			Mark:        "BR",
			Icon:        "bedrock",
			SearchTerms: []string{"aws", "amazon", "sigv4", "iam"},
			Description: "Amazon Bedrock",
			Connection:  apiKeyConnection(),
			Params: []Field{{
				Key: "region", Label: "AWS region", InputKind: InputText,
				Required: true, Normalizer: normalizeCloudIdentifier,
			}},
			Credentials: []Field{
				secretField("api_key", "Bedrock API Key"),
				secretField("access_key", "AWS access key"),
				secretField("secret_key", "AWS secret key"),
				secretField("session_token", "AWS session token"),
				secretField("role_arn", "AWS role ARN"),
				secretField("external_id", "AWS external ID"),
				secretField("session_name", "AWS role session name"),
			},
			CredentialValidator: bedrockCredentialValidator,
			Provider: ProviderBinding{
				ProviderKind:      ProviderAWSBedrock,
				CatalogProviderID: "amazon-bedrock",
				EndpointPolicy:    EndpointCloudParams,
			},
			Routes: allConvertedRoutes(),
		},
		Extensions: Extensions{CredentialValidators: map[CredentialValidatorID]CredentialValidator{
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
