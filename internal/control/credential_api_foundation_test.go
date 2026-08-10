package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestCredentialRoutesReplaceLegacyGroupKeyRoutes(t *testing.T) {
	fixture := newServiceFixture(t)
	module := NewServer(&config.Config{AuthKey: "credential-auth"}, fixture.service).HTTPModule()
	want := map[string]string{
		"control.group-credentials.list":    "/groups/:group_id/credentials",
		"control.group-credentials.reveal":  "/groups/:group_id/credentials/:credential_id/reveal",
		"control.group-credentials.update":  "/groups/:group_id/credentials/:credential_id",
		"control.group-credentials.restore": "/groups/:group_id/credentials/:credential_id/restore",
		"control.group-credentials.batch":   "/groups/:group_id/credentials/batch",
		"control.group-credentials.delete":  "/groups/:group_id/credentials/:credential_id",
		"control.group-credentials.import":  "/groups/:group_id/credentials/import",
	}
	seen := make(map[string]string)
	for _, route := range module.Routes {
		if strings.Contains(route.Path, "/keys") || strings.HasPrefix(route.Name, "control.group-keys") {
			t.Fatalf("legacy key route remains: %s %s", route.Name, route.Path)
		}
		if path, ok := want[route.Name]; ok {
			if len(route.Methods) != 1 || route.Path != path {
				t.Fatalf("route %s = %#v", route.Name, route)
			}
			seen[route.Name] = route.Methods[0]
		}
	}
	if len(seen) != len(want) || seen["control.group-credentials.list"] != http.MethodGet {
		t.Fatalf("credential routes = %#v, want %#v", seen, want)
	}
}

func TestImportAndListGroupCredentialsUseCanonicalCredentialStorage(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("credential api"), ChannelID: channel.OpenAI,
		Params: json.RawMessage(`{}`), Models: optionalGroupModels{Set: true}, Credentials: "first-secret",
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	result, err := fixture.service.ImportGroupCredentials(t.Context(), created.GroupID, CredentialImportRequest{
		Credentials: " second-secret \nfirst-secret\nsecond-secret\n",
	})
	if err != nil {
		t.Fatalf("ImportGroupCredentials() error = %v", err)
	}
	if result.CredentialsAdded != 1 || result.CredentialsDuplicated != 2 {
		t.Fatalf("import result = %#v", result)
	}
	var stored []models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Order("id ASC").Find(&stored).Error; err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored credentials = %#v", stored)
	}
	response, err := fixture.service.ListGroupCredentials(t.Context(), created.GroupID, CredentialCollectionQuery{Page: 1, PageSize: 20})
	if err != nil || response.Summary.Total != 2 || len(response.Items) != 2 {
		t.Fatalf("ListGroupCredentials() = %#v, %v", response, err)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, "first-secret") || strings.Contains(text, "second-secret") ||
		strings.Contains(text, `"id"`) || strings.Contains(text, "key_id") {
		t.Fatalf("credential collection leaked legacy or secret data: %s", encoded)
	}
}

