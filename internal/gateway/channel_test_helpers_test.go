package gateway

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"gpt-load/internal/channel"
	bifrostexecutor "gpt-load/internal/execution/bifrost"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func testChannelConfig(
	t *testing.T,
	selectedProtocol protocol.Protocol,
	baseURL string,
) (channel.ID, json.RawMessage) {
	t.Helper()
	var channelID channel.ID
	switch selectedProtocol {
	case protocol.OpenAICompletions, protocol.OpenAIResponses:
		channelID = channel.OpenAICompatible
	case protocol.Anthropic:
		channelID = channel.AnthropicCompatible
	case protocol.Gemini:
		channelID = channel.GeminiCompatible
	default:
		t.Fatalf("unsupported test protocol %q", selectedProtocol)
	}
	params, err := json.Marshal(map[string]string{"base_url": baseURL})
	if err != nil {
		t.Fatalf("encode channel params: %v", err)
	}
	return channelID, params
}

func testCredentialConfig(id, groupID uint) state.CredentialConfig {
	return state.CredentialConfig{
		ID: id, GroupID: groupID, Status: state.CredentialStatusActive,
		Version: 1, IdentityGeneration: uint64(id),
		Fingerprint: "test-credential-" + strconv.FormatUint(uint64(id), 10),
	}
}

func testCredentialEntry(
	t *testing.T,
	service encryption.Service,
	id uint,
	groupID uint,
	apiKey string,
) state.CredentialEntry {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"api_key": apiKey})
	if err != nil {
		t.Fatalf("encode credential %d: %v", id, err)
	}
	encrypted, err := service.Encrypt(string(payload))
	if err != nil {
		t.Fatalf("encrypt credential %d: %v", id, err)
	}
	return state.CredentialEntry{
		ID: id, GroupID: groupID, Status: state.CredentialStatusActive,
		Version: 1, IdentityGeneration: uint64(id),
		Fingerprint: "test-credential-" + strconv.FormatUint(uint64(id), 10), EncryptedValue: encrypted,
	}
}

func newTestExecutionForwarder(t *testing.T) AttemptForwarder {
	t.Helper()
	runtime, err := bifrostexecutor.NewRuntime(context.Background(), channel.NewRegistry())
	if err != nil {
		t.Fatalf("initialize execution runtime: %v", err)
	}
	t.Cleanup(runtime.Shutdown)
	return NewExecutionForwarder(runtime)
}
