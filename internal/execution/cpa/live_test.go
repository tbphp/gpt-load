package cpa

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/subscription/providers/codex"
)

func TestCodexLiveSelectedAttemptUsesNativeCPAProviderBoundary(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	spec := validSpec(t, row, keyService)
	spec.ClientProtocol = protocol.CodexLive
	spec.Operation = execution.OperationLiveCall
	spec.ClientModel = channel.CodexLiveModelID
	spec.UpstreamModel = channel.CodexLiveModelID
	spec.Method = http.MethodPost
	spec.Path = "/v1/realtime/calls"
	spec.Body = json.RawMessage(`{"model":"gpt-live-1-codex"}`)
	provider, _, err := adapter.validateSpec(spec)
	if err != nil || provider.ProviderKind() != channel.ProviderCodex {
		t.Fatalf("Codex live attempt rejected: provider=%v error=%v", provider, err)
	}
}

func TestCodexLiveRejectionEvidenceDoesNotReplayUnknownCreation(t *testing.T) {
	for _, status := range []int{400, 401, 403, 429, 500, 502, 503} {
		evidence := codexLiveFailure(&codex.LiveHTTPError{Status: status})
		rejected := status == 401 || status == 403 || status == 429
		if (evidence.ReplaySafety == execution.ReplaySafetyRejectedBeforeProcessing) != rejected {
			t.Fatalf("status %d replay safety = %s", status, evidence.ReplaySafety)
		}
	}
	if evidence := codexLiveFailure(errors.New("unknown transport result")); evidence.ReplaySafety == execution.ReplaySafetyRejectedBeforeProcessing {
		t.Fatal("transport failure marked safe for replay")
	}
}
