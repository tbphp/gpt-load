package channel

import (
	"encoding/json"
	"reflect"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestFinalRegistryContainsOnlyApprovedChannels(t *testing.T) {
	registry := NewRegistry()
	want := []ID{
		OpenAI,
		Codex,
		Claude,
		Antigravity,
		Grok,
		Anthropic,
		Gemini,
		AzureOpenAI,
		AWSBedrock,
		GoogleVertex,
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
	}
	if got := descriptorIDs(registry.List()); !reflect.DeepEqual(got, want) {
		t.Fatalf("List() IDs = %v, want %v", got, want)
	}
	for _, removed := range []ID{"anthropic_compatible", "gemini_compatible"} {
		if _, ok := registry.Get(removed); ok {
			t.Fatalf("removed channel %q is still registered", removed)
		}
	}
}

func TestNativeProviderChannelsResolveWithoutCompatibleFallback(t *testing.T) {
	registry := NewRegistry()
	tests := []struct {
		channelID         ID
		providerKind      ProviderKind
		responsesMode     RouteMode
		anthropicMode     RouteMode
		catalogProviderID string
	}{
		{channelID: DeepSeek, providerKind: ProviderKind("deepseek"), responsesMode: RouteNative, anthropicMode: RouteNative, catalogProviderID: "deepseek"},
		{channelID: OpenRouter, providerKind: ProviderKind("openrouter"), responsesMode: RouteNative, anthropicMode: RouteConverted, catalogProviderID: "openrouter"},
		{channelID: Groq, providerKind: ProviderKind("groq"), responsesMode: RouteConverted, anthropicMode: RouteConverted, catalogProviderID: "groq"},
		{channelID: XAI, providerKind: ProviderKind("xai"), responsesMode: RouteNative, anthropicMode: RouteConverted, catalogProviderID: "xai"},
	}
	for _, test := range tests {
		t.Run(string(test.channelID), func(t *testing.T) {
			target, err := registry.Resolve(test.channelID, nil)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if target.ProviderKind != test.providerKind || target.CatalogProviderID != test.catalogProviderID {
				t.Fatalf("target = %#v", target)
			}
			if got := string(target.TargetConfig); got != `{}` {
				t.Fatalf("TargetConfig = %s, want {}", got)
			}
			if mode, ok := target.Mode(protocol.OpenAICompletions, execution.OperationChatCompletion); !ok || mode != RouteNative {
				t.Fatalf("chat mode = %q, %t", mode, ok)
			}
			if mode, ok := target.Mode(protocol.OpenAIResponses, execution.OperationResponsesCreate); !ok || mode != test.responsesMode {
				t.Fatalf("Responses mode = %q, %t, want %q", mode, ok, test.responsesMode)
			}
			if mode, ok := target.Mode(protocol.Anthropic, execution.OperationChatCompletion); !ok || mode != test.anthropicMode {
				t.Fatalf("Anthropic mode = %q, %t, want %q", mode, ok, test.anthropicMode)
			}
			if test.channelID == DeepSeek {
				if mode, ok := target.Mode(protocol.Anthropic, execution.OperationListModels); !ok || mode != RouteConverted {
					t.Fatalf("DeepSeek Anthropic model-list mode = %q, %t", mode, ok)
				}
			}
			if _, ok := target.Mode(protocol.OpenAIResponses, execution.OperationResponsesRetrieve); ok {
				t.Fatal("target unexpectedly supports Responses lifecycle")
			}
		})
	}
}

func TestNativeProviderBaseURLUsesSDKPrefixContract(t *testing.T) {
	registry := NewRegistry()
	tests := []struct {
		channelID ID
		baseURL   string
	}{
		{channelID: OpenAI, baseURL: "https://mirror.example"},
		{channelID: Anthropic, baseURL: "https://mirror.example"},
		{channelID: Gemini, baseURL: "https://mirror.example/v1beta"},
		{channelID: DeepSeek, baseURL: "https://mirror.example/v1"},
		{channelID: OpenRouter, baseURL: "https://mirror.example/api"},
		{channelID: Groq, baseURL: "https://mirror.example/openai"},
		{channelID: XAI, baseURL: "https://mirror.example"},
	}
	for _, test := range tests {
		t.Run(string(test.channelID), func(t *testing.T) {
			target, err := registry.Resolve(test.channelID, json.RawMessage(`{"base_url":"`+test.baseURL+`"}`))
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got := string(target.TargetConfig); got != `{"base_url":"`+test.baseURL+`"}` {
				t.Fatalf("TargetConfig = %s", got)
			}
		})
	}

	if _, err := registry.Resolve(OpenAI, json.RawMessage(`{"base_url":"https://mirror.example?tenant=one"}`)); err == nil {
		t.Fatal("Resolve(BaseURL query) error = nil")
	}
}

func TestBaseURLCanonicalizesDefaultPortsForRuntimeReuse(t *testing.T) {
	registry := NewRegistry()
	tests := []struct {
		raw  string
		want string
	}{
		{raw: `{"base_url":"HTTPS://API.OPENAI.COM:443/"}`, want: `{"base_url":"https://api.openai.com"}`},
		{raw: `{"base_url":"http://relay.example:80/v1/"}`, want: `{"base_url":"http://relay.example/v1"}`},
		{raw: `{"base_url":"https://relay.example:8443/v1/"}`, want: `{"base_url":"https://relay.example:8443/v1"}`},
	}
	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			target, err := registry.Resolve(OpenAI, json.RawMessage(test.raw))
			if err != nil {
				t.Fatal(err)
			}
			if got := string(target.TargetConfig); got != test.want {
				t.Fatalf("TargetConfig = %s, want %s", got, test.want)
			}
		})
	}
}
