package embedded

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
)

func TestCPAClientVersionsMeetSDKBaselines(t *testing.T) {
	for _, test := range []struct {
		provider string
		version  string
		minimum  string
	}{
		{provider: ProviderClaude, version: ClaudeClientVersion, minimum: helps.DefaultClaudeVersion(nil)},
		{provider: ProviderGrok, version: GrokClientVersion, minimum: helps.XAIClientVersionServerFloor},
	} {
		t.Run(test.provider, func(t *testing.T) {
			version, minimum := clientVersionParts(t, test.version), clientVersionParts(t, test.minimum)
			if slices.Compare(version[:], minimum[:]) < 0 {
				t.Fatalf("client version %s is below CPA's %s baseline %s", test.version, test.provider, test.minimum)
			}
			if test.provider == ProviderClaude && (version[0] != minimum[0] || version[1] != minimum[1]) {
				t.Fatalf("Claude %s needs protocol validation for CPA's %s compatibility line", test.version, test.minimum)
			}
		})
	}
}

func clientVersionParts(t *testing.T, version string) [3]int {
	t.Helper()
	var parts [3]int
	if n, err := fmt.Sscanf(version, "%d.%d.%d", &parts[0], &parts[1], &parts[2]); err != nil || n != len(parts) {
		t.Fatalf("invalid client version %q: %v", version, err)
	}
	return parts
}

func TestClaudeClientVersionAcrossExecutionPaths(t *testing.T) {
	const version = ClaudeClientVersion
	for _, operation := range []string{"unary", "stream", "count_tokens"} {
		t.Run(operation, func(t *testing.T) {
			calls := 0
			transport := claudeRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				body, err := io.ReadAll(request.Body)
				if err != nil {
					return nil, err
				}
				if got := request.Header.Get("User-Agent"); got != "claude-cli/"+version+" (external, cli)" {
					t.Errorf("client identity = %q, want version %s", got, version)
				}
				if operation != "count_tokens" && !bytes.Contains(body, []byte("cc_version="+version+".")) {
					t.Errorf("billing identity does not match the client version: %s", body)
				}
				payload := `{"id":"msg-one","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`
				contentType := "application/json"
				if operation == "stream" {
					payload, contentType = claudeExecutionSSE, "text/event-stream"
				} else if operation == "count_tokens" {
					payload = `{"input_tokens":7}`
				}
				return &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}},
					Body: io.NopCloser(strings.NewReader(payload)), Request: request,
				}, nil
			})
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
			executor := NewClaudeHTTPExecutor()
			request := ExecuteRequest{
				Model: "claude-sonnet-4-5", Format: "claude",
				Payload: []byte(`{"model":"claude-sonnet-4-5","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
				Headers: http.Header{"User-Agent": {"unrelated-client/9.9"}},
			}
			var err error
			switch operation {
			case "unary":
				_, err = executor.ExecuteCanonical(ctx, "version-unary", testClaudeExecutionCredential(), request)
			case "count_tokens":
				_, err = executor.CountTokensCanonical(ctx, "version-count", testClaudeExecutionCredential(), request)
			case "stream":
				var stream *ExecuteStreamResponse
				stream, err = executor.ExecuteStreamCanonical(ctx, "version-stream", testClaudeExecutionCredential(), request)
				if err == nil {
					for chunk := range stream.Chunks {
						if chunk.Err != nil {
							t.Fatal(chunk.Err)
						}
					}
				}
			}
			if err != nil || calls != 1 {
				t.Fatalf("execution error/calls = %v/%d", err, calls)
			}
			if request.Headers.Get("User-Agent") != "unrelated-client/9.9" {
				t.Fatal("execution changed caller-owned headers")
			}
		})
	}
}
