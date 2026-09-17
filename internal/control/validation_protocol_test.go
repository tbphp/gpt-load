package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
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
	for _, id := range []channel.ID{channel.Alibaba, channel.Groq, channel.OpenAI} {
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
