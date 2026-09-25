package requestredact

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"gpt-load/internal/platform/encryption"
)

func syntheticRedactionCipher(t *testing.T) encryption.RedactionCipher {
	t.Helper()
	service, err := encryption.NewService("synthetic-requestredact-test-master-key-2026-09-23")
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := service.NewRedactionCipher(42)
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func TestEncryptTextIsStableDistinctAndSkipsAuthenticatedTokens(t *testing.T) {
	cipher := syntheticRedactionCipher(t)
	compiled, err := Compile([]Rule{{Pattern: `[a-z]+@example\.invalid`, Replacement: "SHOULD_NOT_USE", Mode: ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := compiled.TextWithCipher("alice@example.invalid bob@example.invalid alice@example.invalid", cipher)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Fields(first)
	if len(parts) != 3 || parts[0] != parts[2] || parts[0] == parts[1] || strings.Contains(first, "SHOULD_NOT_USE") {
		t.Fatalf("deterministic encrypted text = %q", first)
	}
	if got, err := cipher.RestoreText(first); err != nil || got != "alice@example.invalid bob@example.invalid alice@example.invalid" {
		t.Fatalf("restored text = %q, error = %v", got, err)
	}
	for _, input := range []string{first, parts[0] + ",alice@example.invalid"} {
		got, err := compiled.TextWithCipher(input, cipher)
		want := first
		if input != first {
			want = parts[0] + "," + parts[0]
		}
		if err != nil || got != want {
			t.Fatalf("idempotent text = %q, error = %v, want %q", got, err, want)
		}
	}
	lookalike, err := Compile([]Rule{{Pattern: `gld1_999_bad`, Mode: ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := lookalike.TextWithCipher("gld1_999_bad", cipher)
	if err != nil || got == "gld1_999_bad" {
		t.Fatalf("invalid lookalike text = %q, error = %v", got, err)
	}
	if restored, err := cipher.RestoreText(got); err != nil || restored != "gld1_999_bad" {
		t.Fatalf("invalid lookalike restored = %q, error = %v", restored, err)
	}
}

func TestAuthenticatedTokenIsSkippedButCrossingMatchFailsClosed(t *testing.T) {
	cipher := syntheticRedactionCipher(t)
	token, err := cipher.EncryptToken("private")
	if err != nil {
		t.Fatal(err)
	}
	inside, err := Compile([]Rule{{Pattern: `gld1_`, Replacement: "[MASK]"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := inside.TextWithCipher(token, cipher); err != nil || got != token {
		t.Fatalf("protected token = %q, error = %v", got, err)
	}
	crossing, err := Compile([]Rule{{Pattern: `password:\S+`, Mode: ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := crossing.TextWithCipher("password:"+token+"suffix", cipher); !errors.Is(err, ErrContent) {
		t.Fatalf("cross-token match error = %v, want ErrContent", err)
	}
}

func TestEncryptTextPreservesRulePriorityAndFailsClosed(t *testing.T) {
	cipher := syntheticRedactionCipher(t)
	for _, test := range []struct {
		name     string
		rules    []Rule
		wantText string
	}{
		{"replace wins overlap", []Rule{{Pattern: "abc", Replacement: "[MASK]"}, {Pattern: "bcde", Mode: ModeEncrypt}}, "[MASK]"},
		{"encrypt wins overlap", []Rule{{Pattern: "abc", Mode: ModeEncrypt}, {Pattern: "bcde", Replacement: "[MASK]"}}, "abcde"},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := Compile(test.rules)
			if err != nil {
				t.Fatal(err)
			}
			got, err := compiled.TextWithCipher("abcde", cipher)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := cipher.RestoreText(got)
			if err != nil || restored != test.wantText {
				t.Fatalf("overlap output = %q, restored = %q, error = %v", got, restored, err)
			}
		})
	}
	compiled, err := Compile([]Rule{{Pattern: "private", Mode: ModeEncrypt}, {Pattern: "public", Replacement: "[MASK]"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := compiled.TextWithCipher("private public", cipher)
	if err != nil || !strings.HasSuffix(got, " [MASK]") || strings.Contains(got, "private") {
		t.Fatalf("mixed rules output = %q, error = %v", got, err)
	}
	if _, err := compiled.TextWithCipher("private", nil); !errors.Is(err, ErrContent) {
		t.Fatalf("missing cipher error = %v, want ErrContent", err)
	}
	if got, err := compiled.TextWithCipher("unmatched", nil); err != nil || got != "unmatched" {
		t.Fatalf("unmatched missing-cipher text = %q, error = %v", got, err)
	}
	broken := failedEncryptCipher{RedactionCipher: cipher}
	if _, err := compiled.TextWithCipher("private", broken); !errors.Is(err, ErrContent) {
		t.Fatalf("encryption failure = %v, want ErrContent", err)
	}
}

type failedEncryptCipher struct{ encryption.RedactionCipher }

func (failedEncryptCipher) EncryptToken(string) (string, error) {
	return "", errors.New("synthetic encryption failure")
}

type countingEncryptCipher struct {
	encryption.RedactionCipher
	calls int
}

func (c *countingEncryptCipher) EncryptToken(value string) (string, error) {
	c.calls++
	return c.RedactionCipher.EncryptToken(value)
}

func TestEncryptTextRejectsOversizedOutputBeforeEncryption(t *testing.T) {
	cipher := &countingEncryptCipher{RedactionCipher: syntheticRedactionCipher(t)}
	compiled, err := Compile([]Rule{
		{Pattern: "x", Replacement: strings.Repeat("y", 4096)},
		{Pattern: "private", Mode: ModeEncrypt},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiled.TextWithCipher(strings.Repeat("x", 32769)+"private", cipher); !errors.Is(err, ErrContent) {
		t.Fatalf("oversized output error = %v, want ErrContent", err)
	}
	if cipher.calls != 0 {
		t.Fatalf("encrypted %d values before rejecting oversized output", cipher.calls)
	}
}

func TestApplyWithCipherPreservesJSONStructureAndSignedThinking(t *testing.T) {
	cipher := syntheticRedactionCipher(t)
	compiled, err := Compile([]Rule{{Pattern: `alice@example\.invalid`, Mode: ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{ "model":"public", "messages":[{"role":"assistant","tool_calls":[{"id":"call_1","function":{"name":"lookup","arguments":"{\"password\":\"alice@example.invalid\",\"n\":9007199254740993}"}}]},{"role":"user","content":"alice@example.invalid"}], "temperature":1.00 }`)
	got, err := compiled.ApplyWithCipher(body, cipher)
	if err != nil || !json.Valid(got) {
		t.Fatalf("ApplyWithCipher() = %s, error = %v", got, err)
	}
	token, err := cipher.EncryptToken("alice@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(strings.ReplaceAll(string(body), "alice@example.invalid", token))
	if !bytes.Equal(got, want) || !bytes.Contains(body, []byte("alice@example.invalid")) {
		t.Fatalf("JSON bytes or input changed:\n got: %s\nwant: %s", got, want)
	}
	// 没有还原记录的签名思考由上游生成，原样发回。
	signed := `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"alice@example.invalid","signature":"signed"}]}]}`
	if got, err := compiled.ApplyWithCipher([]byte(signed), cipher); err != nil || string(got) != signed {
		t.Fatalf("signed thinking = %s / %v", got, err)
	}
}

func TestApplyDecisionsWithCipherPreservesQuestionKeys(t *testing.T) {
	cipher := syntheticRedactionCipher(t)
	compiled, err := Compile([]Rule{{Pattern: `alice@example\.invalid`, Mode: ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"state":{"email":"alice@example.invalid"},"questions":{"alice@example.invalid":{"criteria":{"note":"alice@example.invalid"}}}}`)
	got, err := compiled.ApplyDecisionsWithCipher(body, cipher)
	if err != nil {
		t.Fatal(err)
	}
	token, err := cipher.EncryptToken("alice@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"state":{"email":"` + token + `"},"questions":{"alice@example.invalid":{"criteria":{"note":"` + token + `"}}}}`)
	if !bytes.Equal(got, want) {
		t.Fatalf("Decisions output = %s, want %s", got, want)
	}
}

func TestApplyCoversDocumentTextPromptVariablesAndReasoning(t *testing.T) {
	cipher := syntheticRedactionCipher(t)
	const secret = "alice@example.invalid"
	cases := []struct {
		name      string
		body      string
		protected []string
		kept      []string
	}{
		{
			name:      "text document",
			body:      `{"messages":[{"role":"user","content":[{"type":"document","title":"alice@example.invalid","context":"alice@example.invalid","source":{"type":"text","media_type":"text/plain","data":"contact alice@example.invalid"}}]}]}`,
			protected: []string{"messages.0.content.0.title", "messages.0.content.0.context", "messages.0.content.0.source.data"},
		},
		{
			name:      "content document",
			body:      `{"messages":[{"role":"user","content":[{"type":"document","source":{"type":"content","content":[{"type":"text","text":"alice@example.invalid"}]}}]}]}`,
			protected: []string{"messages.0.content.0.source.content.0.text"},
		},
		{
			name:      "binary document",
			body:      `{"messages":[{"role":"user","content":[{"type":"document","title":"alice@example.invalid","source":{"type":"base64","media_type":"application/pdf","data":"alice@example.invalid"}}]}]}`,
			protected: []string{"messages.0.content.0.title"},
			kept:      []string{"messages.0.content.0.source.data"},
		},
		{
			name:      "prompt variables",
			body:      `{"prompt":{"id":"pmpt_1","variables":{"email":"alice@example.invalid","note":{"type":"input_text","text":"alice@example.invalid"},"image":{"type":"input_image","image_url":"https://alice@example.invalid/a.png"}}}}`,
			protected: []string{"prompt.variables.email", "prompt.variables.note.text"},
			kept:      []string{"prompt.variables.image.image_url"},
		},
		{
			name:      "chat reasoning",
			body:      `{"messages":[{"role":"assistant","content":"ok","reasoning_content":"alice@example.invalid","reasoning":"alice@example.invalid"}]}`,
			protected: []string{"messages.0.reasoning_content", "messages.0.reasoning"},
		},
	}
	for _, mode := range []string{ModeEncrypt, ModeReplace} {
		compiled, err := Compile([]Rule{{Pattern: `[a-z]+@example\.invalid`, Replacement: "[EMAIL]", Mode: mode}})
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range cases {
			got, err := compiled.ApplyWithCipher([]byte(tc.body), cipher)
			if err != nil || !json.Valid(got) {
				t.Fatalf("%s/%s: %v", mode, tc.name, err)
			}
			for _, path := range tc.protected {
				value := gjson.GetBytes(got, path).Str
				original := gjson.Get(tc.body, path).Str
				restored, restoreErr := cipher.RestoreText(value)
				if strings.Contains(value, secret) || (mode == ModeEncrypt && (restoreErr != nil || restored != original)) ||
					(mode == ModeReplace && !strings.Contains(value, "[EMAIL]")) {
					t.Errorf("%s/%s: %s = %q was not protected", mode, tc.name, path, value)
				}
			}
			for _, path := range tc.kept {
				if gjson.GetBytes(got, path).Str != gjson.Get(tc.body, path).Str {
					t.Errorf("%s/%s: %s changed", mode, tc.name, path)
				}
			}
		}
	}
}

func TestCompiledReversible(t *testing.T) {
	for _, tc := range []struct {
		rules []Rule
		want  bool
	}{
		{nil, false},
		{[]Rule{{Pattern: "a", Replacement: "b"}}, false},
		{[]Rule{{Pattern: "a", Replacement: "b"}, {Pattern: "c", Mode: ModeEncrypt}}, true},
	} {
		compiled, err := Compile(tc.rules)
		if err != nil {
			t.Fatal(err)
		}
		if got := compiled.Reversible(); got != tc.want {
			t.Errorf("Reversible(%v) = %v, want %v", tc.rules, got, tc.want)
		}
	}
	var empty *Compiled
	if empty.Reversible() {
		t.Error("nil configuration reported reversible rules")
	}
}

func TestApplyWithCipherKeepsUnrecordedSignedGeminiParts(t *testing.T) {
	cipher := syntheticRedactionCipher(t)
	compiled, err := Compile([]Rule{{Pattern: `alice@example\.invalid`, Mode: ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"thoughtSignature", "thought_signature"} {
		body := `{"contents":[{"role":"model","parts":[{"text":"alice@example.invalid","` + field + `":"signed"},{"functionCall":{"name":"send","args":{"to":"alice@example.invalid"}},"` + field + `":"signed"}]}]}`
		if got, err := compiled.ApplyWithCipher([]byte(body), cipher); err != nil || string(got) != body {
			t.Fatalf("%s parts = %s / %v", field, got, err)
		}
	}
}

func TestApplyWithCipherEncryptsModelAuthoredHistory(t *testing.T) {
	cipher := syntheticRedactionCipher(t)
	compiled, err := Compile([]Rule{{Pattern: `alice@example\.invalid`, Mode: ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	const secret = "alice@example.invalid"
	cases := []struct {
		name string
		body string
		kept []string
	}{
		{
			name: "responses native tools",
			body: `{"input":[{"type":"shell_call","call_id":"alice@example.invalid","status":"completed","action":{"commands":["mail alice@example.invalid"]}},` +
				`{"type":"local_shell_call","call_id":"c2","action":{"type":"exec","command":["mail","alice@example.invalid"],"env":{"TO":"alice@example.invalid"}}},` +
				`{"type":"apply_patch_call","call_id":"c3","operation":{"type":"update_file","path":"alice@example.invalid.txt","diff":"+alice@example.invalid"}},` +
				`{"type":"computer_call","call_id":"c4","action":{"type":"type","text":"alice@example.invalid"}},` +
				`{"type":"mcp_call","id":"m1","name":"alice@example.invalid","arguments":"{\"to\":\"alice@example.invalid\"}","output":"sent to alice@example.invalid"}]}`,
			kept: []string{"input.0.call_id", "input.4.name"},
		},
		{
			name: "chat assistant",
			body: `{"messages":[{"role":"assistant","content":"ok","reasoning_details":[{"type":"reasoning.text","text":"alice@example.invalid","signature":"alice@example.invalid"}],` +
				`"tool_calls":[{"id":"alice@example.invalid","type":"function","function":{"name":"alice@example.invalid","arguments":"{\"to\":\"alice@example.invalid\"}"}}]}]}`,
			kept: []string{"messages.0.reasoning_details.0.text", "messages.0.reasoning_details.0.signature", "messages.0.tool_calls.0.id", "messages.0.tool_calls.0.function.name"},
		},
		{
			name: "claude signed thinking",
			body: `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"alice@example.invalid","signature":"sig"},` +
				`{"type":"tool_use","id":"alice@example.invalid","name":"send","input":{"name":"alice@example.invalid"}}]}]}`,
			kept: []string{"messages.0.content.0.thinking", "messages.0.content.1.id"},
		},
		{
			name: "gemini model parts",
			body: `{"contents":[{"role":"model","parts":[{"executableCode":{"language":"PYTHON","code":"print('alice@example.invalid')"}},{"codeExecutionResult":{"outcome":"OUTCOME_OK","output":"alice@example.invalid"}}]}]}`,
		},
	}
	for _, tc := range cases {
		got, err := compiled.ApplyWithCipher([]byte(tc.body), cipher)
		if err != nil || !json.Valid(got) {
			t.Fatalf("%s: %s / %v", tc.name, got, err)
		}
		if remaining := strings.Count(string(got), secret); remaining != len(tc.kept) {
			t.Errorf("%s: %d plaintext values remain, want %d: %s", tc.name, remaining, len(tc.kept), got)
		}
		for _, path := range tc.kept {
			if gjson.GetBytes(got, path).Str != gjson.Get(tc.body, path).Str {
				t.Errorf("%s: protocol field %s changed", tc.name, path)
			}
		}
		// 加密只替换敏感值本身，还原后逐字节回到原文。
		if restored, err := cipher.RestoreText(string(got)); err != nil || restored != tc.body {
			t.Errorf("%s: restored history differs:\n got: %s\nwant: %s", tc.name, restored, tc.body)
		}
	}
}

func TestApplyReplacesModelAuthoredHistory(t *testing.T) {
	compiled, err := Compile([]Rule{{Pattern: `alice@example\.invalid`, Replacement: "[EMAIL]"}})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"input":[{"type":"shell_call","call_id":"alice@example.invalid","action":{"commands":["mail alice@example.invalid"]}},` +
		`{"type":"apply_patch_call","call_id":"c3","operation":{"type":"update_file","path":"a.txt","diff":"+alice@example.invalid"}},` +
		`{"type":"function_call","call_id":"c4","name":"send","arguments":"{\"to\":\"alice@example.invalid\"}"}]}`
	got, err := compiled.Apply([]byte(body))
	if err != nil || !json.Valid(got) {
		t.Fatalf("Apply() = %s / %v", got, err)
	}
	if strings.Count(string(got), "alice@example.invalid") != 1 || gjson.GetBytes(got, "input.0.call_id").Str != "alice@example.invalid" {
		t.Fatalf("protocol field changed or history was not replaced: %s", got)
	}
	for _, path := range []string{"input.0.action.commands.0", "input.1.operation.diff"} {
		if !strings.Contains(gjson.GetBytes(got, path).Str, "[EMAIL]") {
			t.Errorf("%s was not replaced: %s", path, got)
		}
	}
	if gjson.Get(gjson.GetBytes(got, "input.2.arguments").Str, "to").Str != "[EMAIL]" {
		t.Errorf("function arguments were not replaced: %s", got)
	}
}
