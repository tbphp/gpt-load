package parameteroverride

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestRulesApplyArrayWildcards(t *testing.T) {
	for _, test := range []struct {
		name  string
		rules string
		body  string
		want  string
	}{
		{
			name:  "wildcard removal does not delete array elements or create missing parents",
			rules: `[{"remove":["/messages/*","/missing/*/field","/messages/*/missing/field"]}]`,
			body:  `{"messages":[null,{"content":"one"},[1,2]]}`,
			want:  `{"messages":[null,{"content":"one"},[1,2]]}`,
		},
		{
			name:  "remove message field without changing history",
			rules: `[{"match":{"protocol":"openai-completions","model":"DeepSeek-V4*"},"remove":["/messages/*/reasoning_content"]}]`,
			body:  `{"messages":[{"role":"user","content":"question"},{"role":"assistant","content":"answer","reasoning_content":"thought","tool_calls":[{"id":"call","function":{"arguments":"{\"n\":1}"}}]},{"role":"assistant","reasoning_content":null}],"precise":1.2300}`,
			want:  `{"messages":[{"role":"user","content":"question"},{"role":"assistant","content":"answer","tool_calls":[{"id":"call","function":{"arguments":"{\"n\":1}"}}]},{"role":"assistant"}],"precise":1.2300}`,
		},
		{
			name:  "set adds and overwrites fields while merging objects",
			rules: `[{"set":{"messages":{"*":{"tag":"new","metadata":{"enabled":true},"nullable":null}}}}]`,
			body:  `{"messages":[{"content":"one","tag":"old","metadata":{"keep":1}},{"content":"two","metadata":null}],"keep":[1,2]}`,
			want:  `{"messages":[{"content":"one","tag":"new","metadata":{"keep":1,"enabled":true},"nullable":null},{"content":"two","metadata":{"enabled":true},"tag":"new","nullable":null}],"keep":[1,2]}`,
		},
		{
			name:  "empty arrays and non-object elements remain intact",
			rules: `[{"remove":["/messages/*/old"],"set":{"messages":{"*":{"tag":true}},"empty":{"*":{"tag":true}}}}]`,
			body:  `{"messages":[null,1.2300,"<>&",true,[],[{}],{"old":1}],"empty":[]}`,
			want:  `{"messages":[null,1.2300,"<>&",true,[],[{}],{"tag":true}],"empty":[]}`,
		},
		{
			name:  "nested arrays and escaped object keys",
			rules: `[{"remove":["/messages/*/content/*/a~1b/~0old"],"set":{"messages":{"*":{"content":{"*":{"a/b":{"new":2}}}}}}}]`,
			body:  `{"messages":[{"content":[{"type":"text","a/b":{"~old":1,"keep":9007199254740993}},{"text":"hello"}]},{"content":[]}]}`,
			want:  `{"messages":[{"content":[{"type":"text","a/b":{"keep":9007199254740993,"new":2}},{"text":"hello","a/b":{"new":2}}]},{"content":[]}]}`,
		},
		{
			name:  "consecutive wildcards traverse multidimensional arrays",
			rules: `[{"remove":["/matrix/*/*/old"],"set":{"matrix":{"*":{"*":{"tag":true}}}}}]`,
			body:  `{"matrix":[[{"old":1,"keep":true}],[],[null,{"old":2}]]}`,
			want:  `{"matrix":[[{"keep":true,"tag":true}],[],[null,{"tag":true}]]}`,
		},
		{
			name:  "star remains a literal object field",
			rules: `[{"remove":["/messages/*/old"],"set":{"messages":{"*":{"tag":true}}}}]`,
			body:  `{"messages":{"*":{"old":1,"keep":2},"other":{"old":3}}}`,
			want:  `{"messages":{"*":{"keep":2,"tag":true},"other":{"old":3}}}`,
		},
		{
			name:  "remove precedes set and later rules see earlier edits",
			rules: `[{"remove":["/messages/*/nested"],"set":{"messages":{"*":{"nested":{"new":1},"tag":"first"}}}},{"remove":["/messages/*/tag"],"set":{"messages":{"*":{"nested":{"last":2}}}}}]`,
			body:  `{"messages":[{"nested":{"old":true},"content":"one"},{"content":"two"}]}`,
			want:  `{"messages":[{"nested":{"new":1,"last":2},"content":"one"},{"nested":{"new":1,"last":2},"content":"two"}]}`,
		},
		{
			name:  "whole-array replacement resets earlier element edits",
			rules: `[{"set":{"messages":{"*":{"discard":true}}}},{"set":{"messages":[{"content":"replacement","old":1}]}},{"remove":["/messages/*/old"],"set":{"messages":{"*":{"tag":true}}}}]`,
			body:  `{"messages":[{"content":"original"}]}`,
			want:  `{"messages":[{"content":"replacement","tag":true}]}`,
		},
		{
			name:  "whole-array replacement after wildcard retains old semantics",
			rules: `[{"remove":["/messages/*/old"]},{"set":{"messages":["replacement",null]}}]`,
			body:  `{"messages":[{"old":1}]}`,
			want:  `{"messages":["replacement",null]}`,
		},
		{
			name:  "duplicate targets use only the last array and object fields",
			rules: `[{"remove":["/messages/*/nested/old"],"set":{"messages":{"*":{"nested":{"tag":true}}}}}]`,
			body:  `{"messages":[{"nested":{"discard":true}}],"messages":[{"nested":{"old":1,"discard":true},"nested":{"keep":2}}]}`,
			want:  `{"messages":[{"nested":{"keep":2,"tag":true}}]}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rules, err := Compile(json.RawMessage(test.rules))
			if err != nil {
				t.Fatal(err)
			}
			body := []byte(test.body)
			var want any
			decodeJSONForTest(t, []byte(test.want), &want)
			for _, compiled := range []Rules{rules, rules.Clone(), rules} {
				gotBody, applied, err := compiled.Apply(protocol.OpenAICompletions, execution.OperationChatCompletion, "DeepSeek-V4-test", body)
				if err != nil || !applied {
					t.Fatalf("Apply() applied=%t err=%v", applied, err)
				}
				var got any
				decodeJSONForTest(t, gotBody, &got)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("got %s, want %s", gotBody, test.want)
				}
				if !bytes.Equal(body, []byte(test.body)) {
					t.Fatal("Apply() changed the original request")
				}
			}
		})
	}
}

func TestArrayWildcardOutputLimit(t *testing.T) {
	rules := compileRulesForTest(t, []any{map[string]any{"set": map[string]any{
		"messages": map[string]any{"*": map[string]any{"payload": strings.Repeat("x", 4096)}},
	}}})
	body := []byte(`{"messages":[` + strings.Repeat(`{},`, 63) + `{}]}`)
	expectedSize := int64(len(body) + 64*(len(`"payload":""`)+4096))
	for _, limit := range []int64{1024, 64 << 10, expectedSize - 1} {
		got, _, err := rules.ApplyWithLimit(protocol.OpenAICompletions, execution.OperationChatCompletion, "model", body, limit)
		if !errors.Is(err, ErrBodyTooLarge) || got != nil {
			t.Fatalf("limit=%d produced %d bytes, err=%v", limit, len(got), err)
		}
	}
	if got, _, err := rules.ApplyWithLimit(protocol.OpenAICompletions, execution.OperationChatCompletion, "model", body, expectedSize); err != nil || int64(len(got)) != expectedSize {
		t.Fatalf("exact limit=%d produced %d bytes, err=%v", expectedSize, len(got), err)
	}
	// 按最终结果限流；后续规则删除新增字段后，不应因中间逻辑大小而拒绝。
	rules.entries = append(rules.entries, rule{remove: [][]string{{"messages", "*", "payload"}}})
	got, _, err := rules.ApplyWithLimit(protocol.OpenAICompletions, execution.OperationChatCompletion, "model", body, int64(len(body)))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("final result within limit: body=%s err=%v", got, err)
	}
}

func TestArrayWildcardRejectsAmbiguousSetWithoutChangingRequest(t *testing.T) {
	for _, source := range []string{
		`{"*":"replacement"}`,
		`{"*":{"tag":true},"other":true}`,
	} {
		rules, err := Compile(json.RawMessage(`[{"set":{"messages":` + source + `}}]`))
		if err != nil {
			t.Fatal(err)
		}
		const original = `{"messages":[{"content":"original"}]}`
		body := []byte(original)
		_, _, err = rules.Apply(protocol.OpenAICompletions, execution.OperationChatCompletion, "model", body)
		if err == nil || string(body) != original {
			t.Fatalf("ambiguous set %s: body=%s err=%v", source, body, err)
		}
	}
}

func TestRemovedPathsMatchRulesAndOwnTheirData(t *testing.T) {
	rules, err := Compile(json.RawMessage(`[{"match":{"protocol":"openai-completions","model":"DeepSeek*"},"remove":["/messages/*/reasoning_content"]}]`))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		protocol  protocol.Protocol
		operation execution.Operation
		model     string
		match     bool
	}{
		{protocol.OpenAICompletions, execution.OperationChatCompletion, "DeepSeek-V4", true},
		{protocol.OpenAICompletions, execution.OperationChatCompletion, "other", false},
		{protocol.Anthropic, execution.OperationChatCompletion, "DeepSeek-V4", false},
		{protocol.OpenAICompletions, execution.OperationCountTokens, "DeepSeek-V4", false},
	} {
		got := rules.RemovedPaths(test.protocol, test.operation, test.model)
		if !test.match {
			if len(got) != 0 {
				t.Fatalf("unmatched rule returned removed paths: %v", got)
			}
			continue
		}
		want := [][]string{{"messages", "*", "reasoning_content"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("removed paths=%v, want %v", got, want)
		}
		got[0][2] = "changed"
		if !reflect.DeepEqual(rules.RemovedPaths(test.protocol, test.operation, test.model), want) {
			t.Fatal("mutating returned paths changed the compiled rules")
		}
	}
}
