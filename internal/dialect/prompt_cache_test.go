package dialect

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestPromptCacheKeyValidationAndProtocolScope(t *testing.T) {
	cases := []struct{ name, value, want string }{
		{"valid", `"cache-a"`, "cache-a"}, {"unicode", `"缓存分组"`, "缓存分组"},
		{"empty", `""`, ""}, {"null", `null`, ""}, {"number", `42`, ""}, {"object", `{}`, ""},
		{"leading space", `" cache-a"`, ""}, {"trailing space", `"cache-a "`, ""}, {"control", `"cache\u0000a"`, ""},
		{"limit", `"` + strings.Repeat("a", 256) + `"`, strings.Repeat("a", 256)}, {"too long", `"` + strings.Repeat("a", 257) + `"`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"test","prompt_cache_key":` + tc.value + `,"messages":[{"role":"user","content":"hello"}],"input":"hello"}`)
			for _, d := range []Dialect{NewOpenAI(), NewOpenAIResponses()} {
				path := "/v1/chat/completions"
				if _, ok := d.(*OpenAIResponses); ok {
					path = "/v1/responses"
				}
				metadata, err := d.InspectRequest(&ParsedRequest{Method: http.MethodPost, Path: path, Body: body})
				if err != nil || metadata.PromptCacheKey != tc.want {
					t.Fatalf("key = %q, error = %v", metadata.PromptCacheKey, err)
				}
				encoded, _ := json.Marshal(metadata)
				if strings.Contains(string(encoded), "PromptCacheKey") {
					t.Fatal("private key serialized")
				}
			}
		})
	}
	for _, body := range []string{
		`{"prompt_cache_key":"a","prompt_cache_key":"b"}`, `{"metadata":{"prompt_cache_key":"a"}}`, `{"Prompt_Cache_Key":"a"}`,
	} {
		if inspectPromptCacheKey([]byte(body)) != "" {
			t.Fatal("ambiguous or nested key accepted")
		}
	}
	for _, path := range []string{"/v1/responses/compact", "/v1/responses/resp-test"} {
		metadata, err := NewOpenAIResponses().InspectRequest(&ParsedRequest{Method: http.MethodPost, Path: path, Body: []byte(`{"model":"test","input":"hello","prompt_cache_key":"a"}`)})
		if err != nil || metadata.PromptCacheKey != "" {
			t.Fatalf("resource request parsed affinity key: %v", err)
		}
	}
}
