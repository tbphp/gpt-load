package channel

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestRegistryHasStableBuiltInOrderAndSearch(t *testing.T) {
	registry := NewRegistry()
	wantIDs := []ID{
		OpenAI,
		Anthropic,
		Gemini,
		ID("azure_openai"),
		ID("aws_bedrock"),
		ID("google_vertex"),
		DeepSeek,
		MoonshotAI,
		SiliconFlow,
		ZhipuAI,
		Alibaba,
		Volcengine,
		OpenRouter,
		Groq,
		XAI,
		OpenAICompatible,
		AnthropicCompatible,
		GeminiCompatible,
	}
	if got := descriptorIDs(registry.List()); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("List() IDs = %v, want %v", got, wantIDs)
	}
	if got := descriptorIDs(registry.Search("GeMiNi")); !reflect.DeepEqual(got, []ID{Gemini, GeminiCompatible}) {
		t.Fatalf("Search(gemini) IDs = %v", got)
	}
	if got := descriptorIDs(registry.Search("compatible")); !reflect.DeepEqual(got, []ID{OpenAICompatible, AnthropicCompatible, GeminiCompatible}) {
		t.Fatalf("Search(compatible) IDs = %v", got)
	}
	if got := registry.Search("does-not-exist"); len(got) != 0 {
		t.Fatalf("Search(unknown) = %#v, want empty", got)
	}
	if got := descriptorIDs(registry.Search("deep")); !reflect.DeepEqual(got, []ID{DeepSeek}) {
		t.Fatalf("Search(deep) IDs = %v, want [%s]", got, DeepSeek)
	}
	if got := descriptorIDs(registry.Search("azure")); !reflect.DeepEqual(got, []ID{ID("azure_openai")}) {
		t.Fatalf("Search(azure) IDs = %v", got)
	}
	if got := descriptorIDs(registry.Search("aws")); !reflect.DeepEqual(got, []ID{ID("aws_bedrock")}) {
		t.Fatalf("Search(aws) IDs = %v", got)
	}
	if got := descriptorIDs(registry.Search("vertex")); !reflect.DeepEqual(got, []ID{ID("google_vertex")}) {
		t.Fatalf("Search(vertex) IDs = %v", got)
	}

	first := registry.List()
	first[0].ClientProtocols[0] = protocol.Protocol("mutated")
	first[0].ParamFields = append(first[0].ParamFields, FieldDescriptor{Key: "mutated"})
	second := registry.List()
	if second[0].ClientProtocols[0] == protocol.Protocol("mutated") || len(second[0].ParamFields) != 1 {
		t.Fatal("mutating List() output changed registry state")
	}
}

