package dialect

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestModelRewritersDeriveRequestsWithoutMutatingOriginal(t *testing.T) {
	tests := []struct {
		name      string
		rewriter  ModelRewriter
		request   *ParsedRequest
		upstream  string
		wantPath  string
		wantModel string
	}{
		{
			name: "openai", rewriter: NewOpenAI(http.DefaultClient), upstream: "provider-openai",
			request:  &ParsedRequest{Method: http.MethodPost, Path: "/v1/chat/completions", Header: http.Header{"X-Test": {"one"}}, Body: []byte(`{"model":"public","metadata":{"n":9007199254740993}}`)},
			wantPath: "/v1/chat/completions", wantModel: "provider-openai",
		},
		{
			name: "anthropic", rewriter: NewAnthropic(http.DefaultClient), upstream: "provider-claude",
			request:  &ParsedRequest{Method: http.MethodPost, Path: "/v1/messages", Body: []byte(`{"model":"public","messages":[]}`)},
			wantPath: "/v1/messages", wantModel: "provider-claude",
		},
		{
			name: "gemini", rewriter: NewGemini(http.DefaultClient), upstream: "provider-gemini",
			request:  &ParsedRequest{Method: http.MethodPost, Path: "/v1beta/models/public:generateContent", Body: []byte(`{}`)},
			wantPath: "/v1beta/models/provider-gemini:generateContent",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := cloneParsedRequestForTest(test.request)
			derived, err := test.rewriter.RewriteRequestModel(test.request, test.upstream)
			if err != nil {
				t.Fatalf("RewriteRequestModel() error = %v", err)
			}
			if derived == test.request {
				t.Fatal("RewriteRequestModel() returned the caller-owned request")
			}
			if derived.Path != test.wantPath {
				t.Fatalf("derived path = %q, want %q", derived.Path, test.wantPath)
			}
			if test.wantModel != "" {
				model, _, err := test.rewriter.(Dialect).ExtractModel(derived)
				if err != nil {
					t.Fatalf("ExtractModel(derived) error = %v", err)
				}
				if model != test.wantModel {
					t.Fatalf("derived model = %q, want %q", model, test.wantModel)
				}
			}
			if !reflect.DeepEqual(test.request, original) {
				t.Fatalf("original request changed after rewrite:\n got %#v\nwant %#v", test.request, original)
			}

			if derived.Header == nil {
				derived.Header = make(http.Header)
			}
			derived.Header.Set("X-Derived", "changed")
			if len(derived.Body) > 0 {
				derived.Body[0] ^= 0xff
			}
			if !reflect.DeepEqual(test.request, original) {
				t.Fatalf("original request aliases derived request:\n got %#v\nwant %#v", test.request, original)
			}
		})
	}
}

func TestModelRewritersRewriteResponseFields(t *testing.T) {
	tests := []struct {
		name     string
		rewriter ModelRewriter
		body     string
		path     []string
	}{
		{name: "openai", rewriter: NewOpenAI(http.DefaultClient), body: `{"model":"real","n":9007199254740993}`, path: []string{"model"}},
		{name: "anthropic response", rewriter: NewAnthropic(http.DefaultClient), body: `{"type":"message","model":"real"}`, path: []string{"model"}},
		{name: "anthropic message_start", rewriter: NewAnthropic(http.DefaultClient), body: `{"type":"message_start","message":{"model":"real"}}`, path: []string{"message", "model"}},
		{name: "gemini", rewriter: NewGemini(http.DefaultClient), body: `{"modelVersion":"real"}`, path: []string{"modelVersion"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.rewriter.RewriteResponseModel([]byte(test.body), "public")
			if err != nil {
				t.Fatalf("RewriteResponseModel() error = %v", err)
			}
			payload := decodeJSONWithNumbers(t, got)
			var selected any = payload
			for _, part := range test.path {
				object, ok := selected.(map[string]any)
				if !ok {
					t.Fatalf("response path %v reached %T", test.path, selected)
				}
				selected = object[part]
			}
			if selected != "public" {
				t.Fatalf("response path %v = %#v, want public", test.path, selected)
			}
			if number, exists := payload["n"]; exists {
				gotNumber, ok := number.(json.Number)
				if !ok || gotNumber.String() != "9007199254740993" {
					t.Fatalf("untouched integer = %#v", number)
				}
			}
		})
	}
}

