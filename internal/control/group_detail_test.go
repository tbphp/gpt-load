package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/storage/models"
)

func TestGetGroupReturnsCompletePersistedConfiguration(t *testing.T) {
	fixture := newServiceFixture(t)
	validationModel := "claude-haiku-3-5"
	weight := 70
	group := validControlGroup("detail")
	group.Protocols = models.JSON(`["anthropic"]`)
	group.Models = models.JSON(`[{"id":"claude-sonnet","alias":"coding"}]`)
	group.ValidationModel = &validationModel
	group.WeightManual = &weight
	group.Config = models.JSON(`{"first_byte_timeout":180,"header_rules":{"set":{"X-Provider-Key":"${API_KEY}"},"remove":[]}}`)
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	for _, key := range []models.UpstreamKey{
		{GroupID: group.ID, KeyValue: "cipher-one", KeyHash: "hash-one", Status: models.UpstreamKeyStatusActive},
		{GroupID: group.ID, KeyValue: "cipher-two", KeyHash: "hash-two", Status: models.UpstreamKeyStatusDisabled},
	} {
		key := key
		if err := fixture.db.Create(&key).Error; err != nil {
			t.Fatal(err)
		}
	}

	got, err := fixture.service.GetGroup(context.Background(), group.ID)
	if err != nil {
		t.Fatalf("GetGroup() error = %v", err)
	}
	if got.ID != group.ID || got.Name != "detail" || got.UpstreamURL != group.UpstreamURL ||
		!got.Enabled || got.KeyCount != 2 {
		t.Fatalf("GetGroup() = %#v", got)
	}
	if !reflect.DeepEqual(got.Protocols, []protocol.Protocol{protocol.Anthropic}) ||
		!reflect.DeepEqual(got.Models, []GroupModel{{ID: "claude-sonnet", Alias: "coding"}}) {
		t.Fatalf("protocols/models = %#v/%#v", got.Protocols, got.Models)
	}
	if got.ValidationModel == nil || *got.ValidationModel != validationModel ||
		got.WeightManual == nil || *got.WeightManual != weight {
		t.Fatalf("optional fields = %#v/%#v", got.ValidationModel, got.WeightManual)
	}
	if got.Config["first_byte_timeout"] == nil || got.Config["header_rules"] == nil {
		t.Fatalf("config = %#v", got.Config)
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"convert_enabled", "compiled", "timeouts", "key_value", "key_hash"} {
		if json.Valid(encoded) && string(encoded) != "" && containsJSONToken(encoded, forbidden) {
			t.Fatalf("detail exposes forbidden field %q: %s", forbidden, encoded)
		}
	}
}

func TestGetGroupUsesNonNilEmptyCollectionsAndNullPointers(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("empty-detail")
	group.Protocols = models.JSON(`[]`)
	group.Models = models.JSON(`[]`)
	group.Config = models.JSON(`{}`)
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}

	got, err := fixture.service.GetGroup(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Protocols == nil || got.Models == nil || got.Config == nil {
		t.Fatalf("nil collections: %#v", got)
	}
	if got.ValidationModel != nil || got.WeightManual != nil || got.KeyCount != 0 {
		t.Fatalf("null/count fields = %#v", got)
	}
}

func TestGetGroupNotFoundAndPersistedJSONFailure(t *testing.T) {
	fixture := newServiceFixture(t)
	if _, err := fixture.service.GetGroup(t.Context(), 404); !errors.Is(err, app_errors.ErrResourceNotFound) {
		t.Fatalf("missing GetGroup() error = %v", err)
	}

	group := validControlGroup("corrupt-detail")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec("UPDATE groups SET protocols = ? WHERE id = ?", `{`, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.GetGroup(t.Context(), group.ID); err == nil ||
		errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("corrupt GetGroup() error = %v, want internal non-validation error", err)
	}
}

func containsJSONToken(document []byte, token string) bool {
	return bytes.Contains(document, []byte(`"`+token+`"`))
}

func TestGetGroupHTTPContractAndAuthentication(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	group := validControlGroup("detail-http")
	if err := fixture.db.Create(group).Error; err != nil {
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
		})
	}
}
