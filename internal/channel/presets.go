package channel

import (
	"encoding/json"
	"fmt"
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func builtinDefinitions() []definition {
	apiKeySchema := objectSchema{{
		descriptor: FieldDescriptor{
			Key: "api_key", Label: "API Key", InputKind: InputSecret,
			Required: true, Sensitive: true,
		},
		normalize: normalizeNonEmpty,
	}}
	compatibleParams := objectSchema{{
		descriptor: FieldDescriptor{
			Key: "base_url", Label: "Base URL", InputKind: InputURL,
			Required: true,
		},
		normalize: normalizeBaseURL,
	}}
	azureParams := objectSchema{{
		descriptor: FieldDescriptor{Key: "endpoint", Label: "Azure endpoint", InputKind: InputURL, Required: true},
		normalize:  normalizeBaseURL,
	}}
	azureCredentials := objectSchema{
		secretField("api_key", "API Key"),
		secretField("client_id", "Entra client ID"),
		secretField("client_secret", "Entra client secret"),
		secretField("tenant_id", "Entra tenant ID"),
	}
	bedrockParams := objectSchema{{
		descriptor: FieldDescriptor{Key: "region", Label: "AWS region", InputKind: InputText, Required: true},
		normalize:  normalizeCloudIdentifier,
	}}
	bedrockCredentials := objectSchema{
		secretField("api_key", "Bedrock API Key"),
		secretField("access_key", "AWS access key"),
		secretField("secret_key", "AWS secret key"),
		secretField("session_token", "AWS session token"),
		secretField("role_arn", "AWS role ARN"),
		secretField("external_id", "AWS external ID"),
		secretField("session_name", "AWS role session name"),
	}
	vertexParams := objectSchema{
		{
			descriptor: FieldDescriptor{Key: "project_id", Label: "Google Cloud project ID", InputKind: InputText, Required: true},
			normalize:  normalizeCloudIdentifier,
		},
		{
			descriptor: FieldDescriptor{Key: "location", Label: "Vertex location", InputKind: InputText, Required: true},
			normalize:  normalizeCloudIdentifier,
		},
		{
			descriptor: FieldDescriptor{Key: "project_number", Label: "Google Cloud project number", InputKind: InputText},
			normalize:  normalizeCloudIdentifier,
		},
	}
	vertexCredentials := objectSchema{{
		descriptor: FieldDescriptor{
			Key: "service_account_json", Label: "Service account JSON", InputKind: InputSecret,
			Required: true, Sensitive: true,
		},
		normalize: normalizeServiceAccountJSON,
	}}

	azureDefinition := newDefinition(
		AzureOpenAI, "Azure OpenAI", "AZ", "Azure OpenAI and Microsoft Foundry", []string{"azure", "foundry", "entra"},
		azureParams, azureCredentials, "azure", ProviderAzureOpenAI, false, nil, false,
	)
	azureDefinition.validateCredential = validateAzureCredential
	bedrockDefinition := newDefinition(
		AWSBedrock, "AWS Bedrock", "BR", "Amazon Bedrock", []string{"aws", "amazon", "sigv4", "iam"},
		bedrockParams, bedrockCredentials, "amazon-bedrock", ProviderAWSBedrock, false, nil, false,
	)
	bedrockDefinition.validateCredential = validateBedrockCredential
	vertexDefinition := newDefinition(
		GoogleVertex, "Google Vertex AI", "VX", "Google Cloud Vertex AI", []string{"google", "gcp", "vertex"},
		vertexParams, vertexCredentials, "google-vertex", ProviderGoogleVertex, false, nil, false,
	)

	return []definition{
		newDefinition(
			OpenAI, "OpenAI", "OA", "OpenAI official API", []string{"gpt"},
			nil, apiKeySchema, "openai", ProviderOpenAI, false,
			map[protocol.Protocol]bool{
				protocol.OpenAICompletions: true,
				protocol.OpenAIResponses:   true,
			},
			true,
		),
		newDefinition(
			Anthropic, "Anthropic", "AN", "Anthropic official API", []string{"claude"},
			nil, apiKeySchema, "anthropic", ProviderAnthropic, false,
			map[protocol.Protocol]bool{protocol.Anthropic: true}, false,
		),
		newDefinition(
			Gemini, "Google Gemini", "GE", "Google Gemini official API", []string{"google"},
			nil, apiKeySchema, "google", ProviderGemini, false,
			map[protocol.Protocol]bool{protocol.Gemini: true}, false,
		),
		azureDefinition,
		bedrockDefinition,
		vertexDefinition,
		newFixedCompatibleDefinition(
			DeepSeek, "DeepSeek", "DS", []string{"deep seek"},
			"https://api.deepseek.com/v1", "deepseek", apiKeySchema,
		),
		newFixedCompatibleDefinition(
			MoonshotAI, "Moonshot AI", "MS", []string{"kimi", "moonshot"},
			"https://api.moonshot.cn/v1", "moonshotai", apiKeySchema,
		),
		newFixedCompatibleDefinition(
			SiliconFlow, "SiliconFlow", "SF", []string{"silicon flow"},
			"https://api.siliconflow.cn/v1", "siliconflow", apiKeySchema,
		),
		newFixedCompatibleDefinition(
			ZhipuAI, "Zhipu AI", "ZP", []string{"glm", "bigmodel"},
			"https://open.bigmodel.cn/api/paas/v4", "zhipuai", apiKeySchema,
		),
		newFixedCompatibleDefinition(
			Alibaba, "Alibaba Cloud Bailian", "BL", []string{"dashscope", "qwen", "bailian"},
			"https://dashscope.aliyuncs.com/compatible-mode/v1", "alibaba", apiKeySchema,
		),
		newFixedCompatibleDefinition(
			Volcengine, "Volcengine Ark", "VE", []string{"doubao", "ark"},
			"https://ark.cn-beijing.volces.com/api/v3", "volcengine", apiKeySchema,
		),
		newFixedCompatibleDefinition(
			OpenRouter, "OpenRouter", "OR", []string{"router"},
			"https://openrouter.ai/api/v1", "openrouter", apiKeySchema,
		),
		newFixedCompatibleDefinition(
			Groq, "Groq", "GQ", []string{"groqcloud"},
			"https://api.groq.com/openai/v1", "groq", apiKeySchema,
		),
		newFixedCompatibleDefinition(
			XAI, "xAI", "XA", []string{"grok"},
			"https://api.x.ai/v1", "xai", apiKeySchema,
		),
		newDefinition(
			OpenAICompatible, "OpenAI Compatible", "OC", "Custom OpenAI-compatible API", []string{"custom", "proxy", "gateway"},
			compatibleParams, apiKeySchema, "", ProviderOpenAICompatible, true,
			map[protocol.Protocol]bool{protocol.OpenAICompletions: true},
			false,
		),
		newDefinition(
			AnthropicCompatible, "Anthropic Compatible", "AC", "Custom Anthropic-compatible API", []string{"custom", "proxy", "gateway", "claude"},
			compatibleParams, apiKeySchema, "", ProviderAnthropicCompatible, true,
			map[protocol.Protocol]bool{protocol.Anthropic: true}, false,
		),
		newDefinition(
			GeminiCompatible, "Gemini Compatible", "GC", "Custom Gemini-compatible API", []string{"custom", "proxy", "gateway", "google"},
			compatibleParams, apiKeySchema, "", ProviderGeminiCompatible, true,
			map[protocol.Protocol]bool{protocol.Gemini: true}, false,
		),
	}
}

func secretField(key, label string) fieldSpec {
	return fieldSpec{
		descriptor: FieldDescriptor{Key: key, Label: label, InputKind: InputSecret, Sensitive: true},
		normalize:  normalizeNonEmpty,
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
		return &ValidationError{Field: "credential", Reason: "must use either API key or Entra credentials"}
	}
	if !hasAPIKey && entraCount != len(entraFields) {
		return &ValidationError{Field: "credential", Reason: "requires an API key or complete Entra credentials"}
	}
	return nil
}

func validateBedrockCredential(values map[string]string) error {
	hasAPIKey := values["api_key"] != ""
	hasAccessKey := values["access_key"] != ""
	hasSecretKey := values["secret_key"] != ""
	hasRole := values["role_arn"] != ""
	hasSigV4Fields := hasAccessKey || hasSecretKey || hasRole || values["session_token"] != "" ||
		values["external_id"] != "" || values["session_name"] != ""
	if hasAPIKey && hasSigV4Fields {
		return &ValidationError{Field: "credential", Reason: "must use either API key or SigV4 credentials"}
	}
	if hasAPIKey {
		return nil
	}
	if hasAccessKey != hasSecretKey {
		return &ValidationError{Field: "credential", Reason: "access_key and secret_key must be provided together"}
	}
	if !hasAccessKey && !hasRole {
		return &ValidationError{Field: "credential", Reason: "requires an API key, access key pair, or role ARN"}
	}
	if values["session_token"] != "" && !hasAccessKey {
		return &ValidationError{Field: "credential.session_token", Reason: "requires access_key and secret_key"}
	}
	if (values["external_id"] != "" || values["session_name"] != "") && !hasRole {
		return &ValidationError{Field: "credential.role_arn", Reason: "required for role options"}
	}
	return nil
}

func normalizeCloudIdentifier(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 255 {
		return "", fmt.Errorf("must be between 1 and 255 bytes")
	}
	for _, character := range value {
		if character <= ' ' || character == 0x7f {
			return "", fmt.Errorf("must not contain whitespace or control characters")
		}
	}
	return value, nil
}

func normalizeServiceAccountJSON(value string) (string, error) {
	object, err := decodeStrictObject(json.RawMessage(strings.TrimSpace(value)))
	if err != nil {
		return "", fmt.Errorf("must be a valid JSON object")
	}
	for _, key := range []string{"type", "project_id", "client_email", "private_key"} {
		raw, exists := object[key]
		if !exists {
			return "", fmt.Errorf("must contain %s", key)
		}
		var field string
		if json.Unmarshal(raw, &field) != nil || strings.TrimSpace(field) == "" {
			return "", fmt.Errorf("must contain non-empty %s", key)
		}
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return "", fmt.Errorf("must be a valid JSON object")
	}
	return string(encoded), nil
}

func newFixedCompatibleDefinition(
	id ID,
	name string,
	mark string,
	searchTerms []string,
	baseURL string,
	catalogProviderID string,
	credentials objectSchema,
) definition {
	definition := newDefinition(
		id,
		name,
		mark,
		"Managed API preset",
		searchTerms,
		nil,
		credentials,
		catalogProviderID,
		ProviderOpenAICompatible,
		false,
		map[protocol.Protocol]bool{protocol.OpenAICompletions: true},
		false,
	)
	definition.fixedTargetConfig = canonicalJSON(map[string]string{"base_url": baseURL})
	return definition
}

func newDefinition(
	id ID,
	name string,
	mark string,
	description string,
	searchTerms []string,
	params objectSchema,
	credentials objectSchema,
	catalogProviderID string,
	providerKind ProviderKind,
	compatible bool,
	nativeProtocols map[protocol.Protocol]bool,
	responsesLifecycle bool,
) definition {
	capabilities, modes := buildRouteMatrix(nativeProtocols, responsesLifecycle)
	return definition{
		descriptor: Descriptor{
			ID:               id,
			Name:             name,
			Mark:             mark,
			Description:      description,
			ParamFields:      params.descriptors(),
			CredentialFields: credentials.descriptors(),
			ClientProtocols:  orderedProtocols(capabilities),
		},
		searchTerms:       append([]string(nil), searchTerms...),
		params:            append(objectSchema(nil), params...),
		credentials:       append(objectSchema(nil), credentials...),
		catalogProviderID: catalogProviderID,
		providerKind:      providerKind,
		compatible:        compatible,
		capabilities:      capabilities,
		modes:             modes,
	}
}

func buildRouteMatrix(
	nativeProtocols map[protocol.Protocol]bool,
	responsesLifecycle bool,
) (
	map[protocol.Protocol]execution.CapabilitySet,
	map[protocol.Protocol]map[execution.Operation]RouteMode,
) {
	capabilities := make(map[protocol.Protocol]execution.CapabilitySet)
	modes := make(map[protocol.Protocol]map[execution.Operation]RouteMode)
	for _, clientProtocol := range protocol.DataPlaneProtocols() {
		native := nativeProtocols[clientProtocol]
		mode := RouteConverted
		features := mustFeatureSet(execution.FeatureStreaming)
		if native {
			mode = RouteNative
			features = mustFeatureSet(
				execution.FeatureStreaming,
				execution.FeatureTools,
				execution.FeatureReasoning,
				execution.FeatureMultimodal,
				execution.FeatureStructuredOutput,
			)
		}
		operation := execution.OperationChatCompletion
		if clientProtocol == protocol.OpenAIResponses {
			operation = execution.OperationResponsesCreate
			if native && responsesLifecycle {
				features = mustFeatureSet(
					execution.FeatureStreaming,
					execution.FeatureTools,
					execution.FeatureReasoning,
					execution.FeatureMultimodal,
					execution.FeatureStructuredOutput,
					execution.FeatureNativeResourceSemantics,
				)
			}
		}

		items := []execution.Capability{
			{Operation: operation, Features: features},
			{Operation: execution.OperationListModels},
			{Operation: execution.OperationProbe},
		}
		operationModes := map[execution.Operation]RouteMode{
			operation:                     mode,
			execution.OperationListModels: mode,
			execution.OperationProbe:      mode,
		}
		if clientProtocol == protocol.OpenAIResponses && native && responsesLifecycle {
			nativeFeatures := mustFeatureSet(
				execution.FeatureTools,
				execution.FeatureReasoning,
				execution.FeatureMultimodal,
				execution.FeatureStructuredOutput,
			)
			for _, nativeOperation := range []execution.Operation{
				execution.OperationResponsesCompact,
				execution.OperationResponsesInputTokens,
			} {
				items = append(items, execution.Capability{
					Operation: nativeOperation,
					Features:  nativeFeatures,
				})
				operationModes[nativeOperation] = RouteNative
			}
			resourceFeatures := mustFeatureSet(execution.FeatureNativeResourceSemantics)
			for _, resourceOperation := range []execution.Operation{
				execution.OperationResponsesRetrieve,
				execution.OperationResponsesDelete,
				execution.OperationResponsesCancel,
				execution.OperationResponsesInputItems,
			} {
				items = append(items, execution.Capability{Operation: resourceOperation, Features: resourceFeatures})
				operationModes[resourceOperation] = RouteNative
			}
			items = append(items, execution.Capability{
				Operation: execution.OperationResponsesPassthrough,
				Features: mustFeatureSet(
					execution.FeatureStreaming,
					execution.FeatureNativeResourceSemantics,
				),
			})
			operationModes[execution.OperationResponsesPassthrough] = RouteNative
		}
		capabilities[clientProtocol] = mustCapabilitySet(items...)
		modes[clientProtocol] = operationModes
	}
	return capabilities, modes
}

func mustFeatureSet(features ...execution.Feature) execution.FeatureSet {
	set, err := execution.NewFeatureSet(features...)
	if err != nil {
		panic(fmt.Sprintf("build channel feature set: %v", err))
	}
	return set
}

func mustCapabilitySet(capabilities ...execution.Capability) execution.CapabilitySet {
	set, err := execution.NewCapabilitySet(capabilities...)
	if err != nil {
		panic(fmt.Sprintf("build channel capability set: %v", err))
	}
	return set
}

func orderedProtocols(capabilities map[protocol.Protocol]execution.CapabilitySet) []protocol.Protocol {
	ordered := make([]protocol.Protocol, 0, len(capabilities))
	for _, clientProtocol := range protocol.DataPlaneProtocols() {
		if _, ok := capabilities[clientProtocol]; ok {
			ordered = append(ordered, clientProtocol)
		}
	}
	return ordered
}
