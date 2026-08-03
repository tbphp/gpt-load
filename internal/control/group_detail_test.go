package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestGetGroupSummaryUsesCollectionServiceStatusAndOnlyReturnsHeaderCounts(t *testing.T) {
	fixture := newServiceFixture(t)
	available := createGroupCollectionGroup(t, fixture, "summary-available", true, nil)
	unavailable := createGroupCollectionGroup(t, fixture, "summary-unavailable", true, nil)
	disabled := createGroupCollectionGroup(t, fixture, "summary-disabled", false, nil)
	setGroupCollectionRoute(t, fixture, available, `["openai-responses"]`, `[]`)
	setGroupCollectionRoute(t, fixture, unavailable, `["openai-completions"]`, `[]`)
	setGroupCollectionRoute(t, fixture, disabled, `["openai-responses"]`, `[]`)
	publishGroupCollectionRuntime(t, fixture, []state.KeyEntry{
		createGroupCollectionKey(t, fixture, available.ID, models.UpstreamKeyStatusActive, nil),
		createGroupCollectionKey(t, fixture, unavailable.ID, models.UpstreamKeyStatusActive, nil),
		createGroupCollectionKey(t, fixture, disabled.ID, models.UpstreamKeyStatusActive, nil),
	})

	for _, test := range []struct {
		name       string
		groupID    uint
		wantStatus GroupCollectionStatus
		wantKeys   int64
	}{
		{name: "available", groupID: available.ID, wantStatus: GroupCollectionStatusAvailable, wantKeys: 1},
		{name: "unavailable", groupID: unavailable.ID, wantStatus: GroupCollectionStatusUnavailable, wantKeys: 1},
		{name: "disabled", groupID: disabled.ID, wantStatus: GroupCollectionStatusDisabled, wantKeys: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := fixture.service.GetGroupSummary(t.Context(), test.groupID)
			if err != nil {
				t.Fatalf("GetGroupSummary() error = %v", err)
			}
			if got.ID != test.groupID || got.ServiceStatus != test.wantStatus || got.KeyCount != test.wantKeys {
				t.Fatalf("GetGroupSummary() = %#v", got)
			}

			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			wantFields := map[string]struct{}{
				"id": {}, "name": {}, "provider_id": {}, "service_status": {}, "upstream_url": {},
				"protocols": {}, "key_count": {}, "model_count": {},
			}
			for name := range fields {
				if _, exists := wantFields[name]; !exists {
					t.Fatalf("summary exposes unexpected field %q: %s", name, encoded)
				}
			}
			for _, forbidden := range []string{
				"models", "config", "effective_config", "enabled", "weight_manual", "validation_model",
			} {
				if containsJSONToken(encoded, forbidden) {
					t.Fatalf("summary exposes %q: %s", forbidden, encoded)
				}
			}
		})
	}
}

func TestGroupDetailStatusRequiresAHealthyKeyAndRouteCapability(t *testing.T) {
	fixture := newServiceFixture(t)
	group := createGroupCollectionGroup(t, fixture, "detail-zero-model-completions", true, nil)
	setGroupCollectionRoute(t, fixture, group, `["openai-completions"]`, `[]`)
	publishGroupCollectionRuntime(t, fixture, []state.KeyEntry{
		createGroupCollectionKey(t, fixture, group.ID, models.UpstreamKeyStatusActive, nil),
	})

	got, err := fixture.service.GetGroupSummary(t.Context(), group.ID)
	if err != nil {
		t.Fatalf("GetGroupSummary() error = %v", err)
	}
	if got.ServiceStatus != GroupCollectionStatusUnavailable || got.KeyCount != 1 || got.ModelCount != 0 {
		t.Fatalf("GetGroupSummary() = %#v, want unavailable with one key and zero models", got)
	}
}

func TestGetGroupHTTPContractAndAuthentication(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	group := validControlGroup("detail-http")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.Publish(mustBuildCompileInput(t, fixture.db)); err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	for _, test := range []struct {
		name       string
		path       string
		auth       string
		wantStatus int
		wantCode   any
	}{
		{name: "success", path: fmt.Sprintf("/api/groups/%d", group.ID), auth: "Bearer test-auth-key", wantStatus: http.StatusOK, wantCode: float64(0)},
		{name: "missing", path: "/api/groups/9999", auth: "Bearer test-auth-key", wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND"},
		{name: "zero id", path: "/api/groups/0", auth: "Bearer test-auth-key", wantStatus: http.StatusBadRequest, wantCode: "BAD_REQUEST"},
		{name: "overflow id", path: "/api/groups/184467440737095516160", auth: "Bearer test-auth-key", wantStatus: http.StatusBadRequest, wantCode: "BAD_REQUEST"},
		{name: "unauthorized", path: fmt.Sprintf("/api/groups/%d", group.ID), wantStatus: http.StatusUnauthorized, wantCode: "UNAUTHORIZED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			if test.auth != "" {
				request.Header.Set("Authorization", test.auth)
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("response = %d %s, want %d", recorder.Code, recorder.Body.String(), test.wantStatus)
			}
			var envelope map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope["code"] != test.wantCode {
				t.Fatalf("code = %#v, want %#v", envelope["code"], test.wantCode)
			}
			if test.name == "success" {
				data, ok := envelope["data"].(map[string]any)
				if !ok {
					t.Fatalf("summary data = %#v", envelope["data"])
				}
				for _, name := range []string{"models", "config", "effective_config", "enabled", "weight_manual", "validation_model"} {
					if _, exists := data[name]; exists {
						t.Fatalf("summary HTTP response exposes %q: %#v", name, data)
					}
				}
			}
		})
	}
}

func containsJSONToken(document []byte, token string) bool {
	return bytes.Contains(document, []byte(`"`+token+`"`))
}
