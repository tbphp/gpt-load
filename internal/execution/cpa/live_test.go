package cpa

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
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
