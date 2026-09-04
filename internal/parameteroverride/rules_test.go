package parameteroverride

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestRulesApplyMatchingRulesInOrder(t *testing.T) {
	rules := compileRulesForTest(t, []any{
		map[string]any{
			"match": map[string]any{},
			"set": map[string]any{
				"temperature":  json.Number("0.7"),
				"nested":       map[string]any{"keep": true, "replace": "first"},
				"items":        []any{"replacement"},
				"literal_null": nil,
			},
		},
		map[string]any{
			"match": map[string]any{
				"protocol": string(protocol.OpenAIResponses),
				"model":    "gpt-5*",
			},
			"remove": []any{"/temperature", "/nested/keep", "/a~1b/c~0d", "/missing"},
			"set": map[string]any{
				"temperature": json.Number("1.25"),
				"nested":      map[string]any{"replace": "last"},
			},
		},
	})

	body, applied, err := rules.Apply(
		protocol.OpenAIResponses,
		execution.OperationResponsesCreate,
		"gpt-5.4",
		[]byte(`{"model":"gpt-5.4","items":[1,2],"nested":{"keep":false,"original":1},"a/b":{"c~d":true,"keep":true}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("Apply() applied = false, want true")
	}
	var got any
	decodeJSONForTest(t, body, &got)
	want := map[string]any{
		"model":        "gpt-5.4",
		"temperature":  json.Number("1.25"),
		"items":        []any{"replacement"},
		"literal_null": nil,
		"a/b":          map[string]any{"keep": true},
		"nested": map[string]any{
			"original": json.Number("1"),
			"replace":  "last",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Apply() body = %#v, want %#v", got, want)
	}
}

func TestRulesMatchClientProtocolAndClientModel(t *testing.T) {
	rules := compileRulesForTest(t, []any{
		map[string]any{
			"match": map[string]any{
				"protocol": string(protocol.Anthropic),
				"model":    "public-*",
			},
			"set": map[string]any{"max_tokens": json.Number("4096")},
		},
	})

	for _, test := range []struct {
		name      string
		protocol  protocol.Protocol
		operation execution.Operation
		model     string
		applied   bool
	}{
		{name: "match", protocol: protocol.Anthropic, operation: execution.OperationChatCompletion, model: "public-sonnet", applied: true},
		{name: "protocol", protocol: protocol.OpenAICompletions, operation: execution.OperationChatCompletion, model: "public-sonnet"},
		{name: "model", protocol: protocol.Anthropic, operation: execution.OperationChatCompletion, model: "private-sonnet"},
		{name: "utility operation", protocol: protocol.Anthropic, operation: execution.OperationCountTokens, model: "public-sonnet"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, applied, err := rules.Apply(test.protocol, test.operation, test.model, []byte(`{"model":"public-sonnet"}`))
			if err != nil {
				t.Fatal(err)
			}
			if applied != test.applied {
				t.Fatalf("Apply() applied = %t, want %t", applied, test.applied)
			}
		})
	}
}

func TestCompileRejectsInvalidRules(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{name: "not array", value: map[string]any{}},
		{name: "empty action", value: []any{map[string]any{"match": map[string]any{}}}},
		{name: "enabled field", value: []any{map[string]any{"enabled": true, "set": map[string]any{"temperature": 1}}}},
		{name: "operation condition", value: []any{map[string]any{"match": map[string]any{"operation": "chat_completion"}, "set": map[string]any{"temperature": 1}}}},
		{name: "empty protocol", value: []any{map[string]any{"match": map[string]any{"protocol": ""}, "set": map[string]any{"temperature": 1}}}},
		{name: "padded protocol", value: []any{map[string]any{"match": map[string]any{"protocol": " anthropic "}, "set": map[string]any{"temperature": 1}}}},
		{name: "empty model", value: []any{map[string]any{"match": map[string]any{"model": ""}, "set": map[string]any{"temperature": 1}}}},
		{name: "padded model", value: []any{map[string]any{"match": map[string]any{"model": " gpt-5"}, "set": map[string]any{"temperature": 1}}}},
		{name: "invalid protocol", value: []any{map[string]any{"match": map[string]any{"protocol": "openai"}, "set": map[string]any{"temperature": 1}}}},
		{name: "wildcard only", value: []any{map[string]any{"match": map[string]any{"model": "*"}, "set": map[string]any{"temperature": 1}}}},
		{name: "middle wildcard", value: []any{map[string]any{"match": map[string]any{"model": "gpt-*mini"}, "set": map[string]any{"temperature": 1}}}},
		{name: "forbidden set", value: []any{map[string]any{"set": map[string]any{"MODEL": "other"}}}},
		{name: "empty root set field", value: []any{map[string]any{"set": map[string]any{"": 1}}}},
		{name: "empty nested set field", value: []any{map[string]any{"set": map[string]any{"metadata": map[string]any{"": 1}}}}},
		{name: "forbidden remove", value: []any{map[string]any{"remove": []any{"/store/value"}}}},
		{name: "root remove", value: []any{map[string]any{"remove": []any{""}}}},
		{name: "empty root remove field", value: []any{map[string]any{"remove": []any{"/"}}}},
		{name: "empty nested remove field", value: []any{map[string]any{"remove": []any{"/metadata/"}}}},
		{name: "array remove", value: []any{map[string]any{"remove": []any{"/tools/0"}}}},
		{name: "deep remove", value: []any{map[string]any{"remove": []any{strings.Repeat("/field", maxJSONDepth+1)}}}},
		{name: "unsafe integer", value: []any{map[string]any{"set": map[string]any{"value": json.Number("9007199254740992")}}}},
		{name: "overflowing number", value: []any{map[string]any{"set": map[string]any{"value": json.Number("1e400")}}}},
		{name: "lossy decimal", value: []any{map[string]any{"set": map[string]any{"value": json.Number("0.10000000000000001")}}}},
		{name: "negative zero", value: []any{map[string]any{"set": map[string]any{"value": json.Number("-0")}}}},
		{name: "negative decimal zero", value: []any{map[string]any{"set": map[string]any{"value": json.Number("-0.0")}}}},
		{name: "negative exponent zero", value: []any{map[string]any{"set": map[string]any{"value": json.Number("-0e3")}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Compile(test.value); err == nil {
				t.Fatal("Compile() accepted invalid rules")
			}
		})
	}
}

func TestCompileAcceptsNumbersThatRoundTripThroughManagementUI(t *testing.T) {
	for _, value := range []json.Number{
		"9007199254740991",
		"-9007199254740991",
		"0.1",
		"1.0",
		"1e3",
		"1e-7",
	} {
		t.Run(value.String(), func(t *testing.T) {
			if _, err := Compile([]any{map[string]any{"set": map[string]any{"value": value}}}); err != nil {
				t.Fatalf("Compile(%s) error = %v", value, err)
			}
		})
	}
}

func TestRulesRejectOversizedBodiesOnlyAfterRuleMatch(t *testing.T) {
	rules := compileRulesForTest(t, []any{
		map[string]any{
			"match": map[string]any{"model": "matched"},
			"set":   map[string]any{"temperature": json.Number("0.5")},
		},
	})
	prefix := []byte(`{"model":"matched","input":"`)
	suffix := []byte(`"}`)
	body := append(prefix, bytes.Repeat([]byte("x"), maxApplyBytes-len(prefix)-len(suffix)+1)...)
	body = append(body, suffix...)

	if _, applied, err := rules.Apply(
		protocol.OpenAICompletions,
		execution.OperationChatCompletion,
		"matched",
		body,
	); err == nil || applied || !strings.Contains(err.Error(), "body exceeds") {
		t.Fatalf("matching Apply() = applied %t, error %v", applied, err)
	}
	got, applied, err := rules.Apply(
		protocol.OpenAICompletions,
		execution.OperationChatCompletion,
		"other",
		body,
	)
	if err != nil || applied || !bytes.Equal(got, body) {
		t.Fatalf("non-matching Apply() = %d bytes, applied %t, error %v", len(got), applied, err)
	}
}

func TestRulesDoNotInterpretVariables(t *testing.T) {
	rules := compileRulesForTest(t, []any{
		map[string]any{"set": map[string]any{"metadata": "${REQUEST_ID}"}},
	})
	body, applied, err := rules.Apply(
		protocol.OpenAICompletions,
		execution.OperationChatCompletion,
		"gpt-4o",
		[]byte(`{"model":"gpt-4o"}`),
	)
	if err != nil || !applied {
		t.Fatalf("Apply() = applied %t, error %v", applied, err)
	}
	var got map[string]any
	decodeJSONForTest(t, body, &got)
	if got["metadata"] != "${REQUEST_ID}" {
		t.Fatalf("metadata = %#v", got["metadata"])
	}
}

func TestRulesUseFixedOperationAllowlist(t *testing.T) {
	rules := compileRulesForTest(t, []any{
		map[string]any{"set": map[string]any{"marker": true}},
	})
	tests := []struct {
		protocol  protocol.Protocol
		operation execution.Operation
		want      bool
	}{
		{protocol.OpenAICompletions, execution.OperationChatCompletion, true},
		{protocol.OpenAIResponses, execution.OperationResponsesCreate, true},
		{protocol.OpenAIImages, execution.OperationImagesGenerate, true},
		{protocol.OpenAIEmbeddings, execution.OperationEmbeddingsCreate, true},
		{protocol.Anthropic, execution.OperationChatCompletion, true},
		{protocol.Gemini, execution.OperationChatCompletion, true},
		{protocol.OpenAICompletions, execution.OperationListModels, false},
		{protocol.OpenAIResponses, execution.OperationResponsesCompact, false},
		{protocol.OpenAIResponses, execution.OperationResponsesInputTokens, false},
		{protocol.OpenAIImages, execution.OperationImagesEdit, false},
		{protocol.Anthropic, execution.OperationCountTokens, false},
		{protocol.Gemini, execution.OperationProbe, false},
	}
	for _, test := range tests {
		_, applied, err := rules.Apply(test.protocol, test.operation, "model", []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if applied != test.want {
			t.Errorf("Apply(%s, %s) = %t, want %t", test.protocol, test.operation, applied, test.want)
		}
	}
}

func TestRulesCloneOwnsMutableValues(t *testing.T) {
	rules := compileRulesForTest(t, []any{
		map[string]any{
			"set":    map[string]any{"nested": map[string]any{"value": "original"}},
			"remove": []any{"/removed"},
		},
	})
	cloned := rules.Clone()
	rules.entries[0].set["nested"].(map[string]any)["value"] = "changed"
	rules.entries[0].remove[0][0] = "other"

	body, applied, err := cloned.Apply(
		protocol.OpenAICompletions,
		execution.OperationChatCompletion,
		"model",
		[]byte(`{"removed":true}`),
	)
	if err != nil || !applied || string(body) != `{"nested":{"value":"original"}}` {
		t.Fatalf("cloned Apply() = %s, %t, %v", body, applied, err)
	}
}

func compileRulesForTest(t *testing.T, value any) Rules {
	t.Helper()
	rules, err := Compile(value)
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

func decodeJSONForTest(t *testing.T, body []byte, target any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		t.Fatal(err)
	}
}
