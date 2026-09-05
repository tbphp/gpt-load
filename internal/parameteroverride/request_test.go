package parameteroverride

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestApplyAllocationsDoNotScaleWithUntouchedElements(t *testing.T) {
	var fields strings.Builder
	for index := range 8192 {
		fmt.Fprintf(&fields, `"field%d":0,`, index)
	}
	for name, payload := range map[string]string{
		"numbers":       `{"input":[` + strings.Repeat("0,", 32768) + `0]}`,
		"objects":       `{"input":[` + strings.Repeat(`{"a":0},`, 8192) + `{}]}`,
		"root fields":   `{` + fields.String() + `"last":0}`,
		"nested fields": `{"nested":{` + fields.String() + `"last":0}}`,
	} {
		t.Run(name, func(t *testing.T) {
			rules := compileRulesForTest(t, []any{map[string]any{
				"set": map[string]any{"marker": true, "nested": map[string]any{"marker": true}},
			}})
			body := []byte(payload)
			allocations := testing.AllocsPerRun(1, func() {
				result, applied, err := rules.Apply(protocol.OpenAICompletions, execution.OperationChatCompletion, "model", body)
				if err != nil || !applied || !json.Valid(result) {
					t.Fatalf("Apply() applied = %t, error = %v", applied, err)
				}
			})
			if allocations > 200 {
				t.Fatalf("Apply() allocations = %.0f for %d bytes; untouched elements must not become heap objects", allocations, len(body))
			}
		})
	}
}

func TestApplyPreservesSequentialMergeAndRemoveSemantics(t *testing.T) {
	for _, body := range []string{
		`{"nested":{"keep":1,"remove":2},"array":[0,{},null],"text":"<>&","number":-0}`,
		`{"nested":[0,1],"array":{"0":true},"0":{"child":1},"a/b":{"~":1}}`,
		`{"nested":{"first":1},"nested":{"second":2},"\u0061":0,"a":1}`,
		`{"nested":null,"empty":{},"literal":"\\\"{}[]","a.b":3}`,
		`{"nested":{"keep":1},"\ud800":1,"\ud800\udc00":2}`,
	} {
		t.Run(body, func(t *testing.T) {
			rules := compileRulesForTest(t, []any{
				map[string]any{"remove": []any{"/nested/remove", "/array/child"}, "set": map[string]any{"nested": map[string]any{"first": true}, "empty": map[string]any{}, "0": map[string]any{"new": true}}},
				map[string]any{"remove": []any{"/nested", "/a~1b/~0"}, "set": map[string]any{"nested": map[string]any{"last": true}, "array": []any{1, nil}, "a": nil}},
				map[string]any{"remove": []any{"/nested/last"}, "set": map[string]any{"nested": map[string]any{"final": true}}},
				map[string]any{"set": map[string]any{"empty": 0}},
				map[string]any{"set": map[string]any{"empty": map[string]any{}}},
				map[string]any{"set": map[string]any{"empty": map[string]any{"value": nil}}},
			})
			var want map[string]any
			decodeJSONForTest(t, []byte(body), &want)
			for _, entry := range rules.entries {
				for _, path := range entry.remove {
					removeObjectField(want, path)
				}
				mergeObject(want, entry.set)
			}
			gotBody, _, err := rules.Apply(protocol.OpenAICompletions, execution.OperationChatCompletion, "model", []byte(body))
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			decodeJSONForTest(t, gotBody, &got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %s, want %#v", gotBody, want)
			}
			if bytes.Contains(gotBody, []byte(`\u003c`)) {
				t.Fatal("unchanged strings were HTML escaped")
			}
		})
	}
}

func TestApplyDoesNotRetainRemovedPayloadInOutput(t *testing.T) {
	rules := compileRulesForTest(t, []any{map[string]any{"remove": []any{"/input"}}})
	body := []byte(`{"input":"` + strings.Repeat("x", 1<<20) + `","keep":true}`)
	got, applied, err := rules.Apply(protocol.OpenAICompletions, execution.OperationChatCompletion, "model", body)
	if err != nil || !applied || string(got) != `{"keep":true}` {
		t.Fatalf("Apply() = %s, %t, %v", got, applied, err)
	}
	if cap(got) > 1024 {
		t.Fatalf("small output retained a %d-byte backing buffer", cap(got))
	}
	if !bytes.Contains(body, []byte(`"input"`)) {
		t.Fatal("original request was modified")
	}
}

func FuzzApplyMatchesTreeSemantics(f *testing.F) {
	for _, body := range []string{
		`{}`, `{"nested":{"keep":1,"remove":2}}`, `{"nested":[{}],"input":[0,1]}`,
		`{"nested":{"x":1},"nested":{"y":2}}`, `{"\ud800":1}`,
	} {
		f.Add(body)
	}
	f.Fuzz(func(t *testing.T, body string) {
		if len(body) > 4096 || !json.Valid([]byte(body)) {
			return
		}
		var want map[string]any
		if decodeJSON([]byte(body), &want) != nil || want == nil {
			return
		}
		rules := compileRulesForTest(t, []any{
			map[string]any{"remove": []any{"/nested/remove"}, "set": map[string]any{"nested": map[string]any{"keep": true}}},
			map[string]any{"remove": []any{"/nested/keep"}, "set": map[string]any{"input": []any{1, nil}, "nested": map[string]any{"final": true}}},
		})
		for _, entry := range rules.entries {
			for _, path := range entry.remove {
				removeObjectField(want, path)
			}
			mergeObject(want, entry.set)
		}
		gotBody, _, err := rules.Apply(protocol.OpenAICompletions, execution.OperationChatCompletion, "model", []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		decodeJSONForTest(t, gotBody, &got)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %s, want %#v", gotBody, want)
		}
	})
}

func BenchmarkApplyDenseRequest(b *testing.B) {
	rules, err := Compile([]any{map[string]any{"set": map[string]any{"temperature": 0.5}}})
	if err != nil {
		b.Fatal(err)
	}
	body := []byte(`{"input":[` + strings.Repeat("0,", 1<<19) + `0]}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for b.Loop() {
		if _, _, err := rules.Apply(protocol.OpenAICompletions, execution.OperationChatCompletion, "model", body); err != nil {
			b.Fatal(err)
		}
	}
}

// 保留原整树算法作为小数据的独立语义参照，不再用于数据面请求。
func mergeObject(target, source map[string]any) {
	for key, value := range source {
		sourceObject, sourceIsObject := value.(map[string]any)
		targetObject, targetIsObject := target[key].(map[string]any)
		if sourceIsObject && targetIsObject {
			mergeObject(targetObject, sourceObject)
			continue
		}
		target[key] = cloneJSON(value)
	}
}

func removeObjectField(target map[string]any, pointer []string) {
	current := target
	for _, segment := range pointer[:len(pointer)-1] {
		nested, ok := current[segment].(map[string]any)
		if !ok {
			return
		}
		current = nested
	}
	delete(current, pointer[len(pointer)-1])
}
