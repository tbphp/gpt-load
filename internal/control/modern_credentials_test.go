package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/storage/models"
)

func TestModernCredentialFiltersRunBeforePagination(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	mustEnsureInitialPrices(t, fixture)
	keys := make([]string, 21)
	for i := range keys {
		keys[i] = fmt.Sprintf("sk-modern-filter-%02d", i)
	}
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("modern-filter"), ChannelID: channel.OpenAI,
		Params: json.RawMessage(`{}`), Models: optionalGroupModels{Set: true},
		Credentials: strings.Join(keys, "\n"), ConnectionType: "api_key",
	})
	if err != nil {
		t.Fatal(err)
	}
	var last models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Order("id DESC").Take(&last).Error; err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	credentialKey := fixture.encryption.Hash("credential-filter/v1|openai|api_key|" + last.Fingerprint)
	updated := serveGroupDetailLedgerRoute(t, engine, http.MethodPut,
		fmt.Sprintf("/api/groups/%d/credentials/%d", created.GroupID, last.ID),
		`{"weight_manual":99,"proxy":{"mode":"direct"}}`, "Bearer test-auth-key")
	assertGroupDetailLedgerEnvelope(t, updated, http.StatusOK, "")
	for _, test := range []struct {
		query string
		total int
		count int
	}{
		{"sort=weight_desc&page_size=20", 21, 20},
		{"proxy=direct&page_size=20", 1, 1},
		{"credential_key=" + credentialKey + "&page_size=20", 1, 1},
	} {
		path := fmt.Sprintf("/api/modern/groups/%d/credentials?%s", created.GroupID, test.query)
		recorder := serveGroupDetailLedgerRoute(t, engine, http.MethodGet, path, "", "Bearer test-auth-key")
		assertGroupDetailLedgerEnvelope(t, recorder, http.StatusOK, "")
		var envelope struct {
			Data CredentialCollectionResponse `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		data := envelope.Data
		if data.Pagination.TotalItems != test.total || len(data.Items) != test.count || data.Items[0].CredentialID != last.ID {
			t.Fatalf("%s did not filter/sort before paging: %#v", test.query, data)
		}
	}
	for _, prefix := range []string{"/api", "/api/modern"} {
		path := fmt.Sprintf("%s/groups/%d/credentials?sort=invalid", prefix, created.GroupID)
		recorder := serveGroupDetailLedgerRoute(t, engine, http.MethodGet, path, "", "Bearer test-auth-key")
		assertGroupDetailLedgerEnvelope(t, recorder, http.StatusBadRequest, "")
	}
	classic := serveGroupDetailLedgerRoute(t, engine, http.MethodGet,
		fmt.Sprintf("/api/groups/%d/credentials?proxy=direct", created.GroupID), "", "Bearer test-auth-key")
	assertGroupDetailLedgerEnvelope(t, classic, http.StatusBadRequest, "")
	for _, query := range []string{"credential_key=0", "credential_key=abc", "credential_key=" + credentialKey + "&credential_key=" + credentialKey, "credential_id=1"} {
		recorder := serveGroupDetailLedgerRoute(t, engine, http.MethodGet,
			fmt.Sprintf("/api/modern/groups/%d/credentials?%s", created.GroupID, query), "", "Bearer test-auth-key")
		assertGroupDetailLedgerEnvelope(t, recorder, http.StatusBadRequest, "")
	}
	otherGroup, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("other-group"), ChannelID: channel.OpenAI,
		Params: json.RawMessage(`{}`), Models: optionalGroupModels{Set: true},
		Credentials: "sk-other-group", ConnectionType: "api_key", ConfirmSameTarget: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign := serveGroupDetailLedgerRoute(t, engine, http.MethodGet,
		fmt.Sprintf("/api/modern/groups/%d/credentials?credential_key=%s", otherGroup.GroupID, credentialKey), "", "Bearer test-auth-key")
	assertGroupDetailLedgerEnvelope(t, foreign, http.StatusOK, "")
	var foreignEnvelope struct {
		Data CredentialCollectionResponse `json:"data"`
	}
	if err := json.Unmarshal(foreign.Body.Bytes(), &foreignEnvelope); err != nil {
		t.Fatal(err)
	}
	if foreignEnvelope.Data.Pagination.TotalItems != 0 || len(foreignEnvelope.Data.Items) != 0 {
		t.Fatal("credential filter leaked a credential from another group")
	}
	classicExact := serveGroupDetailLedgerRoute(t, engine, http.MethodGet,
		fmt.Sprintf("/api/groups/%d/credentials?credential_key=%s", created.GroupID, credentialKey), "", "Bearer test-auth-key")
	assertGroupDetailLedgerEnvelope(t, classicExact, http.StatusBadRequest, "")
}

func TestModernCredentialRoutesLoadActivity(t *testing.T) {
	for _, connection := range []string{"api_key", "subscription"} {
		t.Run(connection, func(t *testing.T) {
			initControlI18n(t)
			var fixture serviceFixture
			var groupID, credentialID uint
			if connection == "subscription" {
				fixture, groupID, credentialID = newSubscriptionCredentialFixture(t)
			} else {
				fixture = newServiceFixture(t)
				mustEnsureInitialPrices(t, fixture)
				created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
					Name: stringPointer("modern-activity"), ChannelID: channel.OpenAI,
					Params: json.RawMessage(`{}`), Models: optionalGroupModels{Set: true},
					Credentials: "sk-modern-activity", ConnectionType: "api_key",
				})
				if err != nil {
					t.Fatal(err)
				}
				groupID = created.GroupID
				var row models.Credential
				if err := fixture.db.Where("group_id = ?", groupID).Take(&row).Error; err != nil {
					t.Fatal(err)
				}
				credentialID = row.ID
			}
			now := time.UnixMilli(1_800_000_000_000)
			fixture.service.now = func() time.Time { return now }
			lastUsed := now.Add(-time.Minute).UnixMilli()
			reader := &recordingCredentialActivityReader{result: map[uint]requestlog.CredentialActivity{
				credentialID: {CredentialID: credentialID, LastUsedAtMS: &lastUsed, SuccessCount: 17, FailureCount: 3, DataComplete: true},
			}}
			fixture.service.credentialActivity = reader
			engine := gin.New()
			NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
			for _, detail := range []bool{false, true} {
				path := fmt.Sprintf("/api/modern/groups/%d/credentials", groupID)
				if detail {
					path += fmt.Sprintf("/%d", credentialID)
				}
				reader.queries = nil
				recorder := serveGroupDetailLedgerRoute(t, engine, http.MethodGet, path, "", "Bearer test-auth-key")
				assertGroupDetailLedgerEnvelope(t, recorder, http.StatusOK, "")
				var envelope struct {
					Data struct {
						Items      []CredentialItemResponse `json:"items"`
						Credential CredentialItemResponse   `json:"credential"`
					} `json:"data"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				item := envelope.Data.Credential
				if !detail {
					if len(envelope.Data.Items) != 1 {
						t.Fatalf("items = %d, want 1", len(envelope.Data.Items))
					}
					item = envelope.Data.Items[0]
				}
				if item.DailyUsage == nil || item.DailyUsage.SuccessCount != 17 || item.DailyUsage.FailureCount != 3 ||
					item.LastUsedAtMS == nil || *item.LastUsedAtMS != lastUsed {
					t.Errorf("%s missing activity: %#v", path, item.DailyUsage)
				}
				if len(reader.queries) != 1 || len(reader.queries[0].CredentialIDs) != 1 ||
					reader.queries[0].CredentialIDs[0] != credentialID ||
					reader.queries[0].FromMS != now.Add(-24*time.Hour).UnixMilli() || reader.queries[0].ToMS != now.UnixMilli() {
					t.Errorf("%s activity query = %#v, want one exact day batch", path, reader.queries)
				}
			}
		})
	}
}

