package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/automodel"
	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/pricing"
	"gpt-load/internal/requestlog"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/usage"
)

func TestCredentialNameIsIndependentDisplayMetadata(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("named credentials"), ChannelID: channel.OpenAI,
		ConnectionType: models.ConnectionTypeAPIKey, Models: optionalGroupModels{Set: true},
		Credentials: "credential-name-test-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	var before models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&before).Error; err != nil {
		t.Fatal(err)
	}
	if !fixture.registry.SetBlacklisted(before.ID) {
		t.Fatal("blacklist credential")
	}
	runtimeBefore := fixture.registry.Snapshot()
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "name-test-auth"}, fixture.service).RegisterRoutes(engine)
	path := fmt.Sprintf("/api/groups/%d/credentials/%d", created.GroupID, before.ID)
	for _, name := range []string{"  生产账号  ", ""} {
		body, err := json.Marshal(map[string]string{"name": name})
		if err != nil {
			t.Fatal(err)
		}
		res := serveCredentialRequest(t, engine, http.MethodPut, path, string(body), "name-test-auth", "")
		if res.Code != http.StatusOK {
			t.Fatalf("rename status = %d, body = %s", res.Code, res.Body.String())
		}
		var result struct {
			Data struct {
				Name string `json:"name"`
				Mask string `json:"mask"`
			} `json:"data"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		want := "生产账号"
		if name == "" {
			want = ""
		}
		if result.Data.Name != want || result.Data.Mask == "" {
			t.Fatalf("rename result = %+v", result.Data)
		}
		var after models.Credential
		if err := fixture.db.First(&after, before.ID).Error; err != nil {
			t.Fatal(err)
		}
		if after.Data != before.Data || after.SecretVersion != before.SecretVersion || after.Fingerprint != before.Fingerprint || after.IdentityFingerprint != before.IdentityFingerprint || after.Status != before.Status {
			t.Fatal("renaming changed credential identity or configuration")
		}
		if !reflect.DeepEqual(runtimeBefore, fixture.registry.Snapshot()) {
			t.Fatal("renaming changed scheduling or health state")
		}
		var storedName string
		if err := fixture.db.Model(&models.Credential{}).Where("id = ?", before.ID).Pluck("name", &storedName).Error; err != nil {
			t.Fatal(err)
		}
		if storedName != want {
			t.Fatalf("stored name = %q", storedName)
		}
		entries, err := stateloader.BuildCredentialEntries(t.Context(), fixture.db)
		if err != nil || len(entries) != 1 || entries[0].Name != want {
			t.Fatalf("reloaded credential name = %#v, %v", entries, err)
		}
		collection, err := fixture.service.ListGroupCredentials(t.Context(), created.GroupID, CredentialCollectionQuery{Page: 1, PageSize: 20, Query: want})
		if err != nil || len(collection.Items) != 1 || collection.Items[0].Name != want {
			t.Fatalf("named collection = %#v, %v", collection, err)
		}
	}
	for _, body := range []string{`{"name":null}`, `{"name":3}`, `{"name":"line\nbreak"}`, `{"name":"` + strings.Repeat("名", 256) + `"}`} {
		res := serveCredentialRequest(t, engine, http.MethodPut, path, body, "name-test-auth", "")
		if res.Code != http.StatusBadRequest {
			t.Fatalf("invalid name status = %d", res.Code)
		}
	}
}

func TestSubscriptionNamePresentationPreservesPlaintextAccount(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	const email = "account@example.invalid"
	stage, err := fixture.service.ImportCredentialStage(t.Context(), channel.Codex, []byte(`{"type":"codex","access_token":"name-access","refresh_token":"name-refresh","account_id":"name-account","email":"account@example.invalid","expired":"2035-01-01T00:00:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("subscription alias"), ChannelID: channel.Codex,
		ConnectionType: models.ConnectionTypeSubscription, Models: optionalGroupModels{Set: true}, StagedCredentialIDs: []string{stage.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	var row models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	item, err := fixture.service.UpdateGroupCredential(t.Context(), created.GroupID, row.ID, CredentialUpdateRequest{Name: optionalField[string]{Set: true, Value: "生产账号"}})
	if err != nil || item.Name != "生产账号" || item.Account.Email != email {
		t.Fatalf("account presentation = %#v, %v", item, err)
	}
	options, err := fixture.service.ListModernCredentialOptions(t.Context())
	if err != nil || len(options) != 1 || !reflect.DeepEqual(options[0].Names, []string{"生产账号"}) || options[0].Label != email {
		t.Fatalf("options = %#v, %v", options, err)
	}
	const requestID = "11111111-1111-4111-8111-111111111111"
	record := requestlog.Record{RequestID: requestID, CompletedAtMS: 1_700_000_000_000, GroupID: created.GroupID, CredentialID: row.ID, UsageState: usage.StateComplete, CostState: pricing.CostStatePriced, PricingCompleteness: pricing.CompletenessComplete,
		AutoDecision: &automodel.Decision{CredentialID: row.ID},
		Attempts:     []requestlog.Attempt{{Sequence: 1, GroupID: created.GroupID, CredentialID: row.ID}},
	}
	fixture.service.requestLogs = &recordingRequestLogReader{details: map[string]requestlog.Record{requestID: record}, pages: []requestlog.Page{{Items: []requestlog.Record{record}}}}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "name-test-auth"}, fixture.service).RegisterRoutes(engine)
	res := serveCredentialRequest(t, engine, http.MethodGet, "/api/logs/"+requestID, "", "name-test-auth", "")
	if res.Code != http.StatusOK {
		t.Fatalf("log status = %d: %s", res.Code, res.Body.String())
	}
	var response struct {
		Data requestLogDetailResponse `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	log := response.Data
	if log.CredentialName != email || log.CredentialAlias != "生产账号" || log.CredentialConnectionType != "subscription" || len(log.Attempts) != 1 || log.Attempts[0].CredentialAlias != "生产账号" || log.AutoDecision == nil || log.AutoDecision.CredentialAlias != "生产账号" {
		t.Fatalf("log credential projection = %s", res.Body.String())
	}
}
