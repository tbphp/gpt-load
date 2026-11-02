package muse

import (
	"context"
	"errors"
	"net/http"
	"testing"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/embedded"
)

type failedStreamBridge struct {
	cpaembedded.MuseHTTPExecutor
	response *cpaembedded.ExecuteStreamResponse
	err      error
}

func (bridge failedStreamBridge) ExecuteStreamCanonical(context.Context, string, cpaembedded.MuseCredential, cpaembedded.ExecuteRequest) (*cpaembedded.ExecuteStreamResponse, error) {
	return bridge.response, bridge.err
}

func TestExecutorReturnsStreamSetupErrorWithoutForwarding(t *testing.T) {
	wantErr := errors.New("upstream rejected stream")
	budget := int64(1024)
	bridge := failedStreamBridge{response: &cpaembedded.ExecuteStreamResponse{
		Headers:                      http.Header{"X-Request-Id": {"request-one"}},
		AppliedReasoningEffort:       "high",
		AppliedReasoningMode:         "budget",
		AppliedReasoningBudgetTokens: &budget,
	}, err: wantErr}
	value := &executor{bridge: bridge}
	response, err := value.ExecuteStream(t.Context(), "account", Credential{}, ExecuteRequest{})
	if !errors.Is(err, wantErr) || response == nil {
		t.Fatalf("setup failure lost: response=%v error=%v", response, err)
	}
	if response.Chunks != nil {
		t.Fatal("stream setup failure started a forwarding channel")
	}
	if response.Headers.Get("X-Request-Id") != "request-one" || response.AppliedReasoningEffort != "high" || response.AppliedReasoningMode != "budget" || response.AppliedReasoningBudgetTokens == nil || *response.AppliedReasoningBudgetTokens != budget {
		t.Fatal("stream setup failure lost response metadata")
	}
}
