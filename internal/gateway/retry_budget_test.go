package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
)

func TestHandlerFreezesSystemRetryBudgetAcrossGroups(t *testing.T) {
	for _, test := range []struct {
		name            string
		systemCount     int
		groupCounts     []int
		decryptFailures int
		updatedCount    *int
		wantAttempts    int
	}{
		{name: "system zero overrides legacy group retries", systemCount: 0, groupCounts: []int{4, 4, 4, 4}, wantAttempts: 1},
		{name: "system budget crosses groups with zero retries", systemCount: 2, groupCounts: []int{0, 0, 0, 0}, wantAttempts: 3},
		{name: "later group cannot increase request budget", systemCount: 1, groupCounts: []int{4, 4, 4, 4}, wantAttempts: 2},
		{name: "later group cannot reduce request budget", systemCount: 3, groupCounts: []int{1, 0, 0, 0}, wantAttempts: 4},
		{name: "preparation failure does not consume forward budget", systemCount: 2, groupCounts: []int{0, 0, 0, 0}, decryptFailures: 1, wantAttempts: 3},
		{name: "disabled retries still skip unusable candidates", systemCount: 0, groupCounts: []int{4, 4, 4, 4}, decryptFailures: 1, wantAttempts: 1},
		{name: "runtime update only affects the next request", systemCount: 2, groupCounts: []int{4, 4, 4, 4}, updatedCount: new(0), wantAttempts: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := UpstreamResult{
				StatusCode: http.StatusUnauthorized, Header: make(http.Header), RequestWritten: true,
				Body:               []byte(`{"error":"invalid_api_key"}`),
				ClassificationBody: []byte(`{"error":"invalid_api_key"}`),
			}
			forwarder := &scriptedForwarder{results: []UpstreamResult{invalid, invalid, invalid, invalid, invalid}}
			handler, manager, registry := newHandlerForTest(t, forwarder, "sk-first", "sk-second", "sk-third", "sk-fourth")
			entries, err := registry.SnapshotGroupCredentialEntriesExact(1, []uint{1, 2, 3, 4})
			if err != nil {
				t.Fatal(err)
			}
			input := state.CompileInput{
				SystemSettings:  config.Settings{state.SettingRetryCount: test.systemCount},
				ChannelRegistry: channel.NewRegistry(),
				AccessKeys: []state.AccessKeyConfig{{
					ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive,
				}},
			}
			for index := range entries {
				id := uint(index + 1)
				entries[index].GroupID = id
				input.Groups = append(input.Groups, state.GroupConfig{
					ID: id, Name: fmt.Sprintf("group-%d", id), ChannelID: channel.OpenAI, ConnectionType: "api_key",
					Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
					Settings: config.Settings{state.SettingRetryCount: test.groupCounts[index]},
				})
				input.Credentials = append(input.Credentials, state.CredentialConfig{
					ID: id, GroupID: id, Status: entries[index].Status, Version: entries[index].Version,
					IdentityGeneration: entries[index].IdentityGeneration, Fingerprint: entries[index].Fingerprint,
				})
			}
			if err := registry.ReplaceCredentials(entries); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.Publish(input); err != nil {
				t.Fatal(err)
			}
			if test.decryptFailures > 0 {
				handler.encryption = &failCredentialDecrypt{Service: handler.encryption, remaining: test.decryptFailures}
			}
			if test.updatedCount != nil {
				forwarder.onCall = func(index int) {
					if index == 0 {
						input.SystemSettings[state.SettingRetryCount] = *test.updatedCount
						if _, err := manager.Publish(input); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			engine := gin.New()
			bindGatewayRoutesForTest(t, engine, handler)
			send := func() {
				request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
				request.Header.Set("Authorization", "Bearer gl-client")
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)
				if response.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401; body=%s", response.Code, response.Body.String())
				}
			}
			send()
			if len(forwarder.inputs) != test.wantAttempts {
				t.Fatalf("attempts = %d, want system budget %d", len(forwarder.inputs), test.wantAttempts)
			}
			for index, attempt := range forwarder.inputs {
				if want := uint(index + 1 + test.decryptFailures); attempt.Group.ID != want {
					t.Fatalf("attempt %d group = %d, want %d", index, attempt.Group.ID, want)
				}
			}
			if test.updatedCount != nil {
				send()
				if got, want := len(forwarder.inputs)-test.wantAttempts, *test.updatedCount+1; got != want {
					t.Fatalf("next request attempts = %d, want updated system budget %d", got, want)
				}
			}
		})
	}
}
