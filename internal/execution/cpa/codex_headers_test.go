package cpa

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/dialect"
	"gpt-load/internal/gateway"
	"gpt-load/internal/state"
)

func TestCodexGatewayRequestIdentity(t *testing.T) {
	const defaultUA = "codex-tui/0.153.3 (Mac OS 26.5.1; arm64) iTerm.app/3.6.11 (codex-tui; 0.153.3)"
	const configuredUA = "codex-tui/0.200.0 (Mac OS 26.5.0; arm64)"
	tests := []struct {
		name           string
		model          string
		headers        http.Header
		rules          state.HeaderRules
		promptCacheKey string
		wantUA         string
		wantOriginator string
		wantVersion    string
		wantSession    string
	}{
		{
			name:    "client identity cannot change fixed version",
			headers: http.Header{"User-Agent": {"browser/1.0"}, "Originator": {"browser"}, "Version": {"9.9.9"}},
			wantUA:  defaultUA, wantOriginator: "codex-tui", wantVersion: "0.153.3",
		},
		{
			name:    "configured UA cannot override CPA identity",
			headers: http.Header{"User-Agent": {"browser/1.0"}, "Version": {"9.9.9"}},
			rules:   state.HeaderRules{Set: map[string]string{"user-agent": configuredUA, "originator": "configured-client"}},
			wantUA:  defaultUA, wantOriginator: "configured-client", wantVersion: "0.153.3",
		},
		{
			name: "preserve model default and align version", model: "gpt-5.6-luna",
			headers: http.Header{"Version": {"9.9.9"}},
			wantUA:  defaultUA, wantOriginator: "codex-tui", wantVersion: "0.153.3",
		},
		{
			name: "configured UA cannot override model default", model: "gpt-5.6-luna",
			rules:  state.HeaderRules{Set: map[string]string{"User-Agent": configuredUA}},
			wantUA: defaultUA, wantOriginator: "codex-tui", wantVersion: "0.153.3",
		},
		{
			name:    "non Codex configured UA is ignored",
			headers: http.Header{"Version": {"9.9.9"}},
			rules:   state.HeaderRules{Set: map[string]string{"User-Agent": "custom-client/1.0"}},
			wantUA:  defaultUA, wantOriginator: "codex-tui", wantVersion: "0.153.3",
		},
		{
			name:   "configured version cannot override fixed version",
			rules:  state.HeaderRules{Set: map[string]string{"User-Agent": "custom-client/1.0", "Version": "custom-version"}},
			wantUA: defaultUA, wantOriginator: "codex-tui", wantVersion: "0.153.3",
		},
		{
			name:    "removal cannot remove fixed identity",
			headers: http.Header{"User-Agent": {"browser/1.0"}, "Originator": {"browser"}, "Version": {"9.9.9"}},
			rules:   state.HeaderRules{Remove: []string{"User-Agent", "Originator", "Version"}},
			wantUA:  defaultUA, wantVersion: "0.153.3",
		},
		{
			name:   "empty configured identity is ignored",
			rules:  state.HeaderRules{Set: map[string]string{"User-Agent": "", "Version": ""}},
			wantUA: defaultUA, wantOriginator: "codex-tui", wantVersion: "0.153.3",
		},
		{
			name:    "client UA version is ignored",
			headers: http.Header{"User-Agent": {"codex_cli_rs/0.999.0"}},
			wantUA:  defaultUA, wantOriginator: "codex-tui", wantVersion: "0.153.3",
		},
		{
			name: "underscore session is preserved", model: "gpt-5.6-luna",
			headers: http.Header{"Session_id": {"client-session"}},
			wantUA:  defaultUA, wantOriginator: "codex-tui", wantVersion: "0.153.3", wantSession: "client-session",
		},
		{
			name:    "hyphen session wins over alias",
			headers: http.Header{"Session_id": {"alias-session"}, "Session-Id": {"canonical-session"}},
			wantUA:  defaultUA, wantOriginator: "codex-tui", wantVersion: "0.153.3", wantSession: "canonical-session",
		},
		{
			name:    "blank canonical session falls back to alias",
			headers: http.Header{"Session-Id": {"  "}, "Session_id": {" alias-session "}},
			wantUA:  defaultUA, wantOriginator: "codex-tui", wantVersion: "0.153.3", wantSession: "alias-session",
		},
		{
			name: "prompt cache remains the session fallback", promptCacheKey: "cache-session",
			wantUA: defaultUA, wantOriginator: "codex-tui", wantVersion: "0.153.3", wantSession: "cache-session",
		},
		{
			name: "session alias has the same precedence as canonical header", promptCacheKey: "cache-session",
			headers: http.Header{"Session_id": {"client-session"}},
			wantUA:  defaultUA, wantOriginator: "codex-tui", wantVersion: "0.153.3", wantSession: "client-session",
		},
	}
	for _, stream := range []bool{false, true} {
		mode := "unary"
		if stream {
			mode = "stream"
		}
		for _, test := range tests {
			t.Run(mode+"/"+test.name, func(t *testing.T) {
				adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
				spec := validSpec(t, row, keyService)
				model := test.model
				if model == "" {
					model = spec.UpstreamModel
				}
				body := `{"model":"` + model + `","input":"hello"}`
				if test.promptCacheKey != "" {
					body = `{"model":"` + model + `","input":"hello","prompt_cache_key":"` + test.promptCacheKey + `"}`
				}
				capturedHeaders := make(chan http.Header, 1)
				transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
					capturedHeaders <- request.Header.Clone()
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"text/event-stream"}},
						Body:       io.NopCloser(strings.NewReader(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"` + model + `","output":[]}}` + "\n\n")),
						Request:    request,
					}, nil
				})
				ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
				input := gateway.ForwardInput{
					Dialect: dialect.NewOpenAIResponses(), Group: state.GroupView{ID: row.GroupID, HeaderRules: test.rules},
					Request:   &dialect.ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Header: test.headers.Clone(), Body: []byte(body)},
					RequestID: spec.RequestID, AttemptID: spec.AttemptID, AttemptSequence: spec.Sequence,
					ClientProtocol: spec.ClientProtocol, Operation: spec.Operation, ChannelID: spec.ChannelID,
					RouteMode: spec.RouteMode, TargetConfig: spec.TargetConfig, Credential: spec.Credential,
					ExternalModel: model, UpstreamModelID: model,
				}
				forwarder := gateway.NewExecutionForwarder(adapter)
				var result gateway.UpstreamResult
				if stream {
					result = forwarder.ForwardStream(ctx, input, httptest.NewRecorder())
				} else {
					result = forwarder.Forward(ctx, input)
				}
				if result.Err != nil || result.StatusCode != http.StatusOK {
					t.Fatalf("forward error = %v, status = %d", result.Err, result.StatusCode)
				}
				captured := <-capturedHeaders
				for name, want := range map[string]string{"User-Agent": test.wantUA, "Originator": test.wantOriginator, "Version": test.wantVersion} {
					if got := captured.Get(name); got != want {
						t.Errorf("%s = %q, want %q", name, got, want)
					}
				}
				if test.wantSession != "" && captured.Get("Session-Id") != test.wantSession {
					t.Errorf("Session-Id = %q, want %q", captured.Get("Session-Id"), test.wantSession)
				}
				if captured.Get("Session_id") != "" {
					t.Error("upstream retained the underscore session alias")
				}
				if captured.Get("Authorization") != "Bearer access" || captured.Get("Chatgpt-Account-Id") != "account-1" {
					t.Error("upstream credential headers changed")
				}
				if !reflect.DeepEqual(input.Request.Header, test.headers) {
					t.Error("forwarding mutated the downstream headers")
				}
			})
		}
	}
}