func TestCloudCredentialImportAcceptsOneStrictJSONObjectPerLine(t *testing.T) {
	fixture := newServiceFixture(t)
	tests := []struct {
		name        string
		channelID   channel.ID
		params      string
		credentials string
		want        string
	}{
		{
			name: "Azure Entra", channelID: channel.AzureOpenAI,
			params:      `{"endpoint":"https://resource.openai.azure.com"}`,
			credentials: `{"tenant_id":" tenant ","client_secret":" secret ","client_id":" client "}`,
			want:        `{"client_id":"client","client_secret":"secret","tenant_id":"tenant"}`,
		},
		{
			name: "Bedrock SigV4", channelID: channel.AWSBedrock,
			params:      `{"region":"us-east-1"}`,
			credentials: `{"secret_key":" secret ","access_key":" access "}`,
			want:        `{"access_key":"access","secret_key":"secret"}`,
		},
		{
			name: "Vertex service account", channelID: channel.GoogleVertex,
			params:      `{"location":"us-central1","project_id":"project-one"}`,
			credentials: `{"service_account_json":"{\"type\":\"service_account\",\"project_id\":\"project-one\",\"client_email\":\"svc@example.iam.gserviceaccount.com\",\"private_key\":\"private-secret\"}"}`,
			want:        `{"service_account_json":"{\"client_email\":\"svc@example.iam.gserviceaccount.com\",\"private_key\":\"private-secret\",\"project_id\":\"project-one\",\"type\":\"service_account\"}"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
				Name: stringPointer("cloud " + test.name), ChannelID: test.channelID,
				Params: json.RawMessage(test.params), Models: optionalGroupModels{Set: true},
				Credentials: test.credentials + "\n" + test.credentials,
			})
			if err != nil {
				t.Fatalf("CreateGroup() error = %v", err)
			}
			if created.CredentialsAdded != 1 || created.CredentialsDuplicated != 1 {
				t.Fatalf("create result = %#v", created)
			}
			var row models.Credential
			if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&row).Error; err != nil {
				t.Fatal(err)
			}
			plaintext, err := fixture.encryption.Decrypt(row.Data)
			if err != nil {
				t.Fatal(err)
			}
			if plaintext != test.want {
				t.Fatalf("stored credential = %s, want %s", plaintext, test.want)
			}
		})
	}
}

func TestVertexCredentialImportAcceptsPastedServiceAccountJSON(t *testing.T) {
	fixture := newServiceFixture(t)
	raw := `{
  "type": "service_account",
  "project_id": "project-one",
  "client_email": "svc@example.iam.gserviceaccount.com",
  "private_key": "private-secret"
}`
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("vertex pasted credential"), ChannelID: channel.GoogleVertex,
		Params: json.RawMessage(`{"location":"us-central1","project_id":"project-one"}`),
		Models: optionalGroupModels{Set: true}, Credentials: raw,
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	if created.CredentialsAdded != 1 || created.CredentialsDuplicated != 0 {
		t.Fatalf("create result = %#v", created)
	}
	var row models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	plaintext, err := fixture.encryption.Decrypt(row.Data)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"service_account_json":"{\"client_email\":\"svc@example.iam.gserviceaccount.com\",\"private_key\":\"private-secret\",\"project_id\":\"project-one\",\"type\":\"service_account\"}"}`
	if plaintext != want {
		t.Fatalf("stored credential = %s, want %s", plaintext, want)
	}
}

func TestGroupCredentialMutationsPreserveRuntimeIdentityAndHealthContracts(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("credential mutations"), ChannelID: channel.OpenAI,
		Params: json.RawMessage(`{}`), Models: optionalGroupModels{Set: true},
		Credentials: "first-secret\nsecond-secret",
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	var rows []models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("credentials = %#v", rows)
	}
	before, ok := findRuntimeCredential(fixture.registry.Snapshot(), rows[0].ID)
	if !ok {
		t.Fatal("first credential missing from Registry")
	}
	beforeEntries, err := fixture.registry.SnapshotGroupCredentialEntriesExact(created.GroupID, []uint{rows[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.now = func() time.Time { return time.UnixMilli(rows[0].UpdatedAtMS) }
	weight := 17
	updated, err := fixture.service.UpdateGroupCredential(t.Context(), created.GroupID, rows[0].ID, CredentialUpdateRequest{
		Status:       optionalField[state.CredentialStatus]{Set: true, Value: state.CredentialStatusDisabled},
		WeightManual: optionalField[int]{Set: true, Value: weight},
	})
	if err != nil {
		t.Fatalf("UpdateGroupCredential() error = %v", err)
	}
	if updated.CredentialID != rows[0].ID || updated.ConfiguredStatus != "disabled" ||
		updated.WeightMode != "manual" {
		t.Fatalf("updated credential = %#v", updated)
	}
	var committed models.Credential
	if err := fixture.db.Take(&committed, rows[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	after, ok := findRuntimeCredential(fixture.registry.Snapshot(), rows[0].ID)
	afterEntries, entryErr := fixture.registry.SnapshotGroupCredentialEntriesExact(created.GroupID, []uint{rows[0].ID})
	if !ok || committed.UpdatedAtMS <= rows[0].UpdatedAtMS ||
		committed.WeightManual == nil || *committed.WeightManual != weight ||
		after.WeightManual == nil || *after.WeightManual != weight ||
		after.Version != uint64(committed.UpdatedAtMS) ||
		after.IdentityGeneration != before.IdentityGeneration || entryErr != nil ||
		afterEntries[0].Fingerprint != beforeEntries[0].Fingerprint {
		t.Fatalf("identity/version after update: row=%#v before=%#v after=%#v", committed, before, after)
	}

	batch, err := fixture.service.BatchGroupCredentials(t.Context(), created.GroupID, CredentialBatchRequest{
		Action: CredentialBatchEnable, CredentialIDs: []uint{rows[1].ID, rows[0].ID},
	})
	if err != nil {
		t.Fatalf("BatchGroupCredentials() error = %v", err)
	}
	if len(batch.AffectedCredentialIDs) != 2 || batch.AffectedCredentialIDs[0] != rows[0].ID ||
		batch.AffectedCredentialIDs[1] != rows[1].ID || batch.Summary.Available != 2 {
		t.Fatalf("batch response = %#v", batch)
	}

	if !fixture.registry.SetBlacklisted(rows[0].ID) {
		t.Fatal("blacklist first credential")
	}
	restored, err := fixture.service.RestoreGroupCredential(t.Context(), created.GroupID, rows[0].ID)
	if err != nil {
		t.Fatalf("RestoreGroupCredential() error = %v", err)
	}
	if restored.CredentialID != rows[0].ID || restored.EffectiveStatus != "available" {
		t.Fatalf("restored credential = %#v", restored)
	}

	revealed, err := fixture.service.RevealGroupCredential(t.Context(), created.GroupID, rows[0].ID)
	if err != nil {
		t.Fatalf("RevealGroupCredential() error = %v", err)
	}
	if revealed.CredentialID != rows[0].ID || string(revealed.Credential) != `{"api_key":"first-secret"}` {
		t.Fatalf("revealed credential = %#v", revealed)
	}

	fixture.stats.RecordSuccess(rows[1].ID, fixture.service.now())
	if err := fixture.service.DeleteGroupCredential(t.Context(), created.GroupID, rows[1].ID); err != nil {
		t.Fatalf("DeleteGroupCredential() error = %v", err)
	}
	if _, exists := findRuntimeCredential(fixture.registry.Snapshot(), rows[1].ID); exists {
		t.Fatal("deleted credential remains in Registry")
	}
	var count int64
	if err := fixture.db.Model(&models.Credential{}).Where("id = ?", rows[1].ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("deleted credential count = %d, err=%v", count, err)
	}
	if got := fixture.stats.Snapshot(rows[1].ID, fixture.service.now()); got.Success != 0 {
		t.Fatalf("deleted credential stats = %#v", got)
	}
}

func TestCredentialHTTPUsesCanonicalWireAndRejectsLegacyFields(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("credential http"), ChannelID: channel.OpenAI,
		Params: json.RawMessage(`{}`), Models: optionalGroupModels{Set: true}, Credentials: "first-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	var row models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	const auth = "credential-http-auth"
	NewServer(&config.Config{AuthKey: auth}, fixture.service).RegisterRoutes(engine)

	legacy := serveCredentialRequest(t, engine, http.MethodGet,
		fmt.Sprintf("/api/groups/%d/keys", created.GroupID), "", auth, "")
	if legacy.Code != http.StatusNotFound {
		t.Fatalf("legacy key route = %d %s", legacy.Code, legacy.Body.String())
	}

	const idempotencyKey = "00000000-0000-4000-8000-0000000000c1"
	imported := serveCredentialRequest(t, engine, http.MethodPost,
		fmt.Sprintf("/api/groups/%d/credentials/import", created.GroupID),
		`{"credentials":"second-secret"}`, auth, idempotencyKey)
	if imported.Code != http.StatusOK || !strings.Contains(imported.Body.String(), `"credentials_added":1`) ||
		strings.Contains(imported.Body.String(), "keys_added") {
		t.Fatalf("credential import = %d %s", imported.Code, imported.Body.String())
	}
	replayed := serveCredentialRequest(t, engine, http.MethodPost,
		fmt.Sprintf("/api/groups/%d/credentials/import", created.GroupID),
		`{"credentials":"second-secret"}`, auth, idempotencyKey)
	if replayed.Code != http.StatusOK || replayed.Body.String() != imported.Body.String() {
		t.Fatalf("credential import replay = %d %s, want %s", replayed.Code, replayed.Body.String(), imported.Body.String())
	}
	legacyImportField := serveCredentialRequest(t, engine, http.MethodPost,
		fmt.Sprintf("/api/groups/%d/credentials/import", created.GroupID),
		`{"keys":"third-secret"}`, auth, "00000000-0000-4000-8000-0000000000c2")
	if legacyImportField.Code != http.StatusBadRequest {
		t.Fatalf("legacy import field = %d %s", legacyImportField.Code, legacyImportField.Body.String())
	}

	list := serveCredentialRequest(t, engine, http.MethodGet,
		fmt.Sprintf("/api/groups/%d/credentials", created.GroupID), "", auth, "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"credential_id":`) ||
		strings.Contains(list.Body.String(), "key_id") || strings.Contains(list.Body.String(), "first-secret") ||
		strings.Contains(list.Body.String(), "second-secret") {
		t.Fatalf("credential list = %d %s", list.Code, list.Body.String())
	}

	reveal := serveCredentialRequest(t, engine, http.MethodPost,
		fmt.Sprintf("/api/groups/%d/credentials/%d/reveal", created.GroupID, row.ID), "{}", auth, "")
	if reveal.Code != http.StatusOK || reveal.Header().Get("Cache-Control") != "no-store" ||
		reveal.Header().Get("Pragma") != "no-cache" ||
		!strings.Contains(reveal.Body.String(), `"credential":{"api_key":"first-secret"}`) ||
		strings.Contains(reveal.Body.String(), `"key":`) {
		t.Fatalf("credential reveal = %d headers=%v body=%s", reveal.Code, reveal.Header(), reveal.Body.String())
	}
	legacyRevealField := serveCredentialRequest(t, engine, http.MethodPost,
		fmt.Sprintf("/api/groups/%d/credentials/%d/reveal", created.GroupID, row.ID),
		`{"key_id":1}`, auth, "")
	if legacyRevealField.Code != http.StatusBadRequest {
		t.Fatalf("legacy reveal field = %d %s", legacyRevealField.Code, legacyRevealField.Body.String())
	}
	legacyBatchField := serveCredentialRequest(t, engine, http.MethodPost,
		fmt.Sprintf("/api/groups/%d/credentials/batch", created.GroupID),
		fmt.Sprintf(`{"action":"disable","key_ids":[%d]}`, row.ID), auth, "")
	if legacyBatchField.Code != http.StatusBadRequest {
		t.Fatalf("legacy batch field = %d %s", legacyBatchField.Code, legacyBatchField.Body.String())
	}
}

func TestNormalizeStoredCredentialAcceptsLegacyAPIKeyAndCanonicalObjects(t *testing.T) {
	t.Parallel()

	registry := channel.NewRegistry()
	legacy, err := normalizeStoredCredential(registry, channel.OpenAI, "  sk-legacy  ")
	if err != nil {
		t.Fatalf("normalizeStoredCredential(legacy) error = %v", err)
	}
	if got := string(legacy.CanonicalJSON()); got != `{"api_key":"sk-legacy"}` {
		t.Fatalf("legacy canonical credential = %s", got)
	}

	typed, err := normalizeStoredCredential(
		registry,
		channel.AWSBedrock,
		`{"access_key":"AKIAEXAMPLE","secret_key":"secret"}`,
	)
	if err != nil {
		t.Fatalf("normalizeStoredCredential(typed) error = %v", err)
	}
	if got := string(typed.CanonicalJSON()); got != `{"access_key":"AKIAEXAMPLE","secret_key":"secret"}` {
		t.Fatalf("typed canonical credential = %s", got)
	}

	if _, err := normalizeStoredCredential(registry, channel.OpenAI, `{"api_key":`); err == nil {
		t.Fatal("normalizeStoredCredential(invalid object) error = nil")
	}
}

func serveCredentialRequest(
	t *testing.T,
	engine *gin.Engine,
	method string,
	path string,
	body string,
	auth string,
	idempotencyKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+auth)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}
