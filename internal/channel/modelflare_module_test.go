package channel

import (
	"encoding/json"
	"reflect"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestModelflarePresetIsPrincipalPartnerWithVerifiedRoutes(t *testing.T) {
	registry := NewRegistry()
	descriptor, ok := registry.Get(Modelflare)
	if !ok {
		t.Fatal("Get(modelflare) missing")
	}
	wantPartner := &PartnerDescriptor{
		Tier:      PartnerPrincipal,
		SignupURL: "https://modelflare.dev/sign-up?utm_source=gptload&utm_campaign=gptload",
	}
	if !reflect.DeepEqual(descriptor.Partner, wantPartner) {
		t.Fatalf("modelflare partner = %#v, want %#v", descriptor.Partner, wantPartner)
	}
	if descriptor.Connection.Type != "api_key" {
		t.Fatalf("modelflare connection = %q", descriptor.Connection.Type)
	}
	for _, route := range descriptor.Routes {
		if route.RouteMode != RouteNative {
			t.Errorf("unexpected conversion route: %+v", route)
		}
	}

	for _, override := range []string{"", "https://origin.modelflare.dev"} {
		params := json.RawMessage(`{}`)
		wantURL := "https://modelflare.dev"
		if override != "" {
			params = json.RawMessage(`{"base_url":"` + override + `/"}`)
			wantURL = override
		}
		target, err := registry.Resolve(Modelflare, params)
		if err != nil {
			t.Fatal(err)
		}
		if target.ProviderKind != ProviderMultiProtocolGateway || string(target.TargetConfig) != `{"base_url":"`+wantURL+`"}` {
			t.Fatalf("unexpected gateway target: %+v", target)
		}
		for _, p := range []protocol.Protocol{protocol.OpenAICompletions, protocol.Anthropic} {
			if mode, ok := target.Mode(p, execution.OperationChatCompletion); !ok || mode != RouteNative {
				t.Errorf("%s must use a native route, got %s / %t", p, mode, ok)
			}
		}
		if mode, ok := target.Mode(protocol.OpenAIResponses, execution.OperationResponsesCreate); !ok || mode != RouteNative {
			t.Errorf("Responses must use a native route, got %s / %t", mode, ok)
		}
		if handling := target.ResponsesStoreHandling(protocol.OpenAIResponses, execution.OperationResponsesCreate); handling != ResponsesStoreHandlingStateless {
			t.Errorf("Responses store handling = %q, want stateless", handling)
		}
		// 上游 Responses 只支持流式，凭据测试不走 Responses，默认使用 Chat Completions。
		if _, ok := target.Mode(protocol.OpenAIResponses, execution.OperationProbe); ok {
			t.Error("Responses probe must not be declared while the upstream rejects non-streaming requests")
		}
		if selected, ok := target.PreferredProtocol(execution.OperationProbe, "gpt-5.4"); !ok || selected != protocol.OpenAICompletions {
			t.Errorf("credential probe protocol = %q / %t, want openai-completions", selected, ok)
		}
		// 实测 Gemini 原生不可用、图片无法验证，修复前不声明。
		if _, ok := target.Mode(protocol.Gemini, execution.OperationChatCompletion); ok {
			t.Error("Gemini must not be advertised until the upstream native endpoint works")
		}
		if _, ok := target.Mode(protocol.OpenAIImages, execution.OperationImagesGenerate); ok {
			t.Error("Images must not be advertised before verification")
		}
	}
}
