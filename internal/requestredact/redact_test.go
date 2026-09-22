package requestredact

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRedactionPreservesOpaqueBlocksAndRejectsSignedTextMutation(t *testing.T) {
	c, _ := Compile([]Rule{{Pattern: "private", Replacement: "[VALUE]"}})
	image := []byte(`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","data":"private"}}]}]}`)
	got, err := c.Apply(image)
	if err != nil || !bytes.Equal(image, got) {
		t.Fatal("binary content changed")
	}
	_, err = c.Apply([]byte(`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"private","signature":"signed"}]}]}`))
	if !errors.Is(err, ErrContent) {
		t.Fatal("signed text was silently corrupted")
	}
}

func TestRedactionValidationAndLiteralReplacement(t *testing.T) {
	if _, err := Compile([]Rule{{Pattern: "[", Replacement: ""}}); err == nil {
		t.Fatal("invalid regex accepted")
	}
	issues := Validate([]Rule{{Pattern: ".*", Replacement: ""}, {Pattern: "[0-9]+", Replacement: "[NUMBER]"}})
	if len(issues) != 1 || issues[0].Warning != "broad_pattern" || issues[0].Error != "" {
		t.Fatalf("issues = %+v", issues)
	}
	c, err := Compile([]Rule{{Pattern: "(private)", Replacement: "$1"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.Text("private"); err != nil || got != "$1" {
		t.Fatalf("replacement is not literal: %q / %v", got, err)
	}
	c, err = Compile([]Rule{{Pattern: "x?|private", Replacement: "[VALUE]"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Text(strings.Repeat("a", 65537) + "private"); !errors.Is(err, ErrContent) {
		t.Fatal("match limit silently skipped the remaining text")
	}
}

func TestRedactionPreservesMessagesAndUnmatchedWireBytes(t *testing.T) {
	c, err := Compile([]Rule{{Pattern: `secret-\d+`, Replacement: "[SECRET]"}, {Pattern: "SECRET", Replacement: "other"}})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{ "model":"secret-1", "messages":[{"role":"system","content":"secret-1"},{"role":"user","content":[{"type":"text","text":"secret-2"}]},{"role":"assistant","tool_calls":[{"id":"secret-3","type":"function","function":{"name":"secret-4","arguments":"{\"value\":\"secret-5\"}"}}]},{"role":"tool","tool_call_id":"secret-3","content":"secret-6"}], "temperature":1.00, "prompt_cache_key":"secret-7" }`)
	want := []byte(`{ "model":"secret-1", "messages":[{"role":"system","content":"[SECRET]"},{"role":"user","content":[{"type":"text","text":"[SECRET]"}]},{"role":"assistant","tool_calls":[{"id":"secret-3","type":"function","function":{"name":"secret-4","arguments":"{\"value\":\"[SECRET]\"}"}}]},{"role":"tool","tool_call_id":"secret-3","content":"[SECRET]"}], "temperature":1.00, "prompt_cache_key":"secret-7" }`)
	got, err := c.Apply(body)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("got %s, err %v", got, err)
	}
	if !bytes.Contains(body, []byte("secret-6")) {
		t.Fatal("original request mutated")
	}
}

func TestRedactionCoversNativeContentAndKeepsHistoryStable(t *testing.T) {
	c, _ := Compile([]Rule{{Pattern: "private", Replacement: "[VALUE]"}})
	for _, body := range []string{
		`{"instructions":"private","input":[{"role":"user","content":[{"type":"input_text","text":"private"}]},{"type":"function_call_output","call_id":"keep","output":"private"}]}`,
		`{"system":[{"type":"text","text":"private","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"keep","name":"keep","input":{"value":"private"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"keep","content":"private"}]}]}`,
		`{"systemInstruction":{"parts":[{"text":"private"}]},"contents":[{"role":"model","parts":[{"functionCall":{"name":"keep","args":{"value":"private"}}}]},{"role":"user","parts":[{"functionResponse":{"name":"keep","response":{"value":"private"}}}]}]}`,
		`{"messages":[{"role":"assistant","content":[{"type":"server_tool_use","id":"keep","name":"bash","input":{"command":"private"}}]}]}`,
		`{"contents":[{"role":"model","parts":[{"executableCode":{"language":"PYTHON","code":"private"}}]}]}`,
	} {
		got, err := c.Apply([]byte(body))
		if err != nil || bytes.Contains(got, []byte("private")) || !json.Valid(got) {
			t.Fatalf("got %s / %v", got, err)
		}
	}
	first := `{"messages":[{"role":"user","content":"private"}]}`
	next := `{"messages":[{"role":"user","content":"private"},{"role":"user","content":"next private"}]}`
	a, _ := c.Apply([]byte(first))
	b, _ := c.Apply([]byte(next))
	if !bytes.HasPrefix(b, a[:len(a)-2]) {
		t.Fatal("appending a message changed history")
	}
}

func TestRedactionCoversDecisionsStateWithoutChangingQuestionIdentifiers(t *testing.T) {
	c, _ := Compile([]Rule{{Pattern: "private-value", Replacement: "[VALUE]"}})
	for _, state := range []string{
		`"private-value"`,
		`{"customer":"private-value","nested":[{"note":"private-value"}]}`,
		`["private-value",{"note":"private-value"}]`,
	} {
		body := []byte(`{"model":"public","state":` + state + `,"questions":{"private-value":{"type":"choice","instructions":{"note":"private-value"},"criteria":{"private-value":"private-value"}}}}`)
		got, err := c.ApplyDecisions(body)
		if err != nil || bytes.Contains(got, []byte(`"state":`+state)) || !bytes.Contains(got, []byte(`"questions":{"private-value"`)) || !bytes.Contains(got, []byte(`"note":"[VALUE]"`)) || !bytes.Contains(got, []byte(`"private-value":"[VALUE]"`)) {
			t.Fatalf("state=%s, got=%s / %v", state, got, err)
		}
	}
}

func TestRedactionPreservesResponsesToolOutputContentBlockMetadata(t *testing.T) {
	c, _ := Compile([]Rule{{Pattern: `private|[0-9]+|input_image|input_file`, Replacement: "[VALUE]"}})
	body := []byte(`{"input":[{"type":"function_call_output","call_id":"call-123","output":[{"type":"input_text","text":"private 123"},{"type":"input_image","file_id":"file-123","image_url":"https://example.com/private/123"},{"type":"input_file","file_id":"file-456","file_data":"private123","filename":"private123.txt"}]}]}`)
	want := []byte(`{"input":[{"type":"function_call_output","call_id":"call-123","output":[{"type":"input_text","text":"[VALUE] [VALUE]"},{"type":"input_image","file_id":"file-123","image_url":"https://example.com/private/123"},{"type":"input_file","file_id":"file-456","file_data":"private123","filename":"private123.txt"}]}]}`)
	got, err := c.Apply(body)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("got %s / %v", got, err)
	}
}

func TestRedactionLimitsTotalPatchesIncludingNestedArguments(t *testing.T) {
	c, _ := Compile([]Rule{{Pattern: "x", Replacement: "y"}})
	values := strings.Repeat(`"x",`, 65536)
	inner := `{"values":[` + values[:len(values)-1] + `]}`
	arguments, err := json.Marshal(inner)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"input":"x","arguments":` + string(arguments) + `}`)
	if _, err := c.Apply(body); !errors.Is(err, ErrContent) {
		t.Fatalf("patch budget not enforced: %v", err)
	}
}

func TestRedactionMergesOverlapsAndDoesNotRewriteReplacements(t *testing.T) {
	c, _ := Compile([]Rule{{Pattern: "abc", Replacement: "[ONE]"}, {Pattern: "bcde", Replacement: "[TWO]"}, {Pattern: "ONE", Replacement: "changed"}})
	got, err := c.Apply([]byte(`{"input":"abcde abc"}`))
	if err != nil || string(got) != `{"input":"[ONE] [ONE]"}` {
		t.Fatalf("got %s / %v", got, err)
	}
}

func TestRedactionEmptyRulesAndUnmatchedContentAreByteIdentical(t *testing.T) {
	for _, rules := range [][]Rule{nil, {{Pattern: "missing", Replacement: "x"}}} {
		c, _ := Compile(rules)
		body := []byte("{ \"messages\": [], \"unknown\":1.00 }\n")
		got, err := c.Apply(body)
		if err != nil || !bytes.Equal(got, body) {
			t.Fatal("untouched body changed")
		}
	}
}
