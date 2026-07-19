package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestVisibleModelIDs(t *testing.T) {
	snapshot, err := state.Compile(state.CompileInput{
		Groups: []state.GroupConfig{
			{
				ID: 1, Name: "first", UpstreamURL: "https://first.example.com",
				Protocols: []protocol.Protocol{protocol.OpenAI, protocol.Anthropic, protocol.Gemini},
				Models: []state.ModelConfig{
					{ID: "zeta"}, {ID: "shared", Alias: "first-alias"}, {ID: "alpha"},
				},
				Enabled: true,
			},
			{
				ID: 2, Name: "second", UpstreamURL: "https://second.example.com",
				Protocols: []protocol.Protocol{protocol.OpenAI, protocol.Anthropic},
				Models: []state.ModelConfig{
					{ID: "shared", Alias: "second-alias"}, {ID: "beta"},
				},
				Enabled: true,
			},
			{
				ID: 3, Name: "disabled", UpstreamURL: "https://disabled.example.com",
				Protocols: []protocol.Protocol{protocol.Gemini},
				Models:    []state.ModelConfig{{ID: "hidden"}}, Enabled: false,
			},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	tests := []struct {
		name      string
		snapshot  *state.ConfigSnapshot
		accessKey state.AccessKeyView
		value     protocol.Protocol
		want      []string
	}{
		{name: "no filters sorted and deduplicated", snapshot: snapshot, value: protocol.OpenAI, want: []string{"alpha", "beta", "shared", "zeta"}},
		{name: "protocol allowed", snapshot: snapshot, accessKey: state.AccessKeyView{Filters: state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.OpenAI: {}}}}, value: protocol.OpenAI, want: []string{"alpha", "beta", "shared", "zeta"}},
		{name: "protocol denied", snapshot: snapshot, accessKey: state.AccessKeyView{Filters: state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.Gemini: {}}}}, value: protocol.OpenAI, want: []string{}},
		{name: "model filter", snapshot: snapshot, accessKey: state.AccessKeyView{Filters: state.FilterSet{Models: map[string]struct{}{"shared": {}, "zeta": {}, "missing": {}}}}, value: protocol.OpenAI, want: []string{"shared", "zeta"}},
		{name: "group filter keeps any matching target", snapshot: snapshot, accessKey: state.AccessKeyView{Filters: state.FilterSet{Groups: map[uint]struct{}{2: {}}}}, value: protocol.OpenAI, want: []string{"beta", "shared"}},
		{name: "joint filters", snapshot: snapshot, accessKey: state.AccessKeyView{Filters: state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.Anthropic: {}}, Models: map[string]struct{}{"beta": {}, "shared": {}}, Groups: map[uint]struct{}{2: {}}}}, value: protocol.Anthropic, want: []string{"beta", "shared"}},
		{name: "dangling group filter", snapshot: snapshot, accessKey: state.AccessKeyView{Filters: state.FilterSet{Groups: map[uint]struct{}{99: {}}}}, value: protocol.OpenAI, want: []string{}},
		{name: "disabled group model absent", snapshot: snapshot, value: protocol.Gemini, want: []string{"alpha", "shared", "zeta"}},
		{name: "missing protocol", snapshot: snapshot, value: protocol.OpenAIResponse, want: []string{}},
		{name: "nil snapshot", snapshot: nil, value: protocol.OpenAI, want: []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := visibleModelIDs(test.snapshot, test.accessKey, test.value)
			if got == nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("visibleModelIDs() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestWriteModelList(t *testing.T) {
	tests := []struct {
		name     string
		value    protocol.Protocol
		ids      []string
		expected string
	}{
		{
			name: "OpenAI", value: protocol.OpenAI, ids: []string{"alpha", "zeta"},
			expected: `{"object":"list","data":[{"id":"alpha","object":"model","created":1735689600,"owned_by":"gpt-load"},{"id":"zeta","object":"model","created":1735689600,"owned_by":"gpt-load"}]}`,
		},
		{
			name: "Anthropic", value: protocol.Anthropic, ids: []string{"alpha", "zeta"},
			expected: `{"data":[{"type":"model","id":"alpha","display_name":"alpha","created_at":"2025-01-01T00:00:00Z"},{"type":"model","id":"zeta","display_name":"zeta","created_at":"2025-01-01T00:00:00Z"}],"has_more":false,"first_id":"alpha","last_id":"zeta"}`,
		},
		{
			name: "Gemini", value: protocol.Gemini, ids: []string{"models/custom"},
			expected: `{"models":[{"name":"models/models/custom"}]}`,
		},
		{
			name: "OpenAI empty", value: protocol.OpenAI, ids: nil,
			expected: `{"object":"list","data":[]}`,
		},
		{
			name: "Anthropic empty", value: protocol.Anthropic, ids: nil,
			expected: `{"data":[],"has_more":false,"first_id":"","last_id":""}`,
		},
		{
			name: "Gemini empty", value: protocol.Gemini, ids: nil,
			expected: `{"models":[]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			writeModelList(context, test.value, test.ids)
			if recorder.Code != http.StatusOK || !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("response = %d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
			}
			assertJSONEqual(t, recorder.Body.String(), test.expected)
			if strings.Contains(recorder.Body.String(), `"code"`) || strings.Contains(recorder.Body.String(), `"message"`) ||
				strings.Contains(recorder.Body.String(), "nextPageToken") {
				t.Fatalf("model response contains forbidden envelope fields: %s", recorder.Body.String())
			}
			for _, forbidden := range []string{"baseModelId", "version", "inputTokenLimit", "outputTokenLimit", "supportedGenerationMethods"} {
				if strings.Contains(recorder.Body.String(), forbidden) {
					t.Fatalf("model response contains forbidden official metadata field %q: %s", forbidden, recorder.Body.String())
				}
			}
		})
	}
}

func assertJSONEqual(t *testing.T, got, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("decode got JSON: %v; body=%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode expected JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON mismatch\ngot:  %s\nwant: %s", got, want)
	}
}
