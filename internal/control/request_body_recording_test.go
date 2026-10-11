package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestAccessKeyRequestBodyRecordingHTTP(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := newAccessKeyLifecycleEngine(t, fixture)

	plain := serveAccessKeyLifecycleRequest(t, engine, http.MethodPost, "/api/access-keys", `{"name":"plain"}`, "00000000-0000-4000-8000-000000006491")
	if plain.Code != http.StatusOK {
		t.Fatalf("create plain key = %d %s", plain.Code, plain.Body.String())
	}
	assertJSONRawEqual(t, decodeAccessKeyLifecycleData(t, plain)["record_request_body"], "false")

	created := serveAccessKeyLifecycleRequest(t, engine, http.MethodPost, "/api/access-keys", `{"name":"recorded","record_request_body":true}`, "00000000-0000-4000-8000-000000006492")
	if created.Code != http.StatusOK {
		t.Fatalf("create recorded key = %d %s", created.Code, created.Body.String())
	}
	data := decodeAccessKeyLifecycleData(t, created)
	assertJSONRawEqual(t, data["record_request_body"], "true")
	var id uint
	if err := json.Unmarshal(data["id"], &id); err != nil {
		t.Fatal(err)
	}
	assertRequestBodyRecording(t, fixture, id, true)

	path := fmt.Sprintf("/api/access-keys/%d", id)
	for _, test := range []struct {
		body string
		want bool
	}{
		{`{"name":"renamed"}`, true},
		{`{"record_request_body":false}`, false},
		{`{"record_request_body":true}`, true},
	} {
		response := serveAccessKeyLifecycleRequest(t, engine, http.MethodPut, path, test.body, "")
		if response.Code != http.StatusOK {
			t.Fatalf("update %s = %d %s", test.body, response.Code, response.Body.String())
		}
		assertJSONRawEqual(t, decodeAccessKeyLifecycleData(t, response)["record_request_body"], fmt.Sprint(test.want))
		assertRequestBodyRecording(t, fixture, id, test.want)
	}

	rotated, err := fixture.service.RotateAccessKeyIdempotent(t.Context(), "00000000-0000-4000-8000-000000006493", id)
	if err != nil || !rotated.RecordRequestBody {
		t.Fatalf("rotation lost request body recording: %+v, %v", rotated.AccessKeyMetadata, err)
	}
	collection := serveAccessKeyLifecycleRequest(t, engine, http.MethodGet, "/api/modern/access-keys", "", "")
	var envelope struct {
		Data struct {
			Items []struct {
				ID                uint `json:"id"`
				RecordRequestBody bool `json:"record_request_body"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(collection.Body.Bytes(), &envelope); err != nil || collection.Code != http.StatusOK {
		t.Fatalf("collection = %d %s, error = %v", collection.Code, collection.Body.String(), err)
	}
	for _, item := range envelope.Data.Items {
		if item.RecordRequestBody != (item.ID == id) {
			t.Fatalf("collection item %d recording = %v", item.ID, item.RecordRequestBody)
		}
	}
}

func assertRequestBodyRecording(t *testing.T, fixture serviceFixture, id uint, want bool) {
	t.Helper()
	var row models.AccessKey
	if err := fixture.db.First(&row, id).Error; err != nil {
		t.Fatal(err)
	}
	if row.RecordRequestBody != want || fixture.manager.Current().AccessKeysByID[id].RecordRequestBody != want {
		t.Fatalf("stored recording = %v, published = %v, want %v",
			row.RecordRequestBody, fixture.manager.Current().AccessKeysByID[id].RecordRequestBody, want)
	}
}

func TestAuthSessionTellsAccessKeyUserAboutRequestBodyRecording(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	recorded, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "recorded", RecordRequestBody: true})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "plain"})
	if err != nil {
		t.Fatal(err)
	}
	engine := newAccessKeyLifecycleEngine(t, fixture)
	for _, test := range []struct {
		name string
		key  string
		want string
	}{
		{name: "recorded access key", key: recorded.Key, want: `{"retention_days":7}`},
		{name: "plain access key", key: plain.Key},
		{name: "admin", key: authTestKey},
	} {
		request := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
		request.Header.Set("Authorization", "Bearer "+test.key)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s session = %d %s", test.name, response.Code, response.Body.String())
		}
		data := decodeAccessKeyLifecycleData(t, response)
		if got := string(data["request_body_recording"]); got != test.want {
			t.Fatalf("%s request_body_recording = %s, want %s", test.name, got, test.want)
		}
	}
}

func TestRequestLogDetailReturnsRecordedRequestBody(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	owner, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "owner", RecordRequestBody: true})
	if err != nil {
		t.Fatal(err)
	}
	const requestID = "00000000-0000-4000-8000-000000006494"
	const plainID = "00000000-0000-4000-8000-000000006495"
	record := func(id string) requestlog.Record {
		return requestlog.Record{
			RequestID: id, CompletedAtMS: time.Date(2026, time.October, 10, 12, 0, 0, 0, time.UTC).UnixMilli(),
			AccessKey: requestlog.AccessKeyRef{ID: owner.ID, Name: &owner.Name},
			Protocol:  protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ClientModel: "gpt-4o",
			Status: telemetry.RequestStatusSuccess, StatusCode: http.StatusOK,
			UsageState: usage.StateNotApplicable, CostState: pricing.CostStateNotApplicable,
			PricingCompleteness: pricing.CompletenessNotApplicable, PricingMode: pricing.ModeStandard,
		}
	}
	stored := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	reader := &recordingRequestLogReader{
		pages:   []requestlog.Page{{Items: []requestlog.Record{record(requestID)}}, {Items: []requestlog.Record{record(plainID)}}},
		details: map[string]requestlog.Record{requestID: record(requestID), plainID: record(plainID)},
		bodies: map[string]*requestlog.RequestBody{requestID: {
			State: requestlog.RequestBodyTruncated, OriginalBytes: 300 << 10, Body: &stored,
		}},
	}
	fixture.service.requestLogs = reader
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	want := `{"state":"truncated","original_bytes":307200,"body":"{\n  \"model\": \"gpt-4o\",\n  \"messages\": [\n    {\n      \"role\": \"user\",\n      \"content\": \"hi\"\n    }\n  ]\n}"}`
	for _, test := range []struct {
		name, key, id, want string
	}{
		{name: "admin", key: "test-auth-key", id: requestID, want: want},
		{name: "owner", key: owner.Key, id: requestID, want: want},
		{name: "admin without body", key: "test-auth-key", id: plainID, want: "null"},
	} {
		request := httptest.NewRequest(http.MethodGet, "/api/logs/"+test.id, nil)
		request.Header.Set("Authorization", "Bearer "+test.key)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s detail = %d %s", test.name, response.Code, response.Body.String())
		}
		var envelope struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if got := string(envelope.Data["request_body"]); got != test.want {
			t.Fatalf("%s request_body = %s, want %s", test.name, got, test.want)
		}
	}
}

func TestRequestLogBodyFormattingStaysWithinBudget(t *testing.T) {
	// 深层嵌套的小请求缩进后按层数平方膨胀，超过预算时应返回存储原文。
	nested := strings.Repeat("[", 2000) + strings.Repeat("]", 2000)
	ordinary := `{"model":"gpt-4o","messages":[]}`
	for name, test := range map[string]struct{ stored, want string }{
		"deeply nested": {stored: nested, want: nested},
		"ordinary":      {stored: ordinary, want: "{\n  \"model\": \"gpt-4o\",\n  \"messages\": []\n}"},
	} {
		stored := test.stored
		response := mapRequestLogBody(&requestlog.RequestBody{
			State: requestlog.RequestBodyOriginal, OriginalBytes: int64(len(stored)), Body: &stored,
		})
		got := ""
		if response != nil && response.Body != nil {
			got = *response.Body
		}
		if got != test.want {
			t.Fatalf("%s formatted body = %d bytes, want %d", name, len(got), len(test.want))
		}
	}
}
