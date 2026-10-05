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

func TestHandlerPriorityRetryOrderAndBudget(t *testing.T) {
	for _, test := range []struct {
		name             string
		retries          int
		preparationFails bool
		want             []uint
	}{
		{"default budget crosses tiers", 2, false, []uint{1, 3, 4}},
		{"lowest tier exhaustion stops early", 8, false, []uint{1, 3, 4, 5}},
		{"preparation keeps current tier", 2, true, []uint{2, 3, 4}},
		{"disabled retries still skip preparation failure", 0, true, []uint{2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := UpstreamResult{StatusCode: http.StatusUnauthorized, RequestWritten: true,
				Header: http.Header{}, Body: []byte(`{"error":"invalid_api_key"}`),
				ClassificationBody: []byte(`{"error":"invalid_api_key"}`)}
			forwarder := &scriptedForwarder{results: []UpstreamResult{invalid, invalid, invalid, invalid, invalid}}
			handler, manager, registry := newHandlerForTest(t, forwarder, "one", "two", "three", "four", "five")
			entries, err := registry.SnapshotGroupCredentialEntriesExact(1, []uint{1, 2, 3, 4, 5})
			if err != nil {
				t.Fatal(err)
			}
			input := state.CompileInput{ChannelRegistry: channel.NewRegistry(),
				SystemSettings: config.Settings{state.SettingRetryCount: test.retries},
				AccessKeys:     []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive}},
			}
			for index, priority := range []int32{100, 100, 0, -100, -100} {
				id := uint(index + 1)
				entries[index].GroupID = id
				input.Groups = append(input.Groups, state.GroupConfig{ID: id, Name: fmt.Sprint(id), Priority: priority,
					ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Enabled: true,
					Models: []state.ModelConfig{{ID: "gpt-4o"}},
				})
				input.Credentials = append(input.Credentials, state.CredentialConfig{ID: id, GroupID: id,
					Status: entries[index].Status, Version: entries[index].Version,
					IdentityGeneration: entries[index].IdentityGeneration, Fingerprint: entries[index].Fingerprint})
			}
			if err := registry.ReplaceCredentials(entries); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.Publish(input); err != nil {
				t.Fatal(err)
			}
			if test.preparationFails {
				handler.encryption = &failCredentialDecrypt{Service: handler.encryption, remaining: 1}
			}
			engine := gin.New()
			bindGatewayRoutesForTest(t, engine, handler)
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || len(forwarder.inputs) != len(test.want) {
				t.Fatalf("status=%d attempts=%d want=%v", response.Code, len(forwarder.inputs), test.want)
			}
			for index, want := range test.want {
				if got := forwarder.inputs[index].Credential.ID; got != want {
					t.Fatalf("attempt %d selected %d, want %d", index, got, want)
				}
			}
		})
	}
}
