package channel

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestRegistryHasStableBuiltInOrderAndSearch(t *testing.T) {
	registry := NewRegistry()
	wantIDs := []ID{
		OpenAI,
		Codex,
		Claude,
		Antigravity,
		Grok,
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
		GPTLoad,
		NewAPI,
		CLIProxyAPI,
		OpenAICompatible,
	}
	if got := descriptorIDs(registry.List()); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("List() IDs = %v, want %v", got, wantIDs)
	}
	if got := descriptorIDs(registry.Search("GeMiNi")); !reflect.DeepEqual(got, []ID{Gemini}) {
		t.Fatalf("Search(gemini) IDs = %v", got)
	}
	if got := descriptorIDs(registry.Search("subscription")); !reflect.DeepEqual(got, []ID{Codex, Claude, Antigravity, Grok}) {
		t.Fatalf("Search(subscription) IDs = %v, want [codex claude antigravity grok]", got)
	}
	if got := descriptorIDs(registry.Search("compatible")); !reflect.DeepEqual(got, []ID{OpenAICompatible}) {
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
	if got := descriptorIDs(registry.Search("cpa")); !reflect.DeepEqual(got, []ID{CLIProxyAPI}) {
		t.Fatalf("Search(cpa) IDs = %v", got)
	}
	if got := descriptorIDs(registry.Search("gptload")); !reflect.DeepEqual(got, []ID{GPTLoad}) {
		t.Fatalf("Search(gptload) IDs = %v", got)
	}
	if got := descriptorIDs(registry.Search("gl")); !slices.Contains(got, GPTLoad) {
		t.Fatalf("Search(gl) IDs = %v", got)
	}

	first := registry.List()
	first[0].ClientProtocols[0] = protocol.Protocol("mutated")
	first[0].ParamFields = append(first[0].ParamFields, FieldDescriptor{Key: "mutated"})
	vertexIndex := 9
	if first[vertexIndex].ID != GoogleVertex || first[vertexIndex].ParamFields[0].DefaultValue == nil {
		t.Fatalf("unexpected Vertex descriptor = %#v", first[vertexIndex])
	}
	*first[vertexIndex].ParamFields[0].DefaultValue = "mutated"
	second := registry.List()
	if second[0].ClientProtocols[0] == protocol.Protocol("mutated") || len(second[0].ParamFields) != 1 ||
		second[vertexIndex].ParamFields[0].DefaultValue == nil ||
		*second[vertexIndex].ParamFields[0].DefaultValue != "global" {
		t.Fatal("mutating List() output changed registry state")
	}
}

func TestBuiltInSubscriptionChannelsUseOnlyGlobalRiskNotice(t *testing.T) {
	registry := NewRegistry()
	for _, descriptor := range registry.List() {
		if descriptor.Connection.Type == string(spec.ConnectionSubscription) && len(descriptor.Notices) != 0 {
			t.Fatalf("subscription channel %q declares step-level notices: %#v", descriptor.ID, descriptor.Notices)
		}
	}
}

