package control

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestCredentialPriorityUpdateRoundTrip(t *testing.T) {
	fixture := newServiceFixture(t)
	var request GroupCreateRequest
	if err := json.Unmarshal([]byte(`{"channel_id":"openai","connection_type":"api_key","params":{},"models":[{"id":"gpt-4o","alias":"","alias_enabled":false}],"credentials":"test-credential-priority"}`), &request); err != nil {
		t.Fatal(err)
	}
	created, err := fixture.service.CreateGroup(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	var row models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Priority != 0 {
		t.Fatalf("new credential priority = %d, want default 0", row.Priority)
	}

	initControlI18n(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	groupPath := "/api/groups/" + stringGroupID(created.GroupID)
	credentialPath := groupPath + "/credentials/" + stringGroupID(row.ID)
	for _, test := range []struct {
		body string
		want int
	}{
		{`{"priority":3}`, 3},
		{`{"weight_manual":7}`, 3},
		{`{"priority":2147483647}`, 2147483647},
		{`{"priority":-2147483648,"weight_manual":null}`, -2147483648},
		{`{"name":"primary"}`, -2147483648},
		{`{"priority":0}`, 0},
		{`{"priority":5}`, 5},
	} {
		response := serveGroupSettingsRequest(t, engine, http.MethodPut, credentialPath, "test-auth-key", test.body)
		if response.Code != http.StatusOK {
			t.Fatalf("update %s: %d %s", test.body, response.Code, response.Body.String())
		}
		var envelope struct{ Data json.RawMessage }
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		assertPriorityJSON(t, envelope.Data, "priority", test.want)
		var persisted models.Credential
		if err := fixture.db.Where("id = ? AND group_id = ?", row.ID, created.GroupID).Take(&persisted).Error; err != nil {
			t.Fatal(err)
		}
		if int(persisted.Priority) != test.want {
			t.Fatalf("persisted priority = %d, want %d", persisted.Priority, test.want)
		}
		view, ok := findRuntimeCredential(fixture.registry.Snapshot(), row.ID)
		if !ok || int(view.Priority) != test.want {
			t.Fatalf("runtime priority = %d (found %v), want %d", view.Priority, ok, test.want)
		}
	}

	for _, path := range []string{groupPath + "/credentials", "/api/modern/groups/" + stringGroupID(created.GroupID) + "/credentials"} {
		response := serveGroupSettingsRequest(t, engine, http.MethodGet, path, "test-auth-key", "")
		if response.Code != http.StatusOK {
			t.Fatalf("list %s: %d %s", path, response.Code, response.Body.String())
		}
		var envelope struct {
			Data struct{ Items []json.RawMessage }
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || len(envelope.Data.Items) != 1 {
			t.Fatalf("list %s items = %d, %v", path, len(envelope.Data.Items), err)
		}
		assertPriorityJSON(t, envelope.Data.Items[0], "priority", 5)
	}
	modernDetail := serveGroupSettingsRequest(t, engine, http.MethodGet, "/api/modern/groups/"+stringGroupID(created.GroupID)+"/credentials/"+stringGroupID(row.ID), "test-auth-key", "")
	var detail struct {
		Data struct{ Credential json.RawMessage }
	}
	if modernDetail.Code != http.StatusOK || json.Unmarshal(modernDetail.Body.Bytes(), &detail) != nil {
		t.Fatalf("modern detail: %d %s", modernDetail.Code, modernDetail.Body.String())
	}
	assertPriorityJSON(t, detail.Data.Credential, "priority", 5)

	revision := fixture.manager.Current().Revision
	for _, value := range []string{"null", "1.5", "2147483648", "-2147483649", `"1"`} {
		response := serveGroupSettingsRequest(t, engine, http.MethodPut, credentialPath, "test-auth-key", `{"priority":`+value+`}`)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid priority %s: %d %s", value, response.Code, response.Body.String())
		}
	}
	var persisted models.Credential
	if err := fixture.db.Where("id = ? AND group_id = ?", row.ID, created.GroupID).Take(&persisted).Error; err != nil || persisted.Priority != 5 {
		t.Fatalf("invalid priority changed persisted credential: %d, %v", persisted.Priority, err)
	}
	if view, _ := findRuntimeCredential(fixture.registry.Snapshot(), row.ID); view.Priority != 5 || fixture.manager.Current().Revision != revision {
		t.Fatalf("invalid priority changed runtime credential: %d", view.Priority)
	}
}

func TestRouteInspectReportsCredentialPriority(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	if _, err := fixture.manager.Publish(state.CompileInput{
		ChannelRegistry: fixture.channelRegistry,
		Groups: []state.GroupConfig{{ConnectionType: "api_key", ID: 1, Name: "tiered", ChannelID: channel.OpenAI,
			Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true}},
		AccessKeys: []state.AccessKeyConfig{{ID: 10, Name: "client", KeyHash: "hash", Status: state.AccessKeyStatusActive}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{
		{ID: 11, GroupID: 1, Version: 1, IdentityGeneration: 11, Fingerprint: "high", Status: state.CredentialStatusActive, EncryptedValue: "high", Priority: 2},
		{ID: 12, GroupID: 1, Version: 1, IdentityGeneration: 12, Fingerprint: "low", Status: state.CredentialStatusActive, EncryptedValue: "low"},
	}); err != nil {
		t.Fatalf("ReplaceCredentials() error = %v", err)
	}
	result, err := fixture.service.InspectRoute(routeInspectRequest{Protocol: protocol.OpenAICompletions, ExternalModel: "gpt-4o", AccessKeyID: 10})
	if err != nil || len(result.Groups) != 1 || len(result.Groups[0].Credentials) != 2 {
		t.Fatalf("InspectRoute() = %#v, %v", result, err)
	}
	priorities := map[uint]int32{}
	for _, credential := range result.Groups[0].Credentials {
		priorities[credential.CredentialID] = credential.Priority
	}
	if priorities[11] != 2 || priorities[12] != 0 {
		t.Fatalf("credential priorities = %v", priorities)
	}
}
