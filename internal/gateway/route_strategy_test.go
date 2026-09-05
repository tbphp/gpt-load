package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
	"gpt-load/internal/testutil/encryptiontest"
)

func TestHandlerUsesPublishedRouteStrategyAndPreservesSelectedRoute(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true},
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true},
	}}
	engine, manager, registry := newHandlerTestRuntime(t, forwarder)
	keyService := encryptiontest.Service(t, "handler-test-master-key")
	input := state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"), Status: state.AccessKeyStatusActive,
		}},
	}
	entries := make([]state.CredentialEntry, 0, 2)
	for index, target := range []struct {
		channelID channel.ID
		model     string
		weight    int
	}{
		{channel.OpenAI, "native-model", 1},
		{channel.Anthropic, "converted-model", 100},
	} {
		id := uint(index + 1)
		input.Groups = append(input.Groups, state.GroupConfig{
			ID: id, Name: string(target.channelID), ChannelID: target.channelID, ConnectionType: "api_key",
			Params: json.RawMessage(`{}`), Enabled: true, WeightManual: &target.weight,
			Models: []state.ModelConfig{{ID: target.model, Alias: "public"}},
		})
		credential := testCredentialConfig(id, id)
		input.Credentials = append(input.Credentials, credential)
		encrypted, err := keyService.Encrypt(`{"api_key":"test-upstream-key"}`)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, state.CredentialEntry{
			ID: id, GroupID: id, Status: credential.Status, Version: credential.Version,
			IdentityGeneration: credential.IdentityGeneration, Fingerprint: credential.Fingerprint,
			EncryptedValue: encrypted,
		})
	}
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatal(err)
	}
	for index, strategy := range []state.RouteStrategy{state.RouteStrategyNativeFirst, state.RouteStrategyWeightedMix} {
		input.SystemSettings = config.Settings{state.SettingRouteStrategy: string(strategy)}
		if _, err := manager.Publish(input); err != nil {
			t.Fatalf("publish %s: %v", strategy, err)
		}
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			bytes.NewBufferString(`{"model":"public","messages":[{"role":"user","content":"hello"}]}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || len(forwarder.inputs) != index+1 {
			t.Fatalf("%s response/attempts = %d %s / %d", strategy, recorder.Code, recorder.Body, len(forwarder.inputs))
		}
		got := forwarder.inputs[index]
		wantChannel, wantMode, wantModel := channel.OpenAI, execution.RouteNative, "native-model"
		if strategy == state.RouteStrategyWeightedMix {
			wantChannel, wantMode, wantModel = channel.Anthropic, execution.RouteConverted, "converted-model"
		}
		if got.ChannelID != string(wantChannel) || got.RouteMode != wantMode || got.UpstreamModelID != wantModel {
			t.Fatalf("%s selected channel/mode/model = %s/%s/%s", strategy, got.ChannelID, got.RouteMode, got.UpstreamModelID)
		}
	}
}
