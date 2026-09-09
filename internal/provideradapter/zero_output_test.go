package provideradapter

import (
	"encoding/json"
	"fmt"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestRegistryRejectsConvertedAnthropicZeroOutputBeforeDispatch(t *testing.T) {
	t.Parallel()

	for _, target := range []struct {
		channelID channel.ID
		config    json.RawMessage
	}{
		{channel.Gemini, nil},
		{channel.OpenAI, nil},
		{channel.OpenAICompatible, json.RawMessage(`{"base_url":"https://upstream.example/v1"}`)},
		{channel.Codex, json.RawMessage(`{"base_url":"https://chatgpt.com/backend-api/codex"}`)},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", target.channelID, stream), func(t *testing.T) {
				adapter := &recordingAdapter{}
				registry, err := NewRegistry(channel.NewRegistry(), completeBindings(adapter, adapter))
				if err != nil {
					t.Fatal(err)
				}
				spec := execution.AttemptSpec{
					ChannelID: string(target.channelID), TargetConfig: target.config,
					ClientProtocol: protocol.Anthropic, Operation: execution.OperationChatCompletion,
					RouteMode: execution.RouteConverted, UpstreamModel: "upstream-model",
					Body: []byte(`{"model":"client-model","max_tokens":0,"messages":[{"role":"user","content":"hello"}]}`),
				}
				var evidence *execution.ErrorEvidence
				if stream {
					events := 0
					result := registry.ExecuteStream(t.Context(), spec, func(execution.StreamEvent) error {
						events++
						return nil
					})
					if result.DispatchState != execution.DispatchNotSent || result.ResponseStarted || events != 0 {
						t.Errorf("stream result = %+v, events = %d", result, events)
					}
					evidence = result.Error
				} else {
					result := registry.Execute(t.Context(), spec)
					if result.DispatchState != execution.DispatchNotSent || result.ResponseStarted {
						t.Errorf("result = %+v", result)
					}
					evidence = result.Error
				}
				if evidence == nil || evidence.Kind != execution.ErrorKindConversionUnsupported ||
					evidence.Code != execution.ErrorCodeCriticalSemanticLoss ||
					evidence.OriginHint != execution.ErrorOriginInternal || evidence.ScopeHint != execution.ErrorScopeGroup {
					t.Errorf("conversion evidence = %+v", evidence)
				}
				if adapter.unaryCalls != 0 || adapter.streamCalls != 0 {
					t.Errorf("zero-output request reached adapter: %+v", adapter)
				}
			})
		}
	}
}

func TestRegistryZeroOutputGuardUsesEffectiveRequestAndOperation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		body       string
		protocol   protocol.Protocol
		operation  execution.Operation
		native     bool
		wantReject bool
	}{
		{name: "zero", body: `{"max_tokens":0}`, wantReject: true},
		{name: "negative zero", body: `{"max_tokens":-0}`, wantReject: true},
		{name: "decimal zero", body: `{"max_tokens":0.0}`, wantReject: true},
		{name: "exponent zero", body: `{"max_tokens":0e12}`, wantReject: true},
		{name: "small exponent zero", body: `{"max_tokens":0e-10000}`, wantReject: true},
		{name: "effective positive limit", body: `{"max_tokens":16}`},
		{name: "missing limit", body: `{}`},
		{name: "null limit", body: `{"max_tokens":null}`},
		{name: "string is not numeric zero", body: `{"max_tokens":"0"}`},
		{name: "nonzero must not underflow to zero", body: `{"max_tokens":1e-10000}`},
		{name: "nested limit", body: `{"metadata":{"max_tokens":0}}`},
		{name: "last value zero", body: `{"max_tokens":16,"max_tokens":0}`, wantReject: true},
		{name: "last value positive", body: `{"max_tokens":0,"max_tokens":16}`},
		{name: "malformed body keeps existing validation", body: `{"max_tokens":0`},
		{name: "native Anthropic", body: `{"max_tokens":0}`, native: true},
		{name: "count tokens", body: `{"max_tokens":0}`, operation: execution.OperationCountTokens},
		{name: "OpenAI zero retains existing behavior", body: `{"max_tokens":0}`, protocol: protocol.OpenAICompletions},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter := &recordingAdapter{}
			registry, err := NewRegistry(channel.NewRegistry(), completeBindings(adapter, adapter))
			if err != nil {
				t.Fatal(err)
			}
			clientProtocol := test.protocol
			if clientProtocol == "" {
				clientProtocol = protocol.Anthropic
			}
			operation := test.operation
			if operation == "" {
				operation = execution.OperationChatCompletion
			}
			spec := execution.AttemptSpec{
				ChannelID: string(channel.Gemini), ClientProtocol: clientProtocol, Operation: operation,
				RouteMode: execution.RouteConverted, UpstreamModel: "upstream-model", Body: []byte(test.body),
			}
			if test.native {
				spec.ChannelID = string(channel.Anthropic)
				spec.RouteMode = execution.RouteNative
			}
			result := registry.Execute(t.Context(), spec)
			if test.wantReject {
				if result.Error == nil || result.Error.Code != execution.ErrorCodeCriticalSemanticLoss || adapter.unaryCalls != 0 {
					t.Fatalf("result = %+v, adapter calls = %d", result, adapter.unaryCalls)
				}
			} else if result.Error != nil || adapter.unaryCalls != 1 {
				t.Fatalf("unrelated request changed: result = %+v, adapter calls = %d", result, adapter.unaryCalls)
			}
		})
	}
}
