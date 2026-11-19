package channel

import (
	"encoding/json"
	"slices"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestSponsorPresetsUseFeaturedOrderAndNativeRoutes(t *testing.T) {
	registry := NewRegistry()
	var featured []ID
	for _, descriptor := range registry.List() {
		if descriptor.Partner != nil && descriptor.Partner.Tier == PartnerFeatured {
			featured = append(featured, descriptor.ID)
		}
	}
	modelinkIndex, fennoIndex := slices.Index(featured, Modelink), slices.Index(featured, Fenno)
	if modelinkIndex < 0 || fennoIndex != modelinkIndex+1 {
		t.Fatalf("featured order = %v, want Modelink followed by Fenno", featured)
	}

	for _, test := range []struct {
		id        ID
		baseURL   string
		signupURL string
	}{
		{Modelink, "https://api.qnaigc.com", "https://www.qiniu.com/ai/"},
		{Fenno, "https://api.fenno.ai", "https://api.fenno.ai/"},
	} {
		t.Run(string(test.id), func(t *testing.T) {
			descriptor, ok := registry.Get(test.id)
			if !ok || descriptor.Partner == nil || descriptor.Partner.SignupURL != test.signupURL {
				t.Fatalf("unexpected partner descriptor: %+v", descriptor)
			}
			for _, route := range descriptor.Routes {
				if route.RouteMode != RouteNative {
					t.Errorf("unexpected conversion route: %+v", route)
				}
			}
			for _, endpoint := range []struct {
				params json.RawMessage
				want   string
			}{
				{nil, test.baseURL},
				{json.RawMessage(`{"base_url":"https://relay.example/tenant/"}`), "https://relay.example/tenant"},
			} {
				target, err := registry.Resolve(test.id, endpoint.params)
				if err != nil {
					t.Fatal(err)
				}
				if target.ProviderKind != ProviderMultiProtocolGateway || string(target.TargetConfig) != `{"base_url":"`+endpoint.want+`"}` {
					t.Fatalf("unexpected target: %+v", target)
				}
				for _, clientProtocol := range []protocol.Protocol{protocol.OpenAICompletions, protocol.Anthropic, protocol.Gemini} {
					if mode, ok := target.Mode(clientProtocol, execution.OperationChatCompletion); !ok || mode != RouteNative {
						t.Errorf("%s chat mode = %q / %t, want native", clientProtocol, mode, ok)
					}
				}
				if handling := target.ResponsesStoreHandling(protocol.OpenAIResponses, execution.OperationResponsesCreate); handling != ResponsesStoreHandlingStateless {
					t.Errorf("Responses storage = %q, want stateless", handling)
				}
			}
		})
	}
}
