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
