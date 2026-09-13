package channel

import (
	"encoding/json"
	"testing"
)

func TestOpenAICompatibleReasoningAliasParamsNormalizeBothDirections(t *testing.T) {
	registry := NewRegistry()
	cases := []struct {
		name   string
		raw    string
		key    string
		want   string
		wantOK bool
	}{
		{
			name:   "request canonical rename direction",
			raw:    `{"base_url":"https://example.com/v1","request_reasoning_alias":"reasoning_content_to_reasoning"}`,
			key:    "request_reasoning_alias",
			want:   "reasoning_content_to_reasoning",
			wantOK: true,
		},
		{
			name:   "request empty stays omitted",
			raw:    `{"base_url":"https://example.com/v1","request_reasoning_alias":"  "}`,
			key:    "request_reasoning_alias",
			want:   "",
			wantOK: false,
		},
		{
			name:   "response duplicate accepted",
			raw:    `{"base_url":"https://example.com/v1","reasoning_content_alias":"duplicate"}`,
			key:    "reasoning_content_alias",
			want:   "duplicate",
			wantOK: true,
		},
		{
			name:   "response off kept explicit",
			raw:    `{"base_url":"https://example.com/v1","reasoning_content_alias":"off"}`,
			key:    "reasoning_content_alias",
			want:   "off",
			wantOK: true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			params, err := registry.ValidateParams(OpenAICompatible, json.RawMessage(test.raw))
			if err != nil {
				t.Fatalf("ValidateParams error = %v", err)
			}
			got, ok := params.Value(test.key)
			if ok != test.wantOK || got != test.want {
				t.Fatalf("Value(%s) = %q, %t; want %q, %t", test.key, got, ok, test.want, test.wantOK)
			}
		})
	}
}

func TestOpenAICompatibleReasoningAliasParamsRejectJunk(t *testing.T) {
	registry := NewRegistry()
	for _, raw := range []string{
		`{"base_url":"https://example.com/v1","reasoning_content_alias":"maybe"}`,
		`{"base_url":"https://example.com/v1","reasoning_content_alias":"true"}`,
		`{"base_url":"https://example.com/v1","reasoning_content_alias":"reasoning_to_content"}`,
		`{"base_url":"https://example.com/v1","request_reasoning_alias":"content_to_reasoning"}`,
		`{"base_url":"https://example.com/v1","request_reasoning_alias":"duplicate"}`,
		`{"base_url":"https://example.com/v1","request_reasoning_alias":"true"}`,
		`{"base_url":"https://example.com/v1","request_reasoning_alias":"false"}`,
		`{"base_url":"https://example.com/v1","request_reasoning_alias":true}`,
		`{"base_url":"https://example.com/v1","reasoning_content_alias":"reasoning_content_to_reasoningx"}`,
		`{"base_url":"https://example.com/v1","reasoning_content_alias":true}`,
		`{"base_url":"https://example.com/v1","request_reasoning_alias":{"mode":"off"}}`,
	} {
		if _, err := registry.ValidateParams(OpenAICompatible, json.RawMessage(raw)); err == nil {
			t.Fatalf("ValidateParams(%s) error = nil", raw)
		}
	}
}
