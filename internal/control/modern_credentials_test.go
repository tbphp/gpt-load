package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage/models"
)

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
