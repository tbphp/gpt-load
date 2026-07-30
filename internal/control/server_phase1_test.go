package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage/models"
)

func TestCreateAndImportEndpointsRequireCanonicalIdempotencyKeyBeforeMutation(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "seed-idempotency-header")
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	tests := []struct {
		name string
		path string
		body string
	}{
		{
			name: "AccessKey create",
			path: "/api/access-keys",
			body: `{"name":"client"}`,
		},
		{
			name: "Group create",
			path: "/api/groups",
			body: `{"upstream_url":"https://header.example.com","protocols":["openai-chat-completions"],"keys":"K"}`,
		},
		{
			name: "Group key import",
			path: "/api/groups/" + strconv.FormatUint(uint64(groupID), 10) + "/keys/import",
			body: `{"keys":"K"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := countCreateImportRows(t, fixture)
			for _, header := range []struct {
				value      string
				wantStatus int
				wantCode   string
			}{
				{wantStatus: http.StatusPreconditionRequired, wantCode: "IDEMPOTENCY_KEY_REQUIRED"},
				{
					value:      "not-a-canonical-uuid",
					wantStatus: http.StatusBadRequest,
					wantCode:   "INVALID_IDEMPOTENCY_KEY",
				},
			} {
				request := httptest.NewRequest(
					http.MethodPost,
					test.path,
					strings.NewReader(test.body),
				)
				request.Header.Set("Authorization", "Bearer test-auth-key")
				if header.value != "" {
					request.Header.Set("Idempotency-Key", header.value)
				}
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, request)
				if recorder.Code != header.wantStatus ||
					!strings.Contains(recorder.Body.String(), header.wantCode) {
					t.Fatalf(
						"POST %s = %d %s, want %d/%s",
						test.path,
						recorder.Code,
						recorder.Body.String(),
						header.wantStatus,
						header.wantCode,
					)
				}
				if after := countCreateImportRows(t, fixture); after != before {
					t.Fatalf("rows changed from %#v to %#v", before, after)
				}
			}
		})
	}
}

func TestAccessKeyCreateReplayOptionsAndRevealWireContracts(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(make([]byte, 16))
	fixture.service.operationRandom = bytes.NewReader(bytes.Repeat([]byte{0x73}, 16))
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	const idempotencyKey = "318f47a2-9c35-4d6e-8b1a-1234567890ab"

	create := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/access-keys",
			strings.NewReader(`{"name":"wire-client"}`),
		)
		request.Header.Set("Authorization", "Bearer test-auth-key")
		request.Header.Set("Idempotency-Key", idempotencyKey)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		return recorder
	}
	first := create()
	if first.Code != http.StatusOK ||
		first.Header().Get("Cache-Control") != "no-store" ||
		first.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("first create = %d headers=%v body=%s", first.Code, first.Header(), first.Body)
	}
	var firstEnvelope struct {
		Data AccessKeyCreateResult `json:"data"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstEnvelope); err != nil {
		t.Fatalf("decode first create: %v", err)
	}
	if firstEnvelope.Data.Key == "" || firstEnvelope.Data.Replayed {
		t.Fatalf("first create data = %#v", firstEnvelope.Data)
	}

	replay := create()
	if replay.Code != http.StatusOK ||
		strings.Contains(replay.Body.String(), firstEnvelope.Data.Key) {
		t.Fatalf("replay create = %d %s", replay.Code, replay.Body)
	}
	var replayEnvelope struct {
		Data AccessKeyCreateResult `json:"data"`
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &replayEnvelope); err != nil {
		t.Fatalf("decode replay create: %v", err)
	}
	if replayEnvelope.Data.Key != "" || !replayEnvelope.Data.Replayed ||
		replayEnvelope.Data.ID != firstEnvelope.Data.ID {
		t.Fatalf("replay data = %#v", replayEnvelope.Data)
	}

	optionsRequest := httptest.NewRequest(http.MethodGet, "/api/access-keys/options", nil)
	optionsRequest.Header.Set("Authorization", "Bearer test-auth-key")
	options := httptest.NewRecorder()
	engine.ServeHTTP(options, optionsRequest)
	if options.Code != http.StatusOK ||
		strings.Contains(options.Body.String(), "masked_key") ||
		strings.Contains(options.Body.String(), `"key"`) {
		t.Fatalf("options = %d %s", options.Code, options.Body)
	}

	revealRequest := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/access-keys/%d/reveal", firstEnvelope.Data.ID),
		nil,
	)
	revealRequest.Header.Set("Authorization", "Bearer test-auth-key")
	reveal := httptest.NewRecorder()
	engine.ServeHTTP(reveal, revealRequest)
	if reveal.Code != http.StatusOK ||
		reveal.Header().Get("Cache-Control") != "no-store" ||
		reveal.Header().Get("Pragma") != "no-cache" ||
		!strings.Contains(reveal.Body.String(), firstEnvelope.Data.Key) {
		t.Fatalf("reveal = %d headers=%v body=%s", reveal.Code, reveal.Header(), reveal.Body)
	}
}

type createImportRowCounts struct {
	groups     int64
	upstream   int64
	access     int64
	operations int64
}

func countCreateImportRows(
	t *testing.T,
	fixture serviceFixture,
) createImportRowCounts {
	t.Helper()
	var result createImportRowCounts
	for model, target := range map[any]*int64{
		&models.Group{}:            &result.groups,
		&models.UpstreamKey{}:      &result.upstream,
		&models.AccessKey{}:        &result.access,
		&models.ControlOperation{}: &result.operations,
	} {
		if err := fixture.db.Model(model).Count(target).Error; err != nil {
			t.Fatalf("count %T: %v", model, err)
		}
	}
	return result
}
