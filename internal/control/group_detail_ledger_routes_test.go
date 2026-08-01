package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage/models"
)

func TestGroupDetailLedgerRoutesReplaceLegacyContracts(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	mustEnsureInitialPrices(t, fixture)
	groupID := createGroupForKeyImport(t, fixture, "sk-ledger-route")
	var key models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", groupID).Take(&key).Error; err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	legacy := serveGroupDetailLedgerRoute(
		t,
		engine,
		http.MethodPut,
		fmt.Sprintf("/api/groups/%d", groupID),
		`{"name":"retired"}`,
		"Bearer test-auth-key",
	)
	assertGroupDetailLedgerEnvelope(t, legacy, http.StatusNotFound, "ROUTE_NOT_FOUND")

	canonical := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "summary", method: http.MethodGet, path: fmt.Sprintf("/api/groups/%d", groupID)},
		{name: "models get", method: http.MethodGet, path: fmt.Sprintf("/api/groups/%d/models", groupID)},
		{name: "models put", method: http.MethodPut, path: fmt.Sprintf("/api/groups/%d/models", groupID), body: `{}`},
		{name: "settings get", method: http.MethodGet, path: fmt.Sprintf("/api/groups/%d/settings", groupID)},
		{name: "settings put", method: http.MethodPut, path: fmt.Sprintf("/api/groups/%d/settings", groupID), body: `{}`},
		{name: "keys collection", method: http.MethodGet, path: fmt.Sprintf("/api/groups/%d/keys", groupID)},
		{name: "key update", method: http.MethodPut, path: fmt.Sprintf("/api/groups/%d/keys/%d", groupID, key.ID), body: `{}`},
		{name: "key restore", method: http.MethodPost, path: fmt.Sprintf("/api/groups/%d/keys/%d/restore", groupID, key.ID), body: `{}`},
		{name: "key batch", method: http.MethodPost, path: fmt.Sprintf("/api/groups/%d/keys/batch", groupID), body: `{}`},
	}
	for _, route := range canonical {
		t.Run(route.name+" requires authentication", func(t *testing.T) {
			recorder := serveGroupDetailLedgerRoute(t, engine, route.method, route.path, route.body, "")
			assertGroupDetailLedgerEnvelope(t, recorder, http.StatusUnauthorized, "UNAUTHORIZED")
		})
		t.Run(route.name+" uses standard envelope", func(t *testing.T) {
			recorder := serveGroupDetailLedgerRoute(
				t,
				engine,
				route.method,
				route.path,
				route.body,
				"Bearer test-auth-key",
			)
			if recorder.Code < http.StatusOK || recorder.Code >= http.StatusInternalServerError {
				t.Fatalf("response = %d %s, want standard non-5xx envelope", recorder.Code, recorder.Body.String())
			}
			assertGroupDetailLedgerEnvelope(t, recorder, recorder.Code, "")
		})
	}

	updated := serveGroupDetailLedgerRoute(
		t,
		engine,
		http.MethodPut,
		fmt.Sprintf("/api/groups/%d/keys/%d", groupID, key.ID),
		`{"weight_manual":25}`,
		"Bearer test-auth-key",
	)
	assertGroupDetailLedgerEnvelope(t, updated, http.StatusOK, "")
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(updated.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"id", "mask", "configured_status", "effective_status", "weight_mode", "weight",
		"recent_success_count", "recent_failure_count", "consecutive_failure_count",
		"last_failure_category", "last_status_code", "cooldown_until_ms", "recovery",
	} {
		if _, exists := envelope.Data[field]; !exists {
			t.Fatalf("updated key missing ledger field %q: %s", field, updated.Body.String())
		}
	}
	for _, field := range []string{
		"group_id", "status", "weight_manual", "weight_auto", "blacklisted", "failure_count",
	} {
		if _, exists := envelope.Data[field]; exists {
			t.Fatalf("updated key exposes legacy field %q: %s", field, updated.Body.String())
		}
	}

	zeroWeight := serveGroupDetailLedgerRoute(
		t,
		engine,
		http.MethodPut,
		fmt.Sprintf("/api/groups/%d/keys/%d", groupID, key.ID),
		`{"weight_manual":0}`,
		"Bearer test-auth-key",
	)
	assertGroupDetailLedgerEnvelope(t, zeroWeight, http.StatusBadRequest, "VALIDATION_FAILED")
}

func serveGroupDetailLedgerRoute(
	t *testing.T,
	engine *gin.Engine,
	method string,
	path string,
	body string,
	auth string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if auth != "" {
		request.Header.Set("Authorization", auth)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func assertGroupDetailLedgerEnvelope(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	wantStatus int,
	wantCode string,
) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("response = %d %s, want %d", recorder.Code, recorder.Body.String(), wantStatus)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v; body=%s", err, recorder.Body.String())
	}
	if _, exists := envelope["code"]; !exists {
		t.Fatalf("envelope missing code: %s", recorder.Body.String())
	}
	if _, exists := envelope["message"]; !exists {
		t.Fatalf("envelope missing message: %s", recorder.Body.String())
	}
	if wantCode == "" {
		return
	}
	var code string
	if err := json.Unmarshal(envelope["code"], &code); err != nil || code != wantCode {
		t.Fatalf("code = %s, want %q", envelope["code"], wantCode)
	}
}