func TestRegistryPublicDescriptorsContainSchemasButNoInternalOrSecretValues(t *testing.T) {
	registry := NewRegistry()
	official, ok := registry.Get(OpenAI)
	if !ok {
		t.Fatal("Get(openai) missing")
	}
	if len(official.ParamFields) != 1 || len(official.CredentialFields) != 1 {
		t.Fatalf("openai schemas = params %#v credentials %#v", official.ParamFields, official.CredentialFields)
	}
	if field := official.ParamFields[0]; field.Key != "base_url" || field.InputKind != InputURL || field.Required || field.Sensitive {
		t.Fatalf("openai base URL override field = %#v", field)
	}
	credentialField := official.CredentialFields[0]
	if credentialField.Key != "api_key" || credentialField.InputKind != InputSecret || !credentialField.Required || !credentialField.Sensitive {
		t.Fatalf("openai credential field = %#v", credentialField)
	}
	compatible, ok := registry.Get(OpenAICompatible)
	if !ok || len(compatible.ParamFields) != 1 {
		t.Fatalf("Get(openai_compatible) = %#v, %t", compatible, ok)
	}
	if field := compatible.ParamFields[0]; field.Key != "base_url" || field.InputKind != InputURL || !field.Required || field.Sensitive {
		t.Fatalf("openai compatible param field = %#v", field)
	}

	encoded, err := json.Marshal(compatible)
	if err != nil {
		t.Fatalf("json.Marshal(descriptor) error = %v", err)
	}
	for _, forbidden := range []string{"target_config", "catalog_provider", "secret-value", "default_value"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("public descriptor leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestRegistryReturnsExactCatalogProviderMappingWithoutResolvingParams(t *testing.T) {
	registry := NewRegistry()
	for id, want := range map[ID]string{
		OpenAI: "openai", Anthropic: "anthropic", Gemini: "google",
		ID("azure_openai"): "azure", ID("aws_bedrock"): "amazon-bedrock", ID("google_vertex"): "google-vertex",
		OpenAICompatible: "", AnthropicCompatible: "", GeminiCompatible: "",
	} {
		got, ok := registry.CatalogProviderID(id)
		if !ok || got != want {
			t.Errorf("CatalogProviderID(%q) = %q, %t, want %q, true", id, got, ok, want)
		}
	}
	if got, ok := registry.CatalogProviderID(ID("missing")); ok || got != "" {
		t.Fatalf("CatalogProviderID(missing) = %q, %t, want empty, false", got, ok)
	}
}

func TestRegistryValidatesCloudChannelParamsAndStructuredCredentials(t *testing.T) {
	registry := NewRegistry()
	tests := []struct {
		name               string
		channelID          ID
		params             string
		credential         string
		wantParams         string
		wantCredential     string
		invalidCredentials []string
	}{
		{
			name: "Azure API key", channelID: ID("azure_openai"),
			params:         `{"endpoint":" HTTPS://Example.OPENAI.Azure.COM/ "}`,
			credential:     `{"api_key":" azure-secret "}`,
			wantParams:     `{"endpoint":"https://example.openai.azure.com"}`,
			wantCredential: `{"api_key":"azure-secret"}`,
			invalidCredentials: []string{
				`{}`,
				`{"client_id":"client","tenant_id":"tenant"}`,
				`{"api_key":"key","client_id":"client","client_secret":"secret","tenant_id":"tenant"}`,
			},
		},
		{
			name: "Azure Entra", channelID: ID("azure_openai"),
			params:         `{"endpoint":"https://resource.services.ai.azure.com"}`,
			credential:     `{"client_id":" client ","client_secret":" secret ","tenant_id":" tenant "}`,
			wantParams:     `{"endpoint":"https://resource.services.ai.azure.com"}`,
			wantCredential: `{"client_id":"client","client_secret":"secret","tenant_id":"tenant"}`,
		},
		{
			name: "Bedrock API key", channelID: ID("aws_bedrock"),
			params:         `{"region":" us-east-1 "}`,
			credential:     `{"api_key":" bedrock-secret "}`,
			wantParams:     `{"region":"us-east-1"}`,
			wantCredential: `{"api_key":"bedrock-secret"}`,
			invalidCredentials: []string{
				`{}`,
				`{"access_key":"access"}`,
				`{"secret_key":"secret"}`,
				`{"api_key":"key","access_key":"access","secret_key":"secret"}`,
			},
		},
		{
			name: "Bedrock SigV4", channelID: ID("aws_bedrock"),
			params:         `{"region":"eu-west-1"}`,
			credential:     `{"access_key":" access ","secret_key":" secret ","session_token":" token ","role_arn":" arn:aws:iam::123456789012:role/test "}`,
			wantParams:     `{"region":"eu-west-1"}`,
			wantCredential: `{"access_key":"access","role_arn":"arn:aws:iam::123456789012:role/test","secret_key":"secret","session_token":"token"}`,
		},
		{
			name: "Bedrock ambient role", channelID: ID("aws_bedrock"),
			params:         `{"region":"ap-southeast-1"}`,
			credential:     `{"role_arn":" arn:aws:iam::123456789012:role/runtime "}`,
			wantParams:     `{"region":"ap-southeast-1"}`,
			wantCredential: `{"role_arn":"arn:aws:iam::123456789012:role/runtime"}`,
		},
		{
			name: "Vertex service account", channelID: ID("google_vertex"),
			params:         `{"project_id":" project-one ","location":" us-central1 "}`,
			credential:     `{"service_account_json":" {\"type\":\"service_account\",\"project_id\":\"project-one\",\"client_email\":\"svc@example.iam.gserviceaccount.com\",\"private_key\":\"secret\"} "}`,
			wantParams:     `{"location":"us-central1","project_id":"project-one"}`,
			wantCredential: `{"service_account_json":"{\"client_email\":\"svc@example.iam.gserviceaccount.com\",\"private_key\":\"secret\",\"project_id\":\"project-one\",\"type\":\"service_account\"}"}`,
			invalidCredentials: []string{
				`{}`,
				`{"service_account_json":"not-json"}`,
				`{"service_account_json":"{}"}`,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params, err := registry.ValidateParams(test.channelID, json.RawMessage(test.params))
			if err != nil {
				t.Fatalf("ValidateParams() error = %v", err)
			}
			if got := string(params.CanonicalJSON()); got != test.wantParams {
				t.Fatalf("params = %s, want %s", got, test.wantParams)
			}
			credential, err := registry.ValidateCredential(test.channelID, json.RawMessage(test.credential))
			if err != nil {
				t.Fatalf("ValidateCredential() error = %v", err)
			}
			if got := string(credential.CanonicalJSON()); got != test.wantCredential {
				t.Fatalf("credential = %s, want %s", got, test.wantCredential)
			}
			for _, raw := range test.invalidCredentials {
				if _, err := registry.ValidateCredential(test.channelID, json.RawMessage(raw)); err == nil {
					t.Fatalf("ValidateCredential(%s) error = nil", raw)
				}
			}
		})
	}
}

func TestCloudTargetsCarryOnlyNeutralPresetMetadata(t *testing.T) {
	registry := NewRegistry()
	tests := []struct {
		channelID       ID
		params          string
		providerKind    ProviderKind
		catalogProvider string
	}{
		{ID("azure_openai"), `{"endpoint":"https://resource.openai.azure.com"}`, ProviderKind("azure_openai"), "azure"},
		{ID("aws_bedrock"), `{"region":"us-east-1"}`, ProviderKind("aws_bedrock"), "amazon-bedrock"},
		{ID("google_vertex"), `{"location":"us-central1","project_id":"project-one"}`, ProviderKind("google_vertex"), "google-vertex"},
	}
	for _, test := range tests {
		t.Run(string(test.channelID), func(t *testing.T) {
			target, err := registry.Resolve(test.channelID, json.RawMessage(test.params))
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if target.ChannelID != test.channelID || target.ProviderKind != test.providerKind || target.CatalogProviderID != test.catalogProvider || string(target.TargetConfig) != test.params {
				t.Fatalf("target = %#v, config=%s", target, target.TargetConfig)
			}
			if mode, ok := target.Mode(protocol.OpenAICompletions, execution.OperationChatCompletion); !ok || mode != RouteConverted {
				t.Fatalf("OpenAI chat mode = %q, %t", mode, ok)
			}
			if target.Supports(protocol.OpenAIResponses, execution.OperationResponsesRetrieve, execution.FeatureSet{}) {
				t.Fatal("cloud converted target unexpectedly exposes native Responses lifecycle")
			}
		})
	}
}

func TestRegistryStrictlyValidatesAndCanonicalizesParams(t *testing.T) {
	registry := NewRegistry()
	params, err := registry.ValidateParams(OpenAI, nil)
	if err != nil {
		t.Fatalf("ValidateParams(openai, nil) error = %v", err)
	}
	if got := string(params.CanonicalJSON()); got != `{}` {
		t.Fatalf("openai params = %s, want {}", got)
	}

	valid, err := registry.ValidateParams(OpenAICompatible, json.RawMessage(`{"base_url":" HTTPS://Example.COM/v1/ "}`))
	if err != nil {
		t.Fatalf("ValidateParams(compatible) error = %v", err)
	}
	if got, ok := valid.Value("base_url"); !ok || got != "https://example.com/v1" {
		t.Fatalf("base_url = %q, %t", got, ok)
	}
	if got := string(valid.CanonicalJSON()); got != `{"base_url":"https://example.com/v1"}` {
		t.Fatalf("canonical params = %s", got)
	}

	withQuery, err := registry.ValidateParams(OpenAICompatible, json.RawMessage(`{"base_url":" HTTPS://Example.COM/v1/?api-version=2025-01-01&tenant=one "}`))
	if err != nil {
		t.Fatalf("ValidateParams(compatible query) error = %v", err)
	}
	if got, ok := withQuery.Value("base_url"); !ok || got != "https://example.com/v1?api-version=2025-01-01&tenant=one" {
		t.Fatalf("query base_url = %q, %t", got, ok)
	}

	tests := []struct {
		name string
		id   ID
		raw  string
	}{
		{name: "missing required", id: OpenAICompatible, raw: `{}`},
		{name: "unknown", id: OpenAICompatible, raw: `{"base_url":"https://example.com","extra":"value"}`},
		{name: "wrong type", id: OpenAICompatible, raw: `{"base_url":42}`},
		{name: "duplicate", id: OpenAICompatible, raw: `{"base_url":"https://one.example","base_url":"https://two.example"}`},
		{name: "not object", id: OpenAICompatible, raw: `[]`},
		{name: "unsafe URL", id: OpenAICompatible, raw: `{"base_url":"https://user:secret@example.com"}`},
		{name: "query credential", id: OpenAICompatible, raw: `{"base_url":"https://example.com/v1?api_key=secret"}`},
		{name: "encoded query credential", id: OpenAICompatible, raw: `{"base_url":"https://example.com/v1?api%5Fkey=secret"}`},
		{name: "OpenAI override without v1 prefix", id: OpenAI, raw: `{"base_url":"https://example.com"}`},
		{name: "official unknown", id: OpenAI, raw: `{"base_url":"https://example.com","extra":"value"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := registry.ValidateParams(test.id, json.RawMessage(test.raw)); err == nil {
				t.Fatal("ValidateParams() error = nil")
			}
		})
	}
	if _, err := registry.ValidateParams(ID("missing"), nil); err == nil {
		t.Fatal("ValidateParams(unknown channel) error = nil")
	}
}

func TestRegistryStrictlyValidatesCredentialsWithoutLeakingSecrets(t *testing.T) {
	registry := NewRegistry()
	const secret = "sk-secret-value"
	credential, err := registry.ValidateCredential(OpenAI, json.RawMessage(`{"api_key":" `+secret+` "}`))
	if err != nil {
		t.Fatalf("ValidateCredential() error = %v", err)
	}
	if got, ok := credential.Value("api_key"); !ok || got != secret {
		t.Fatalf("api_key = %q, %t", got, ok)
	}
	if got := string(credential.CanonicalJSON()); got != `{"api_key":"`+secret+`"}` {
		t.Fatalf("canonical credential = %s", got)
	}
	encoded, err := json.Marshal(credential)
	if err != nil {
		t.Fatalf("json.Marshal(credential) error = %v", err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "api_key") {
		t.Fatalf("credential JSON leaked secret fields: %s", encoded)
	}

	for _, raw := range []string{
		`{}`,
		`{"api_key":""}`,
		`{"api_key":42}`,
		`{"api_key":"one","extra":"value"}`,
		`{"api_key":"one","api_key":"two"}`,
	} {
		_, err := registry.ValidateCredential(OpenAI, json.RawMessage(raw))
		if err == nil {
			t.Fatalf("ValidateCredential(%s) error = nil", raw)
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "one") || strings.Contains(err.Error(), "two") {
			t.Fatalf("validation error leaked credential value: %v", err)
		}
	}
}

func TestResolvedTargetsUseExactCatalogMappingAndProtocolSpecificCapabilities(t *testing.T) {
	registry := NewRegistry()
	openAI, err := registry.Resolve(OpenAI, nil)
	if err != nil {
		t.Fatalf("Resolve(openai) error = %v", err)
	}
	if openAI.ChannelID != OpenAI || openAI.ProviderKind != ProviderOpenAI ||
		openAI.CatalogProviderID != "openai" || string(openAI.TargetConfig) != `{}` {
		t.Fatalf("openai target = %#v", openAI)
	}
	if !openAI.Supports(protocol.OpenAIResponses, execution.OperationResponsesRetrieve, featureSet(t, execution.FeatureNativeResourceSemantics)) {
		t.Fatal("openai target should support native Responses retrieval")
	}
	for _, operation := range []execution.Operation{
		execution.OperationResponsesCompact,
		execution.OperationResponsesInputTokens,
		execution.OperationResponsesPassthrough,
	} {
		if !openAI.Supports(protocol.OpenAIResponses, operation, execution.FeatureSet{}) {
			t.Fatalf("openai target should support native %q", operation)
		}
		if mode, ok := openAI.Mode(protocol.OpenAIResponses, operation); !ok || mode != RouteNative {
			t.Fatalf("openai %q mode = %q, %t", operation, mode, ok)
		}
	}
	if !openAI.Supports(protocol.OpenAICompletions, execution.OperationChatCompletion, featureSet(t, execution.FeatureReasoning)) {
		t.Fatal("openai native completions should support reasoning")
	}
	if mode, ok := openAI.Mode(protocol.OpenAICompletions, execution.OperationChatCompletion); !ok || mode != RouteNative {
		t.Fatalf("openai completions mode = %q, %t", mode, ok)
	}

	anthropic, err := registry.Resolve(Anthropic, nil)
	if err != nil {
		t.Fatalf("Resolve(anthropic) error = %v", err)
	}
	if anthropic.ProviderKind != ProviderAnthropic || anthropic.CatalogProviderID != "anthropic" {
		t.Fatalf("anthropic catalog provider = %q", anthropic.CatalogProviderID)
	}
	if anthropic.Supports(protocol.OpenAIResponses, execution.OperationResponsesRetrieve, execution.FeatureSet{}) {
		t.Fatal("anthropic target unexpectedly supports native Responses retrieval")
	}
	if anthropic.Supports(protocol.OpenAICompletions, execution.OperationChatCompletion, featureSet(t, execution.FeatureReasoning)) {
		t.Fatal("converted OpenAI-to-Anthropic route unexpectedly advertises reasoning")
	}
	if !anthropic.Supports(protocol.OpenAICompletions, execution.OperationChatCompletion, featureSet(t, execution.FeatureStreaming)) {
		t.Fatal("converted OpenAI-to-Anthropic route should support streaming")
	}
	if mode, ok := anthropic.Mode(protocol.OpenAICompletions, execution.OperationChatCompletion); !ok || mode != RouteConverted {
		t.Fatalf("converted OpenAI-to-Anthropic mode = %q, %t", mode, ok)
	}
	if !anthropic.Supports(protocol.Anthropic, execution.OperationChatCompletion, featureSet(t, execution.FeatureReasoning)) {
		t.Fatal("native Anthropic route should support reasoning")
	}

	gemini, err := registry.Resolve(Gemini, nil)
	if err != nil || gemini.ProviderKind != ProviderGemini || gemini.CatalogProviderID != "google" {
		t.Fatalf("Resolve(gemini) = %#v, %v", gemini, err)
	}
	compatible, err := registry.Resolve(GeminiCompatible, json.RawMessage(`{"base_url":"https://proxy.example/v1/"}`))
	if err != nil {
		t.Fatalf("Resolve(gemini compatible) error = %v", err)
	}
	if compatible.ProviderKind != ProviderGeminiCompatible || compatible.CatalogProviderID != "" ||
		string(compatible.TargetConfig) != `{"base_url":"https://proxy.example/v1"}` {
		t.Fatalf("compatible target = %#v", compatible)
	}
	encodedTarget, err := json.Marshal(compatible)
	if err != nil {
		t.Fatalf("json.Marshal(compatible target) error = %v", err)
	}
	if strings.Contains(string(encodedTarget), "base_url") || strings.Contains(string(encodedTarget), "proxy.example") {
		t.Fatalf("resolved target JSON leaked internal config: %s", encodedTarget)
	}
	if mode, ok := compatible.Mode(protocol.OpenAIResponses, execution.OperationResponsesCreate); !ok || mode != RouteConverted {
		t.Fatalf("Gemini-compatible Responses create mode = %q, %t", mode, ok)
	}
	if compatible.Supports(protocol.OpenAIResponses, execution.OperationResponsesRetrieve, execution.FeatureSet{}) {
		t.Fatal("Gemini-compatible target unexpectedly supports Responses lifecycle")
	}

	openAICompatible, err := registry.Resolve(OpenAICompatible, json.RawMessage(`{"base_url":"https://proxy.example/v1"}`))
	if err != nil {
		t.Fatalf("Resolve(openai compatible) error = %v", err)
	}
	if mode, ok := openAICompatible.Mode(protocol.OpenAIResponses, execution.OperationResponsesCreate); !ok || mode != RouteConverted {
		t.Fatalf("OpenAI-compatible Responses create mode = %q, %t", mode, ok)
	}
	if openAICompatible.Supports(protocol.OpenAIResponses, execution.OperationResponsesRetrieve, featureSet(t, execution.FeatureNativeResourceSemantics)) {
		t.Fatal("generic OpenAI-compatible target must not advertise Responses lifecycle")
	}
	for _, operation := range []execution.Operation{
		execution.OperationResponsesCompact,
		execution.OperationResponsesInputTokens,
	} {
		if openAICompatible.Supports(protocol.OpenAIResponses, operation, execution.FeatureSet{}) {
			t.Fatalf("generic OpenAI-compatible target unexpectedly supports %q", operation)
		}
	}
	openAICompatibleNonV1, err := registry.Resolve(OpenAICompatible, json.RawMessage(`{"base_url":"https://proxy.example/vendor/v4"}`))
	if err != nil {
		t.Fatalf("Resolve(non-v1 OpenAI compatible) error = %v", err)
	}
	if mode, ok := openAICompatibleNonV1.Mode(protocol.OpenAICompletions, execution.OperationChatCompletion); !ok || mode != RouteConverted {
		t.Fatalf("non-v1 OpenAI-compatible completions mode = %q, %t", mode, ok)
	}
	if openAICompatibleNonV1.Supports(protocol.OpenAICompletions, execution.OperationChatCompletion, featureSet(t, execution.FeatureTools)) {
		t.Fatal("typed non-v1 OpenAI-compatible route unexpectedly advertises tools")
	}

	capabilities := openAI.Capabilities(protocol.OpenAIResponses)
	if !capabilities.Has(execution.OperationResponsesRetrieve) {
		t.Fatal("Capabilities() omitted native operation")
	}
	capabilities = execution.CapabilitySet{}
	if !openAI.Supports(protocol.OpenAIResponses, execution.OperationResponsesRetrieve, execution.FeatureSet{}) {
		t.Fatal("mutating returned capability value changed target")
	}
}

func TestCuratedChannelResolvesCodeOwnedTargetAndCatalogMapping(t *testing.T) {
	registry := NewRegistry()
	target, err := registry.Resolve(DeepSeek, nil)
	if err != nil {
		t.Fatalf("Resolve(deepseek) error = %v", err)
	}
	if target.ChannelID != DeepSeek || target.ProviderKind != ProviderOpenAICompatible ||
		target.CatalogProviderID != "deepseek" ||
		string(target.TargetConfig) != `{"base_url":"https://api.deepseek.com/v1"}` {
		t.Fatalf("Resolve(deepseek) = %#v", target)
	}
	if mode, ok := target.Mode(protocol.OpenAICompletions, execution.OperationChatCompletion); !ok || mode != RouteNative {
		t.Fatalf("DeepSeek native completions mode = %q, %t", mode, ok)
	}
	if mode, ok := target.Mode(protocol.Anthropic, execution.OperationChatCompletion); !ok || mode != RouteConverted {
		t.Fatalf("DeepSeek converted Anthropic mode = %q, %t", mode, ok)
	}
	descriptor, ok := registry.Get(DeepSeek)
	if !ok || len(descriptor.ParamFields) != 1 || len(descriptor.CredentialFields) != 1 {
		t.Fatalf("Get(deepseek) = %#v, %t", descriptor, ok)
	}
	if field := descriptor.ParamFields[0]; field.Key != "base_url" || field.Required || field.InputKind != InputURL {
		t.Fatalf("DeepSeek base URL override field = %#v", field)
	}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatalf("json.Marshal(deepseek descriptor) error = %v", err)
	}
	if strings.Contains(string(encoded), "api.deepseek.com") {
		t.Fatalf("public descriptor leaked fixed target: %s", encoded)
	}
	validated, err := registry.ResolveExecutionTarget(DeepSeek, target.TargetConfig)
	if err != nil || validated.ProviderKind != ProviderOpenAICompatible {
		t.Fatalf("ResolveExecutionTarget(deepseek) = %#v, %v", validated, err)
	}
	for _, tampered := range []json.RawMessage{
		nil,
		json.RawMessage(`{}`),
		json.RawMessage(`{"base_url":"https://attacker.example/v1","extra":"value"}`),
	} {
		if _, err := registry.ResolveExecutionTarget(DeepSeek, tampered); err == nil {
			t.Fatalf("ResolveExecutionTarget(deepseek, %s) error = nil", tampered)
		}
	}
	override, err := registry.Resolve(DeepSeek, json.RawMessage(`{"base_url":"https://mirror.example/v1/"}`))
	if err != nil || string(override.TargetConfig) != `{"base_url":"https://mirror.example/v1"}` {
		t.Fatalf("Resolve(deepseek override) = %#v, %v", override, err)
	}
	if _, err := registry.ResolveExecutionTarget(DeepSeek, override.TargetConfig); err != nil {
		t.Fatalf("ResolveExecutionTarget(deepseek override) error = %v", err)
	}
}

func TestOfficialChannelsUseSDKDefaultUnlessBaseURLIsExplicitlyOverridden(t *testing.T) {
	registry := NewRegistry()
	for _, channelID := range []ID{OpenAI, Anthropic, Gemini} {
		t.Run(string(channelID), func(t *testing.T) {
			defaultTarget, err := registry.Resolve(channelID, nil)
			if err != nil || string(defaultTarget.TargetConfig) != `{}` {
				t.Fatalf("Resolve(%s, default) = %#v, %v", channelID, defaultTarget, err)
			}
			override, err := registry.Resolve(channelID, json.RawMessage(`{"base_url":"https://mirror.example/v1/"}`))
			if err != nil || string(override.TargetConfig) != `{"base_url":"https://mirror.example/v1"}` {
				t.Fatalf("Resolve(%s, override) = %#v, %v", channelID, override, err)
			}
			if _, err := registry.ResolveExecutionTarget(channelID, override.TargetConfig); err != nil {
				t.Fatalf("ResolveExecutionTarget(%s, override) error = %v", channelID, err)
			}
		})
	}
}

func TestOpenRouterUsesNativeChatAndConvertedResponsesWithoutLifecycle(t *testing.T) {
	target, err := NewRegistry().Resolve(OpenRouter, nil)
	if err != nil {
		t.Fatalf("Resolve(openrouter) error = %v", err)
	}
	if mode, ok := target.Mode(protocol.OpenAICompletions, execution.OperationChatCompletion); !ok || mode != RouteNative {
		t.Fatalf("OpenRouter chat mode = %q, %t", mode, ok)
	}
	if mode, ok := target.Mode(protocol.OpenAIResponses, execution.OperationResponsesCreate); !ok || mode != RouteConverted {
		t.Fatalf("OpenRouter Responses create mode = %q, %t", mode, ok)
	}
	if target.Supports(protocol.OpenAIResponses, execution.OperationResponsesRetrieve, execution.FeatureSet{}) {
		t.Fatal("OpenRouter unexpectedly advertises Responses lifecycle")
	}
}

func descriptorIDs(descriptors []Descriptor) []ID {
	ids := make([]ID, len(descriptors))
	for index, descriptor := range descriptors {
		ids[index] = descriptor.ID
	}
	return ids
}

func featureSet(t *testing.T, features ...execution.Feature) execution.FeatureSet {
	t.Helper()
	set, err := execution.NewFeatureSet(features...)
	if err != nil {
		t.Fatalf("NewFeatureSet() error = %v", err)
	}
	return set
}