func TestRegistryExposesNonEmptyIconAndSearchTermsForEveryBuiltInChannel(t *testing.T) {
	registry := NewRegistry()
	for _, descriptor := range registry.List() {
		if strings.TrimSpace(descriptor.Icon) == "" {
			t.Fatalf("channel %q has no icon", descriptor.ID)
		}
	}
	// SearchTerms is the exact data Search() matches against; a hit on an
	// alias that is not the channel ID, name, or description proves the two
	// stay in sync after being folded into one field.
	if got := descriptorIDs(registry.Search("kimi")); !reflect.DeepEqual(got, []ID{MoonshotAI}) {
		t.Fatalf("Search(kimi) IDs = %v, want [%s]", got, MoonshotAI)
	}
	moonshot, ok := registry.Get(MoonshotAI)
	if !ok || !reflect.DeepEqual(moonshot.SearchTerms, []string{"kimi", "moonshot"}) {
		t.Fatalf("Get(moonshotai).SearchTerms = %#v, %t", moonshot.SearchTerms, ok)
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
	for _, forbidden := range []string{"target_config", "catalog_provider", "secret-value"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("public descriptor leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestRegistryPublishesOnlySafeParameterDefaults(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	vertex, ok := registry.Get(GoogleVertex)
	if !ok || len(vertex.ParamFields) != 1 || vertex.ParamFields[0].DefaultValue == nil ||
		*vertex.ParamFields[0].DefaultValue != "global" {
		t.Fatalf("Vertex parameter schema = %#v", vertex.ParamFields)
	}
	for _, descriptor := range registry.List() {
		for _, field := range descriptor.CredentialFields {
			if field.DefaultValue != nil {
				t.Fatalf("channel %q publishes a credential default for %q", descriptor.ID, field.Key)
			}
		}
	}
}

func TestCodexIsTheOnlySubscriptionChannelWithoutExposingExecutor(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	descriptor, ok := registry.Get(Codex)
	if !ok {
		t.Fatal("Codex descriptor is missing")
	}
	if descriptor.Connection.Type != "subscription" {
		t.Fatalf("connection = %#v", descriptor.Connection)
	}
	if got := descriptor.Connection.AuthorizationMethods; len(got) != 2 ||
		got[0] != "browser_oauth" || got[1] != "oauth_file" {
		t.Fatalf("subscription authorization methods = %#v", got)
	}
	if !descriptor.Capabilities.ModelDiscovery || !descriptor.Capabilities.QuotaObservation ||
		!reflect.DeepEqual(descriptor.Capabilities.CredentialActions, []CredentialAction{CredentialActionResetCredit}) {
		t.Fatalf("subscription capabilities = %#v", descriptor.Capabilities)
	}
	openAI, ok := registry.Get(OpenAI)
	if !ok || openAI.Connection.Type != "api_key" {
		t.Fatalf("OpenAI connection = %#v, found = %t", openAI.Connection, ok)
	}
	if !openAI.Capabilities.ModelDiscovery || openAI.Capabilities.QuotaObservation ||
		len(openAI.Capabilities.CredentialActions) != 0 {
		t.Fatalf("OpenAI capabilities = %#v", openAI.Capabilities)
	}
	target, err := registry.Resolve(Codex, nil)
	if err != nil {
		t.Fatal(err)
	}
	for clientProtocol, expectation := range map[protocol.Protocol]struct {
		operation execution.Operation
		mode      RouteMode
	}{
		protocol.OpenAICompletions: {operation: execution.OperationChatCompletion, mode: RouteConverted},
		protocol.OpenAIResponses:   {operation: execution.OperationResponsesCreate, mode: RouteNative},
		protocol.Anthropic:         {operation: execution.OperationChatCompletion, mode: RouteConverted},
		protocol.Gemini:            {operation: execution.OperationChatCompletion, mode: RouteConverted},
	} {
		if mode, exists := target.Mode(clientProtocol, expectation.operation); !exists || mode != expectation.mode {
			t.Fatalf("Codex %q/%q mode = %q, %t, want %q", clientProtocol, expectation.operation, mode, exists, expectation.mode)
		}
		for _, unsupported := range []execution.Operation{execution.OperationListModels, execution.OperationProbe} {
			if _, exists := target.Mode(clientProtocol, unsupported); exists {
				t.Fatalf("Codex unexpectedly advertises %q/%q", clientProtocol, unsupported)
			}
		}
	}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	var publicDescriptor map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &publicDescriptor); err != nil {
		t.Fatal(err)
	}
	if _, ok := publicDescriptor["capabilities"]; !ok {
		t.Fatalf("Codex descriptor omits safe capabilities: %s", encoded)
	}
	for _, internalName := range []string{"CPA", "Bifrost", "executor"} {
		if strings.Contains(strings.ToLower(string(encoded)), strings.ToLower(internalName)) {
			t.Fatalf("descriptor exposes %q: %s", internalName, encoded)
		}
	}
}

func TestChannelDescriptorMarksOutboundProxySupport(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, test := range []struct {
		channelID ID
		want      bool
	}{
		{channelID: OpenAI, want: true},
		{channelID: Codex, want: true},
		{channelID: AzureOpenAI, want: false},
		{channelID: AWSBedrock, want: false},
		{channelID: GoogleVertex, want: false},
	} {
		descriptor, ok := registry.Get(test.channelID)
		if !ok {
			t.Fatalf("channel %q is missing", test.channelID)
		}
		encoded, err := json.Marshal(descriptor.Capabilities)
		if err != nil {
			t.Fatal(err)
		}
		var capabilities map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &capabilities); err != nil {
			t.Fatal(err)
		}
		var got bool
		value, exists := capabilities["outbound_proxy"]
		if !exists {
			t.Fatalf("channel %q omits outbound_proxy: %s", test.channelID, encoded)
		}
		if err := json.Unmarshal(value, &got); err != nil {
			t.Fatalf("channel %q outbound_proxy is invalid: %v", test.channelID, err)
		}
		if got != test.want {
			t.Fatalf("channel %q outbound_proxy = %t, want %t", test.channelID, got, test.want)
		}
	}
}

func TestRegistryPublicDescriptorUsesSingularConnectionAndExplicitRoutes(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, descriptor := range registry.List() {
		encoded, err := json.Marshal(descriptor)
		if err != nil {
			t.Fatalf("json.Marshal(%q) error = %v", descriptor.ID, err)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &object); err != nil {
			t.Fatalf("json.Unmarshal(%q) error = %v", descriptor.ID, err)
		}
		if _, exists := object["connection_types"]; exists {
			t.Fatalf("channel %q still exposes connection_types: %s", descriptor.ID, encoded)
		}
		if _, exists := object["connection"]; !exists {
			t.Fatalf("channel %q has no singular connection: %s", descriptor.ID, encoded)
		}
		if routes, exists := object["routes"]; !exists || string(routes) == "[]" || string(routes) == "null" {
			t.Fatalf("channel %q has no explicit routes: %s", descriptor.ID, encoded)
		}
	}
}

func TestSubscriptionChannelsRemainSeparateFromAPIKeyChannels(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, id := range []ID{Codex, Claude, Antigravity, Grok} {
		if !registry.SupportsConnectionType(id, "subscription") {
			t.Fatalf("%s subscription is not supported", id)
		}
	}
	if registry.SupportsConnectionType(OpenAI, "subscription") ||
		registry.SupportsConnectionType(Anthropic, "subscription") ||
		registry.SupportsConnectionType(OpenAICompatible, "subscription") {
		t.Fatal("an unsupported channel advertises subscription")
	}
	if registry.SupportsConnectionType(Codex, "api_key") {
		t.Fatal("Codex unexpectedly supports API keys")
	}
	if !registry.SupportsConnectionType(Anthropic, "api_key") {
		t.Fatal("legacy api_key support is missing")
	}
}

func TestRegistryReturnsExactCatalogProviderMappingWithoutResolvingParams(t *testing.T) {
	registry := NewRegistry()
	for id, want := range map[ID]string{
		OpenAI: "openai", Codex: "", Claude: "", Antigravity: "", Grok: "", Anthropic: "anthropic", Gemini: "google",
		ID("azure_openai"): "azure", ID("aws_bedrock"): "amazon-bedrock", ID("google_vertex"): "google-vertex",
		OpenAICompatible: "",
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

func TestRegistryReturnsProviderKindWithoutExposingItInDescriptor(t *testing.T) {
	registry := NewRegistry()
	if got, ok := registry.ProviderKind(OpenAI); !ok || got != ProviderOpenAI {
		t.Fatalf("ProviderKind(openai) = %q, %t", got, ok)
	}
	if got, ok := registry.ProviderKind(Codex); !ok || string(got) != "codex" {
		t.Fatalf("ProviderKind(codex) = %q, %t, want codex, true", got, ok)
	}
	if got, ok := registry.ProviderKind(Grok); !ok || got != ProviderGrok {
		t.Fatalf("ProviderKind(grok) = %q, %t", got, ok)
	}
	if got, ok := registry.ProviderKind(ID("missing")); ok || got != "" {
		t.Fatalf("ProviderKind(missing) = %q, %t", got, ok)
	}
	encoded, err := json.Marshal(registry.List())
	if err != nil {
		t.Fatalf("json.Marshal(descriptors) error = %v", err)
	}
	if strings.Contains(string(encoded), "provider_kind") {
		t.Fatalf("public descriptors exposed provider kind: %s", encoded)
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
			params:         `{"location":" us-central1 "}`,
			credential:     `{"service_account_json":" {\"type\":\"service_account\",\"project_id\":\"project-one\",\"client_email\":\"svc@example.iam.gserviceaccount.com\",\"private_key\":\"secret\"} "}`,
			wantParams:     `{"location":"us-central1"}`,
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
		{ID("google_vertex"), `{"location":"us-central1"}`, ProviderKind("google_vertex"), "google-vertex"},
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
			if _, ok := target.Mode(protocol.OpenAIResponses, execution.OperationResponsesRetrieve); ok {
				t.Fatal("cloud converted target unexpectedly exposes native Responses lifecycle")
			}
		})
	}
}

func TestVertexUsesCredentialProjectAndDefaultsLocation(t *testing.T) {
	registry := NewRegistry()

	params, err := registry.ValidateParams(GoogleVertex, nil)
	if err != nil {
		t.Fatalf("ValidateParams(default Vertex) error = %v", err)
	}
	if got := string(params.CanonicalJSON()); got != `{"location":"global"}` {
		t.Fatalf("default Vertex params = %s, want global location", got)
	}
	emptyLocation, err := registry.ValidateParams(GoogleVertex, json.RawMessage(`{"location":""}`))
	if err != nil || string(emptyLocation.CanonicalJSON()) != `{"location":"global"}` {
		t.Fatalf("empty Vertex location = %s, %v, want global", emptyLocation.CanonicalJSON(), err)
	}

	for _, field := range []string{"project_id", "project_number"} {
		_, err := registry.ValidateParams(
			GoogleVertex,
			json.RawMessage(`{"`+field+`":"legacy-value","location":"us-central1"}`),
		)
		if err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("ValidateParams(Vertex %s) error = %v, want unknown field", field, err)
		}
	}

	descriptor, ok := registry.Get(GoogleVertex)
	if !ok || len(descriptor.ParamFields) != 1 || descriptor.ParamFields[0].Key != "location" || descriptor.ParamFields[0].Required {
		t.Fatalf("Vertex descriptor params = %#v, %t", descriptor.ParamFields, ok)
	}

	target, err := registry.Resolve(GoogleVertex, nil)
	if err != nil {
		t.Fatalf("Resolve(default Vertex) error = %v", err)
	}
	if mode, ok := target.ModeForModel(protocol.Gemini, execution.OperationChatCompletion, "gemini-2.5-pro"); !ok || mode != RouteNative {
		t.Fatalf("Vertex Gemini mode = %q, %t, want native", mode, ok)
	}
	if mode, ok := target.ModeForModel(protocol.Gemini, execution.OperationChatCompletion, "claude-sonnet-4"); !ok || mode != RouteConverted {
		t.Fatalf("Vertex Claude-through-Gemini mode = %q, %t, want converted", mode, ok)
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
		{name: "ordinary query", id: OpenAICompatible, raw: `{"base_url":"https://example.com/v1?tenant=one"}`},
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

func TestResolvedTargetsUseExactCatalogMappingAndProtocolSpecificRouteModes(t *testing.T) {
	registry := NewRegistry()
	openAI, err := registry.Resolve(OpenAI, nil)
	if err != nil {
		t.Fatalf("Resolve(openai) error = %v", err)
	}
	if openAI.ChannelID != OpenAI || openAI.ProviderKind != ProviderOpenAI ||
		openAI.CatalogProviderID != "openai" || string(openAI.TargetConfig) != `{}` {
		t.Fatalf("openai target = %#v", openAI)
	}
	if mode, ok := openAI.Mode(protocol.OpenAIResponses, execution.OperationResponsesRetrieve); !ok || mode != RouteNative {
		t.Fatalf("openai Responses retrieval mode = %q, %t", mode, ok)
	}
	for _, operation := range []execution.Operation{
		execution.OperationResponsesCompact,
		execution.OperationResponsesInputTokens,
		execution.OperationResponsesPassthrough,
	} {
		if mode, ok := openAI.Mode(protocol.OpenAIResponses, operation); !ok || mode != RouteNative {
			t.Fatalf("openai %q mode = %q, %t", operation, mode, ok)
		}
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
	if _, ok := anthropic.Mode(protocol.OpenAIResponses, execution.OperationResponsesRetrieve); ok {
		t.Fatal("anthropic target unexpectedly supports native Responses retrieval")
	}
	if mode, ok := anthropic.Mode(protocol.OpenAICompletions, execution.OperationChatCompletion); !ok || mode != RouteConverted {
		t.Fatalf("converted OpenAI-to-Anthropic mode = %q, %t", mode, ok)
	}
	if mode, ok := anthropic.Mode(protocol.Anthropic, execution.OperationChatCompletion); !ok || mode != RouteNative {
		t.Fatalf("native Anthropic mode = %q, %t", mode, ok)
	}

	gemini, err := registry.Resolve(Gemini, nil)
	if err != nil || gemini.ProviderKind != ProviderGemini || gemini.CatalogProviderID != "google" {
		t.Fatalf("Resolve(gemini) = %#v, %v", gemini, err)
	}
	openAICompatible, err := registry.Resolve(OpenAICompatible, json.RawMessage(`{"base_url":"https://proxy.example/v1"}`))
	if err != nil {
		t.Fatalf("Resolve(openai compatible) error = %v", err)
	}
	if mode, ok := openAICompatible.Mode(protocol.OpenAIResponses, execution.OperationResponsesCreate); !ok || mode != RouteConverted {
		t.Fatalf("OpenAI-compatible Responses create mode = %q, %t", mode, ok)
	}
	if _, ok := openAICompatible.Mode(protocol.OpenAIResponses, execution.OperationResponsesRetrieve); ok {
		t.Fatal("generic OpenAI-compatible target must not advertise Responses lifecycle")
	}
	for _, operation := range []execution.Operation{
		execution.OperationResponsesCompact,
		execution.OperationResponsesInputTokens,
	} {
		if _, ok := openAICompatible.Mode(protocol.OpenAIResponses, operation); ok {
			t.Fatalf("generic OpenAI-compatible target unexpectedly supports %q", operation)
		}
	}
	openAICompatibleNonV1, err := registry.Resolve(OpenAICompatible, json.RawMessage(`{"base_url":"https://proxy.example/vendor/v4"}`))
	if err != nil {
		t.Fatalf("Resolve(non-v1 OpenAI compatible) error = %v", err)
	}
	if mode, ok := openAICompatibleNonV1.Mode(protocol.OpenAICompletions, execution.OperationChatCompletion); !ok || mode != RouteNative {
		t.Fatalf("non-v1 OpenAI-compatible completions mode = %q, %t", mode, ok)
	}
	operations := openAI.Operations(protocol.OpenAIResponses)
	if !containsOperation(operations, execution.OperationResponsesRetrieve) {
		t.Fatal("Operations() omitted native operation")
	}
	operations[0] = execution.Operation("mutated")
	if !containsOperation(openAI.Operations(protocol.OpenAIResponses), execution.OperationResponsesRetrieve) {
		t.Fatal("mutating returned operations changed target")
	}
}

func TestCuratedChannelResolvesCodeOwnedTargetAndCatalogMapping(t *testing.T) {
	registry := NewRegistry()
	target, err := registry.Resolve(DeepSeek, nil)
	if err != nil {
		t.Fatalf("Resolve(deepseek) error = %v", err)
	}
	if target.ChannelID != DeepSeek || target.ProviderKind != ProviderDeepSeek ||
		target.CatalogProviderID != "deepseek" || string(target.TargetConfig) != `{}` {
		t.Fatalf("Resolve(deepseek) = %#v", target)
	}
	if mode, ok := target.Mode(protocol.OpenAICompletions, execution.OperationChatCompletion); !ok || mode != RouteNative {
		t.Fatalf("DeepSeek native completions mode = %q, %t", mode, ok)
	}
	if mode, ok := target.Mode(protocol.OpenAIResponses, execution.OperationResponsesCreate); !ok || mode != RouteNative {
		t.Fatalf("DeepSeek native Responses mode = %q, %t", mode, ok)
	}
	if mode, ok := target.Mode(protocol.Anthropic, execution.OperationChatCompletion); !ok || mode != RouteNative {
		t.Fatalf("DeepSeek native Anthropic mode = %q, %t", mode, ok)
	}
	if mode, ok := target.Mode(protocol.Anthropic, execution.OperationListModels); !ok || mode != RouteConverted {
		t.Fatalf("DeepSeek Anthropic model-list mode = %q, %t", mode, ok)
	}
	if mode, ok := target.Mode(protocol.Gemini, execution.OperationChatCompletion); !ok || mode != RouteConverted {
		t.Fatalf("DeepSeek converted Gemini mode = %q, %t", mode, ok)
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
		t.Fatalf("public descriptor leaked SDK default target: %s", encoded)
	}
	validated, err := registry.ResolveExecutionTarget(DeepSeek, target.TargetConfig)
	if err != nil || validated.ProviderKind != ProviderDeepSeek {
		t.Fatalf("ResolveExecutionTarget(deepseek) = %#v, %v", validated, err)
	}
	for _, tampered := range []json.RawMessage{
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
	tests := []struct {
		channelID ID
		baseURL   string
	}{
		{channelID: OpenAI, baseURL: "https://mirror.example"},
		{channelID: Anthropic, baseURL: "https://mirror.example"},
		{channelID: Gemini, baseURL: "https://mirror.example/v1beta"},
	}
	for _, test := range tests {
		t.Run(string(test.channelID), func(t *testing.T) {
			defaultTarget, err := registry.Resolve(test.channelID, nil)
			if err != nil || string(defaultTarget.TargetConfig) != `{}` {
				t.Fatalf("Resolve(%s, default) = %#v, %v", test.channelID, defaultTarget, err)
			}
			override, err := registry.Resolve(test.channelID, json.RawMessage(`{"base_url":"`+test.baseURL+`"}`))
			if err != nil || string(override.TargetConfig) != `{"base_url":"`+test.baseURL+`"}` {
				t.Fatalf("Resolve(%s, override) = %#v, %v", test.channelID, override, err)
			}
			if _, err := registry.ResolveExecutionTarget(test.channelID, override.TargetConfig); err != nil {
				t.Fatalf("ResolveExecutionTarget(%s, override) error = %v", test.channelID, err)
			}
		})
	}
}

func TestOpenRouterUsesNativeChatAndResponsesWithoutLifecycle(t *testing.T) {
	target, err := NewRegistry().Resolve(OpenRouter, nil)
	if err != nil {
		t.Fatalf("Resolve(openrouter) error = %v", err)
	}
	if mode, ok := target.Mode(protocol.OpenAICompletions, execution.OperationChatCompletion); !ok || mode != RouteNative {
		t.Fatalf("OpenRouter chat mode = %q, %t", mode, ok)
	}
	if mode, ok := target.Mode(protocol.OpenAIResponses, execution.OperationResponsesCreate); !ok || mode != RouteNative {
		t.Fatalf("OpenRouter Responses create mode = %q, %t", mode, ok)
	}
	if _, ok := target.Mode(protocol.OpenAIResponses, execution.OperationResponsesRetrieve); ok {
		t.Fatal("OpenRouter unexpectedly advertises Responses lifecycle")
	}
}

func TestOnlyVerifiedNativeSubscriptionChannelsDeclareStatelessResponsesStore(t *testing.T) {
	registry := NewRegistry()
	verified := map[ID]struct{}{Codex: {}, Grok: {}}

	key := routeKey{
		clientProtocol: protocol.OpenAIResponses,
		operation:      execution.OperationResponsesCreate,
	}
	for _, channelID := range registry.order {
		got := registry.byID[channelID].responsesStoreCompatibilities[key]
		_, want := verified[channelID]
		if want && got != ResponsesStoreCompatibilityStateless {
			t.Errorf("%s compatibility = %q, want stateless", channelID, got)
		}
		if !want && got != ResponsesStoreCompatibilityNone {
			t.Errorf("%s compatibility = %q, want none", channelID, got)
		}
	}
}

func descriptorIDs(descriptors []Descriptor) []ID {
	ids := make([]ID, len(descriptors))
	for index, descriptor := range descriptors {
		ids[index] = descriptor.ID
	}
	return ids
}

func containsOperation(operations []execution.Operation, want execution.Operation) bool {
	for _, operation := range operations {
		if operation == want {
			return true
		}
	}
	return false
}
