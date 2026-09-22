package control

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/execution/bifrost"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/provideradapter"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestCredentialProbeProtocolOverrideDoesNotChangeGroupDefault(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "protocol-test-secret")
	var credential models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Take(&credential).Error; err != nil {
		t.Fatal(err)
	}
	executor := &credentialProbeTestExecutor{result: successfulCredentialProbeResult()}
	fixture.service.executor = executor
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "protocol-auth"}, fixture.service).RegisterRoutes(engine)
	path := fmt.Sprintf("/api/groups/%d/credentials/%d/test", groupID, credential.ID)
	response := serveCredentialRequest(t, engine, http.MethodPost, path, `{"protocol":"openai-embeddings","model":" temporary-model "}`, "protocol-auth", "")
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	calls := executor.recordedCalls()
	if len(calls) != 1 || calls[0].ClientProtocol != protocol.OpenAIEmbeddings || calls[0].UpstreamModel != "temporary-model" {
		t.Fatalf("calls = %#v", calls)
	}
	settings, err := fixture.service.GetGroupSettings(t.Context(), groupID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(settings)
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["validation_protocol"] != string(protocol.OpenAICompletions) || fields["validation_model"] != nil {
		t.Fatalf("settings = %s", encoded)
	}
}

func TestValidationProtocolSettingsAndAutomaticTarget(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "protocol-settings-secret")
	settings, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		ValidationProtocol: optionalField[protocol.Protocol]{Set: true, Value: protocol.OpenAIEmbeddings},
	})
	if err != nil {
		t.Fatal(err)
	}
	if settings.ValidationProtocol == nil || *settings.ValidationProtocol != protocol.OpenAIEmbeddings {
		t.Fatalf("settings = %#v", settings)
	}
	target, valid := buildGroupValidationTarget(fixture.service.manager.Current().Groups[groupID])
	if !valid || target.protocol != protocol.OpenAIEmbeddings || len(target.fallbackProtocols) != 0 {
		t.Fatalf("automatic target = %#v, %v", target, valid)
	}
	_, err = fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		ValidationProtocol: optionalField[protocol.Protocol]{Set: true, Value: protocol.Protocol("unsupported")},
	})
	if err == nil {
		t.Fatal("unsupported protocol accepted")
	}
	if _, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{ValidationProtocol: optionalField[protocol.Protocol]{Set: true, Value: protocol.Anthropic}}); err != nil {
		t.Fatal(err)
	}
	var credential models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Take(&credential).Error; err != nil {
		t.Fatal(err)
	}
	fixture.service.executor = &credentialProbeTestExecutor{result: successfulCredentialProbeResult()}
	if _, err := fixture.service.TestGroupCredential(t.Context(), groupID, credential.ID, CredentialProbeRequest{Protocol: optionalField[protocol.Protocol]{Set: true, Value: protocol.Anthropic}}); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitProbeProtocolDoesNotFallback(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "protocol-no-fallback")
	var credential models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Take(&credential).Error; err != nil {
		t.Fatal(err)
	}
	result := failedCredentialProbeResult(http.StatusNotFound, execution.ErrorKindHTTP, execution.FailureHintModelUnavailable)
	result.Error.OriginHint = execution.ErrorOriginUpstream
	executor := &credentialProbeTestExecutor{result: result}
	fixture.service.executor = executor
	response, err := fixture.service.TestGroupCredential(t.Context(), groupID, credential.ID, CredentialProbeRequest{Protocol: optionalField[protocol.Protocol]{Set: true, Value: protocol.OpenAICompletions}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Protocol != protocol.OpenAICompletions || len(executor.recordedCalls()) != 1 {
		t.Fatalf("unexpected fallback: %#v", response)
	}
}

func TestValidationProtocolOptionsUseChannelDeclarations(t *testing.T) {
	registry := channel.NewRegistry()
	for _, id := range []channel.ID{channel.Alibaba, channel.Groq, channel.OpenAI, channel.Jev, channel.OpenRouter} {
		t.Run(string(id), func(t *testing.T) {
			response, err := groupSettingsResponse(models.Group{ChannelID: string(id), Models: models.JSON(`[]`)}, state.RuntimeSettings{}, registry)
			if err != nil {
				t.Fatal(err)
			}
			target, err := registry.Resolve(id, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range protocol.DataPlaneProtocols() {
				_, declared := target.Mode(candidate, execution.OperationProbe)
				if slices.Contains(response.ValidationProtocols, candidate) != declared {
					t.Fatalf("protocols = %v; declaration mismatch for %s", response.ValidationProtocols, candidate)
				}
			}
		})
	}
}

func TestGatewayValidationProtocolsAndExplicitSelection(t *testing.T) {
	for _, id := range []channel.ID{channel.NewAPI, channel.GPTLoad, channel.CLIProxyAPI, channel.Sub2API} {
		t.Run(string(id), func(t *testing.T) {
			fixture := newServiceFixture(t)
			created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
				ChannelID: id, Params: json.RawMessage(`{"base_url":"https://gateway.example/team-a"}`),
				ConnectionType: "api_key", Credentials: "gateway-probe-test-secret",
				Models: optionalGroupModels{Set: true, Values: []GroupModel{{ID: "probe-model", Alias: "client-alias", AliasEnabled: true}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			settings, err := fixture.service.GetGroupSettings(t.Context(), created.GroupID)
			if err != nil {
				t.Fatal(err)
			}
			want := []protocol.Protocol{protocol.OpenAICompletions, protocol.OpenAIResponses, protocol.Anthropic, protocol.Gemini}
			if id == channel.NewAPI || id == channel.GPTLoad {
				want = append(want, protocol.OpenAIEmbeddings, protocol.Rerank)
			}
			if len(settings.ValidationProtocols) != len(want) {
				t.Fatalf("test protocols = %v, want %v", settings.ValidationProtocols, want)
			}
			for _, selected := range want {
				if !slices.Contains(settings.ValidationProtocols, selected) {
					t.Errorf("missing test protocol %s", selected)
				}
			}
			if settings.ValidationProtocol == nil || *settings.ValidationProtocol != protocol.OpenAICompletions {
				t.Fatal("default test protocol must remain openai-completions")
			}
			var credential models.Credential
			if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&credential).Error; err != nil {
				t.Fatal(err)
			}
			for _, selected := range []protocol.Protocol{protocol.OpenAIResponses, protocol.Anthropic, protocol.Gemini} {
				executor := &credentialProbeTestExecutor{result: failedCredentialProbeResult(http.StatusNotFound, execution.ErrorKindHTTP, execution.FailureHintModelUnavailable)}
				fixture.service.executor = executor
				response, err := fixture.service.TestGroupCredential(t.Context(), created.GroupID, credential.ID, CredentialProbeRequest{
					Protocol: optionalField[protocol.Protocol]{Set: true, Value: selected},
				})
				if err != nil {
					t.Fatal(err)
				}
				calls := executor.recordedCalls()
				if len(calls) != 1 || calls[0].ClientProtocol != selected || calls[0].UpstreamModel != "probe-model" ||
					response.Protocol != selected || response.Outcome == CredentialProbeOutcomePassed || response.CanRestore {
					t.Fatalf("explicit %s probe = %#v; calls = %#v", selected, response, calls)
				}
			}
			settings, err = fixture.service.GetGroupSettings(t.Context(), created.GroupID)
			if err != nil || settings.ValidationProtocol == nil || *settings.ValidationProtocol != protocol.OpenAICompletions {
				t.Fatalf("temporary selection changed default: %#v; error = %v", settings, err)
			}
			for _, selected := range []protocol.Protocol{protocol.OpenAIResponses, protocol.Anthropic, protocol.Gemini} {
				settings, err = fixture.service.UpdateGroupSettings(t.Context(), created.GroupID, GroupSettingsUpdateRequest{
					ValidationProtocol: optionalField[protocol.Protocol]{Set: true, Value: selected},
				})
				if err != nil || settings.ValidationProtocol == nil || *settings.ValidationProtocol != selected {
					t.Fatalf("save %s test protocol: %#v; error = %v", selected, settings, err)
				}
				target, valid := buildGroupValidationTarget(fixture.service.manager.Current().Groups[created.GroupID])
				if !valid || target.protocol != selected || len(target.fallbackProtocols) != 0 {
					t.Fatalf("automatic test target = %#v; valid = %t", target, valid)
				}
				executor := &credentialProbeTestExecutor{result: successfulCredentialProbeResult()}
				fixture.service.executor = executor
				response, err := fixture.service.TestGroupCredential(t.Context(), created.GroupID, credential.ID)
				if err != nil || response.Protocol != selected || response.Outcome != CredentialProbeOutcomePassed || len(executor.recordedCalls()) != 1 {
					t.Fatalf("default %s probe = %#v; error = %v", selected, response, err)
				}
			}
		})
	}
}

func TestCredentialProbeRejectsInvalidTemporaryModel(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "invalid-model-secret")
	var credential models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Take(&credential).Error; err != nil {
		t.Fatal(err)
	}
	for _, model := range []optionalField[string]{
		{Set: true, Null: true}, {Set: true, Value: ""}, {Set: true, Value: "   "}, {Set: true, Value: "model\ninvalid"},
	} {
		if _, err := fixture.service.TestGroupCredential(t.Context(), groupID, credential.ID, CredentialProbeRequest{Model: model}); err == nil {
			t.Fatalf("invalid model accepted: %#v", model)
		}
	}
}

func TestGatewayMalformedProbeDoesNotRecoverCredential(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		selected protocol.Protocol
		invalid  string
		valid    string
	}{
		{protocol.OpenAIResponses, `{"object":"response","status":"completed","output":[null]}`, `{"object":"response","status":"incomplete","output":[]}`},
		{protocol.Anthropic, `{"type":"message","content":[1]}`, `{"type":"message","content":[]}`},
		{protocol.Gemini, `{"candidates":[null]}`, `{"candidates":[{"finishReason":"MAX_TOKENS"}]}`},
		{protocol.OpenAIResponses, `{"object":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":null}]}]}`, `{"object":"response","status":"incomplete","output":[]}`},
		{protocol.Anthropic, `{"type":"message","content":[{"type":"text"}]}`, `{"type":"message","content":[]}`},
		{protocol.Gemini, `{"candidates":[{"content":{"parts":[{"text":null}]}}]}`, `{"candidates":[{"finishReason":"MAX_TOKENS"}]}`},
		{protocol.Gemini, `{"candidates":[{"content":{"parts":[{"text":"pong","functionCall":{"name":"clock","args":{}}}]}}]}`, `{"candidates":[{"content":{"parts":[{"text":"","thought":true,"thoughtSignature":"c2ln"}]}}]}`},
	} {
		t.Run(string(tc.selected), func(t *testing.T) {
			var healthy atomic.Bool
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				body := tc.invalid
				if healthy.Load() {
					body = tc.valid
				}
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			params, err := json.Marshal(map[string]string{"base_url": server.URL})
			if err != nil {
				t.Fatal(err)
			}
			group := state.GroupView{ID: 1, ValidationModel: "probe-model", ValidationProtocol: tc.selected}
			setValidationChannel(&group, channel.NewAPI, params)
			worker := newValidationWorkerForTest(validationSnapshot(map[uint]state.GroupView{1: group}), []state.CredentialRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}}, &validationProbeRecorder{})
			runtime, err := bifrost.NewRuntime(t.Context(), worker.channels)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(runtime.Shutdown)
			if err := runtime.Reconcile([]provideradapter.RuntimeTarget{{Target: group.ResolvedTarget}}); err != nil {
				t.Fatal(err)
			}
			worker.executor = runtime
			worker.Validate(t.Context())
			if calls.Load() != 1 || len(worker.recorder.events()) != 0 {
				t.Errorf("malformed response: calls = %d; recovery events = %v", calls.Load(), worker.recorder.events())
			}
			healthy.Store(true)
			worker.Validate(t.Context())
			if calls.Load() != 2 || !slices.Equal(worker.recorder.events(), []string{"registry.recover:7", "stats.reset:7"}) {
				t.Errorf("valid response: calls = %d; recovery events = %v", calls.Load(), worker.recorder.events())
			}
		})
	}
}
