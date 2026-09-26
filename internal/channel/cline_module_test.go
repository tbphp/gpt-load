package channel

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestClineChannelContract(t *testing.T) {
	registry := NewRegistry()
	target, err := registry.Resolve(ID("cline"), nil)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		BaseURL string `json:"base_url"`
	}
	if err := json.Unmarshal(target.TargetConfig, &config); err != nil || config.BaseURL != "https://api.cline.bot/api/v1" {
		t.Fatalf("Cline target = %s, err=%v", target.TargetConfig, err)
	}
	for _, test := range []struct {
		protocol  protocol.Protocol
		operation execution.Operation
		mode      execution.RouteMode
	}{
		{protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteNative},
		{protocol.OpenAICompletions, execution.OperationListModels, execution.RouteNative},
		{protocol.OpenAIResponses, execution.OperationResponsesCreate, execution.RouteConverted},
		{protocol.Anthropic, execution.OperationChatCompletion, execution.RouteConverted},
		{protocol.Gemini, execution.OperationChatCompletion, execution.RouteConverted},
	} {
		if mode, ok := target.Mode(test.protocol, test.operation); !ok || mode != test.mode {
			t.Errorf("%s/%s = %s, %t; want %s", test.protocol, test.operation, mode, ok, test.mode)
		}
	}
	if target.SupportsResponsesLifecycle() {
		t.Fatal("Cline must not advertise upstream Responses state")
	}
	for _, value := range []protocol.Protocol{protocol.OpenAIImages, protocol.OpenAIEmbeddings, protocol.GeminiEmbeddings, protocol.Rerank} {
		if _, ok := target.Mode(value, execution.OperationProbe); ok {
			t.Errorf("Cline unexpectedly advertises %s", value)
		}
	}
	descriptor, ok := registry.Get(ID("cline"))
	if !ok || descriptor.Connection.Type != "api_key" || descriptor.Icon != "cline" {
		t.Fatalf("Cline descriptor = %+v", descriptor)
	}
}
