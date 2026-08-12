package dialect

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDialectsExtractStablePromptAffinityPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dialect Dialect
		path    string
		base    string
		variant string
	}{
		{
			name: "OpenAI chat", dialect: NewOpenAI(), path: "/v1/chat/completions",
			base: `{
				"model":"gpt-4o","temperature":0.1,
				"messages":[
					{"role":"system","content":"Be helpful"},
					{"role":"user","content":"Hello"},
					{"role":"assistant","content":"old answer"}
				]
			}`,
			variant: `{
				"messages":[
					{"content":[{"text":"Be helpful","type":"text"}],"role":"system"},
					{"content":[{"type":"input_text","text":"Hello"}],"role":"user"},
					{"role":"assistant","content":"different answer"},
					{"role":"user","content":"later turn"}
				],"stream":true,"model":"gpt-4o","temperature":1
			}`,
		},
		{
			name: "OpenAI Responses", dialect: NewOpenAIResponses(), path: "/v1/responses",
			base: `{"model":"gpt-4.1","instructions":"Be helpful","input":"Hello","store":false}`,
			variant: `{
				"store":false,"model":"gpt-4.1","instructions":[{"type":"input_text","text":"Be helpful"}],
				"input":[
					{"role":"user","content":[{"text":"Hello","type":"input_text"}]},
					{"role":"assistant","content":[{"type":"output_text","text":"different answer"}]}
				]
			}`,
		},
		{
			name: "Anthropic", dialect: NewAnthropic(), path: "/v1/messages",
			base: `{
				"model":"claude-sonnet-4-5","max_tokens":100,"system":"Be helpful",
				"messages":[{"role":"user","content":"Hello"},{"role":"assistant","content":"old answer"}]
			}`,
			variant: `{
				"model":"claude-sonnet-4-5","max_tokens":200,
				"system":[{"text":"Be helpful","type":"text"}],
				"messages":[
					{"role":"user","content":[{"type":"text","text":"Hello"}]},
					{"role":"assistant","content":"different answer"},
					{"role":"user","content":"later turn"}
				]
			}`,
		},
		{
			name: "Gemini", dialect: NewGemini(), path: "/v1beta/models/gemini-2.5-pro:generateContent",
			base: `{
				"systemInstruction":{"parts":[{"text":"Be helpful"}]},
				"contents":[{"role":"user","parts":[{"text":"Hello"}]},{"role":"model","parts":[{"text":"old answer"}]}]
			}`,
			variant: `{
				"system_instruction":{"parts":[{"text":"Be helpful"}]},
				"contents":[
					{"parts":[{"text":"Hello"}],"role":"user"},
					{"role":"model","parts":[{"text":"different answer"}]},
					{"role":"user","parts":[{"text":"later turn"}]}
				],"generationConfig":{"temperature":1}
			}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base, err := test.dialect.InspectRequest(&ParsedRequest{
				Method: http.MethodPost, Path: test.path, Body: []byte(test.base),
			})
			if err != nil {
				t.Fatalf("base InspectRequest() error = %v", err)
			}
			variant, err := test.dialect.InspectRequest(&ParsedRequest{
				Method: http.MethodPost, Path: test.path, Body: []byte(test.variant),
			})
			if err != nil {
				t.Fatalf("variant InspectRequest() error = %v", err)
			}
			if len(base.AffinityPrefix) == 0 {
				t.Fatal("base AffinityPrefix is empty")
			}
			if !bytes.Equal(base.AffinityPrefix, variant.AffinityPrefix) {
				t.Fatalf("AffinityPrefix differs for stable semantic prefix:\nbase=%q\nvariant=%q", base.AffinityPrefix, variant.AffinityPrefix)
			}
		})
	}
}

func TestPromptAffinityPrefixChangesWithInitialInstructionOrUserText(t *testing.T) {
	t.Parallel()

	base := inspectPromptAffinityPrefix(
		NewOpenAI().Protocol(),
		[]byte(`{"messages":[{"role":"system","content":"one"},{"role":"user","content":"hello"}]}`),
	)
	changedSystem := inspectPromptAffinityPrefix(
		NewOpenAI().Protocol(),
		[]byte(`{"messages":[{"role":"system","content":"two"},{"role":"user","content":"hello"}]}`),
	)
	changedUser := inspectPromptAffinityPrefix(
		NewOpenAI().Protocol(),
		[]byte(`{"messages":[{"role":"system","content":"one"},{"role":"user","content":"different"}]}`),
	)
	if len(base) == 0 || bytes.Equal(base, changedSystem) || bytes.Equal(base, changedUser) {
		t.Fatalf("prefixes = %q / %q / %q, want non-empty distinct values", base, changedSystem, changedUser)
	}
}

func TestPromptAffinityPrefixRequiresInitialUserText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dialect Dialect
		path    string
		body    string
	}{
		{name: "OpenAI system only", dialect: NewOpenAI(), path: "/v1/chat/completions", body: `{"model":"gpt-4o","messages":[{"role":"system","content":"shared"}]}`},
		{name: "Responses resource reference only", dialect: NewOpenAIResponses(), path: "/v1/responses", body: `{"model":"gpt-4.1","previous_response_id":"resp_123","store":false}`},
		{name: "Anthropic assistant only", dialect: NewAnthropic(), path: "/v1/messages", body: `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"assistant","content":"hello"}]}`},
		{name: "Gemini image only", dialect: NewGemini(), path: "/v1beta/models/gemini-2.5-pro:generateContent", body: `{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"AA=="}}]}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata, err := test.dialect.InspectRequest(&ParsedRequest{
				Method: http.MethodPost, Path: test.path, Body: []byte(test.body),
			})
			if err != nil {
				t.Fatalf("InspectRequest() error = %v", err)
			}
			if len(metadata.AffinityPrefix) != 0 {
				t.Fatalf("AffinityPrefix = %q, want empty", metadata.AffinityPrefix)
			}
		})
	}
}

func TestPromptAffinityPrefixBoundsEachRoleWithoutBreakingUTF8(t *testing.T) {
	t.Parallel()

	longText := strings.Repeat("界", maxPromptAffinityRoleBytes)
	body, err := json.Marshal(map[string]any{
		"messages": []map[string]string{
			{"role": "system", "content": longText},
			{"role": "user", "content": longText},
		},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	prefix := inspectPromptAffinityPrefix(NewOpenAI().Protocol(), body)
	var decoded canonicalPromptAffinityPrefix
	if err := json.Unmarshal(prefix, &decoded); err != nil {
		t.Fatalf("decode affinity prefix: %v", err)
	}
	for name, parts := range map[string][]string{"system": decoded.System, "user": decoded.User} {
		total := 0
		for _, part := range parts {
			if !utf8.ValidString(part) {
				t.Fatalf("%s part is not valid UTF-8", name)
			}
			total += len(part)
		}
		if total == 0 || total > maxPromptAffinityRoleBytes {
			t.Fatalf("%s bytes = %d, want 1..%d", name, total, maxPromptAffinityRoleBytes)
		}
	}
}