func TestModelRewritersRejectInvalidTargetModels(t *testing.T) {
	rewriters := []struct {
		name  string
		value ModelRewriter
		req   *ParsedRequest
	}{
		{name: "openai", value: NewOpenAI(http.DefaultClient), req: &ParsedRequest{Body: []byte(`{"model":"public"}`)}},
		{name: "anthropic", value: NewAnthropic(http.DefaultClient), req: &ParsedRequest{Body: []byte(`{"model":"public"}`)}},
		{name: "gemini", value: NewGemini(http.DefaultClient), req: &ParsedRequest{Path: "/v1beta/models/public:generateContent"}},
	}
	for _, test := range rewriters {
		t.Run(test.name, func(t *testing.T) {
			for _, model := range []string{"", " public", "public "} {
				if _, err := test.value.RewriteRequestModel(test.req, model); err == nil {
					t.Fatalf("RewriteRequestModel(%q) error = nil", model)
				}
				if _, err := test.value.RewriteResponseModel([]byte(`{"model":"real","modelVersion":"real"}`), model); err == nil {
					t.Fatalf("RewriteResponseModel(%q) error = nil", model)
				}
			}
		})
	}
	gemini := NewGemini(http.DefaultClient)
	if _, err := gemini.RewriteRequestModel(&ParsedRequest{Path: "/v1beta/models/public:generateContent"}, "models/provider"); err == nil {
		t.Fatal("Gemini RewriteRequestModel() accepted slash in model")
	}
	if _, err := gemini.RewriteResponseModel([]byte(`{"modelVersion":"real"}`), "models/provider"); err == nil {
		t.Fatal("Gemini RewriteResponseModel() accepted slash in model")
	}
}

func TestModelRewritersRequireRequestModelAndCloneAbsentResponse(t *testing.T) {
	for _, test := range []struct {
		name     string
		rewriter ModelRewriter
		request  *ParsedRequest
		response []byte
	}{
		{name: "openai", rewriter: NewOpenAI(http.DefaultClient), request: &ParsedRequest{Body: []byte(`{}`)}, response: []byte(`{"id":"one"}`)},
		{name: "anthropic", rewriter: NewAnthropic(http.DefaultClient), request: &ParsedRequest{Body: []byte(`{}`)}, response: []byte(`{"type":"content_block_delta"}`)},
		{name: "gemini", rewriter: NewGemini(http.DefaultClient), request: &ParsedRequest{Path: "/v1beta/models/:generateContent"}, response: []byte(`{"candidates":[]}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.rewriter.RewriteRequestModel(test.request, "provider"); err == nil {
				t.Fatal("RewriteRequestModel() error = nil for absent model")
			}
			got, err := test.rewriter.RewriteResponseModel(test.response, "public")
			if err != nil {
				t.Fatalf("RewriteResponseModel() error = %v", err)
			}
			if !bytes.Equal(got, test.response) {
				t.Fatalf("absent response rewrite = %q, want %q", got, test.response)
			}
			if len(got) > 0 {
				got[0] ^= 0xff
				if bytes.Equal(got, test.response) {
					t.Fatal("absent response rewrite aliases input bytes")
				}
			}
		})
	}
}

func cloneParsedRequestForTest(request *ParsedRequest) *ParsedRequest {
	clone := *request
	clone.Header = request.Header.Clone()
	clone.Body = bytes.Clone(request.Body)
	return &clone
}

func decodeJSONWithNumbers(t *testing.T, body []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return payload
}
