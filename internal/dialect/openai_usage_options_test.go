package dialect

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestOpenAIInjectStreamUsage(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "absent",
			body: `{"model":"gpt-4o","stream":true,"future":{"keep":1}}`,
			want: `{"model":"gpt-4o","stream":true,"future":{"keep":1},"stream_options":{"include_usage":true}}`,
		},
		{
			name: "null",
			body: `{"model":"gpt-4o","stream_options":null}`,
			want: `{"model":"gpt-4o","stream_options":{"include_usage":true}}`,
		},
		{
			name: "preserves unknown option",
			body: `{"model":"gpt-4o","stream_options":{"future":"keep","include_usage":false}}`,
			want: `{"model":"gpt-4o","stream_options":{"future":"keep","include_usage":true}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := &ParsedRequest{
				Method: http.MethodPost,
				Path:   "/v1/chat/completions",
				Header: http.Header{"X-Original": {"value"}},
				Body:   []byte(test.body),
			}
			original := cloneParsedRequestForTest(request)

			derived, err := NewOpenAI().InjectStreamUsage(request)
			if err != nil {
				t.Fatalf("InjectStreamUsage() error = %v", err)
			}
			if derived == request {
				t.Fatal("InjectStreamUsage() returned the caller-owned request")
			}
			if !reflect.DeepEqual(decodeUsageOptionsJSON(t, derived.Body), decodeUsageOptionsJSON(t, []byte(test.want))) {
				t.Fatalf("derived body = %s, want semantic JSON %s", derived.Body, test.want)
			}
			if !reflect.DeepEqual(request, original) {
				t.Fatalf("original request changed:\n got %#v\nwant %#v", request, original)
			}

			derived.Header.Set("X-Derived", "changed")
			derived.Body[0] ^= 0xff
			if !reflect.DeepEqual(request, original) {
				t.Fatalf("derived request aliases original:\n got %#v\nwant %#v", request, original)
			}
		})
	}
}

func TestOpenAIInjectStreamUsageRejectsInvalidStreamOptions(t *testing.T) {
	for _, body := range []string{
		`{"stream_options":"invalid"}`,
		`{"stream_options":1}`,
		`{"stream_options":false}`,
		`{"stream_options":[]}`,
	} {
		request := &ParsedRequest{Body: []byte(body)}
		if _, err := NewOpenAI().InjectStreamUsage(request); err == nil {
			t.Fatalf("InjectStreamUsage(%s) error = nil", body)
		}
	}
}

func TestOpenAIInjectStreamUsageIsSemanticallyIdempotent(t *testing.T) {
	request := &ParsedRequest{Body: []byte(`{"model":"gpt-4o","stream_options":{"future":"keep"}}`)}
	first, err := NewOpenAI().InjectStreamUsage(request)
	if err != nil {
		t.Fatalf("first InjectStreamUsage() error = %v", err)
	}
	second, err := NewOpenAI().InjectStreamUsage(first)
	if err != nil {
		t.Fatalf("second InjectStreamUsage() error = %v", err)
	}
	if !reflect.DeepEqual(decodeUsageOptionsJSON(t, first.Body), decodeUsageOptionsJSON(t, second.Body)) {
		t.Fatalf("injection is not semantically idempotent:\n first %s\nsecond %s", first.Body, second.Body)
	}
}

func TestOnlyOpenAIImplementsStreamUsageInjector(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
		want  bool
	}{
		{name: "OpenAI", value: NewOpenAI(), want: true},
		{name: "OpenAI Responses", value: NewOpenAIResponses(), want: false},
		{name: "Anthropic", value: NewAnthropic(), want: false},
		{name: "Gemini", value: NewGemini(), want: false},
		{name: "dialect only", value: &usageDialectOnly{}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, got := test.value.(StreamUsageInjector)
			if got != test.want {
				t.Fatalf("StreamUsageInjector = %t, want %t", got, test.want)
			}
		})
	}
}

func decodeUsageOptionsJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode JSON %q: %v", body, err)
	}
	return value
}
