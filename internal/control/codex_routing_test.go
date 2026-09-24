package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/codexrouting"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage/models"
)

func TestCodexRoutingStatusOmitsCookieAndProxySecrets(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	secret := "SECRET_COOKIE_VALUE_XYZ"
	store := codexrouting.NewStore(t.TempDir(), fixture.encryption, codexrouting.Config{
		Enabled:    true,
		ProbeProxy: "http://user:proxy-secret-pass@127.0.0.1:5000",
		EventLimit: 8,
	})
	header := make(http.Header)
	header.Add("Set-Cookie", "__oailb="+secret+"; Path=/")
	store.Capture(codexrouting.WithDiscovery(t.Context()), 9, "gpt-6-astra", header, 200)
	fixture.service.SetCodexRouting(store, nil)

	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	unauth := httptest.NewRecorder()
	engine.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/api/codex-routing/status", nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d %s", unauth.Code, unauth.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet, "/api/codex-routing/status", nil)
	request.Header.Set("Authorization", "Bearer test-auth-key")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if strings.Contains(body, secret) {
		t.Fatalf("status leaked cookie: %s", body)
	}
	if strings.Contains(body, "proxy-secret-pass") {
		t.Fatalf("status leaked proxy: %s", body)
	}
	var envelope struct {
		Data codexrouting.Status `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !envelope.Data.Enabled || !envelope.Data.ProbeProxyConfigured {
		t.Fatalf("status flags = %#v", envelope.Data)
	}
	_ = time.Now()
}

func TestCodexTokenReadsStoredCredential(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	stage := mustImportSubscriptionStage(t, fixture, "account-cookie-lab", "lab@localhost")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("cookie-lab"), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-6-astra"}}},
		StagedCredentialIDs: []string{stage.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	var credentialID uint
	for _, account := range fixture.service.CodexAccounts() {
		if account.GroupID == created.GroupID {
			credentialID = account.CredentialID
			break
		}
	}
	if credentialID == 0 {
		t.Fatalf("no Codex account for group %d", created.GroupID)
	}
	access, accountID, err := fixture.service.CodexToken(t.Context(), credentialID)
	if err != nil {
		t.Fatalf("CodexToken() error = %v", err)
	}
	if access == "" || accountID != "account-cookie-lab" {
		t.Fatalf("token/account = %q %q", access, accountID)
	}
}

func TestCodexRoutingClearRequiresConfirm(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	store := codexrouting.NewStore("", nil, codexrouting.Config{Enabled: true, EventLimit: 4})
	fixture.service.SetCodexRouting(store, nil)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	request := httptest.NewRequest(http.MethodPost, "/api/codex-routing/clear", strings.NewReader(`{"credential_id":1,"confirm":false}`))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	setRequiredTestIdempotencyHeader(request)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("clear without confirm = %d %s", recorder.Code, recorder.Body.String())
	}
}