func TestModernCredentialRoutesExposeManualWeightWithoutChangingClassic(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	mustEnsureInitialPrices(t, fixture)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("modern-credential-weight"), ChannelID: channel.OpenAI,
		Params: json.RawMessage(`{}`), Models: optionalGroupModels{Set: true},
		Credentials: "sk-modern-credential-weight", ConnectionType: "api_key",
	})
	if err != nil {
		t.Fatal(err)
	}
	var credential models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&credential).Error; err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	classicList := fmt.Sprintf("/api/groups/%d/credentials", created.GroupID)
	modernList := fmt.Sprintf("/api/modern/groups/%d/credentials", created.GroupID)
	classicDetail := fmt.Sprintf("%s/%d", classicList, credential.ID)
	modernDetail := fmt.Sprintf("%s/%d", modernList, credential.ID)
	for _, path := range []string{modernList, modernDetail} {
		recorder := serveGroupDetailLedgerRoute(t, engine, http.MethodGet, path, "", "")
		assertGroupDetailLedgerEnvelope(t, recorder, http.StatusUnauthorized, "UNAUTHORIZED")
	}
	for _, state := range []struct {
		name  string
		patch string
		want  string
	}{
		{name: "default", want: "null"},
		{name: "override", patch: `{"weight_manual":25}`, want: "25"},
		{name: "restore default", patch: `{"weight_manual":null}`, want: "null"},
	} {
		t.Run(state.name, func(t *testing.T) {
			if state.patch != "" {
				recorder := serveGroupDetailLedgerRoute(t, engine, http.MethodPut, classicDetail, state.patch, "Bearer test-auth-key")
				assertGroupDetailLedgerEnvelope(t, recorder, http.StatusOK, "")
			}
			for _, route := range []struct {
				path   string
				list   bool
				modern bool
			}{
				{path: classicList, list: true},
				{path: classicDetail},
				{path: modernList, list: true, modern: true},
				{path: modernDetail, modern: true},
			} {
				recorder := serveGroupDetailLedgerRoute(t, engine, http.MethodGet, route.path, "", "Bearer test-auth-key")
				assertGroupDetailLedgerEnvelope(t, recorder, http.StatusOK, "")
				var envelope struct {
					Data struct {
						Items      []map[string]json.RawMessage `json:"items"`
						Credential map[string]json.RawMessage   `json:"credential"`
					} `json:"data"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				item := envelope.Data.Credential
				if route.list {
					if len(envelope.Data.Items) != 1 {
						t.Fatalf("%s returned %d credentials, want 1", route.path, len(envelope.Data.Items))
					}
					item = envelope.Data.Items[0]
				}
				if string(item["credential_id"]) != fmt.Sprint(credential.ID) {
					t.Fatalf("%s returned the wrong credential: %s", route.path, recorder.Body.String())
				}
				weight, exists := item["weight_manual"]
				if route.modern {
					if !exists || string(weight) != state.want {
						t.Fatalf("%s weight_manual = %s, want %s", route.path, weight, state.want)
					}
				} else if exists {
					t.Fatalf("%s unexpectedly exposes weight_manual", route.path)
				}
			}
		})
	}
}
