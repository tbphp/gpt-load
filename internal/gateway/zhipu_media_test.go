package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/media"
	"gpt-load/internal/mediastore"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/testutil/encryptiontest"
	"gpt-load/internal/testutil/sqlitetest"
)

func newZhipuGatewayFixture(t *testing.T) (*gin.Engine, *Handler, *mediastore.Store, *state.Manager, *state.CredentialRegistry, *scriptedForwarder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	keyService := encryptiontest.Service(t, "zhipu-media-test-master-key")
	manager := state.NewManager()
	forwarder := &scriptedForwarder{}
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ConnectionType: "api_key", ID: 1, Name: "zhipu-media", ChannelID: channel.ZhipuAI,
			Params:  json.RawMessage(`{}`),
			Models:  []state.ModelConfig{{ID: "CogView-3-Flash"}, {ID: "CogVideoX-Flash"}},
			Enabled: true,
		}},
		Credentials: []state.CredentialConfig{
			{ID: 1, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1"},
			{ID: 2, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 2, Fingerprint: "credential-2"},
			{ID: 3, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 3, Fingerprint: "credential-3"},
		},
		AccessKeys: []state.AccessKeyConfig{
			{
				ID: 1, Name: "zhipu-client", KeyHash: keyService.Hash("gl-zhipu"), Status: state.AccessKeyStatusActive,
				Filters: state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.ZhipuMedia: {}}},
			},
			{
				ID: 2, Name: "image-client", KeyHash: keyService.Hash("gl-images"), Status: state.AccessKeyStatusActive,
				Filters: state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.OpenAIImages: {}}},
			},
		},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	registry := state.NewCredentialRegistry()
	entries := make([]state.CredentialEntry, 0, 3)
	for index, secret := range []string{"key-one", "key-two", "key-three"} {
		encrypted, err := keyService.Encrypt(`{"api_key":"` + secret + `"}`)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, state.CredentialEntry{
			ID: uint(index + 1), GroupID: 1, Version: 1, IdentityGeneration: uint64(index + 1),
			Fingerprint: "credential-" + string(rune('1'+index)), Status: state.CredentialStatusActive,
			EncryptedValue: encrypted,
		})
	}
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatal(err)
	}
	store := mediastore.NewStore(sqlitetest.OpenMigrated(t))
	handler := NewHandler(
		manager, registry, keyService, forwarder, dialect.NewSet(dialect.NewOpenAIImages(), dialect.NewZhipuMedia()),
		health.NewStatsStore(), health.NewMutationCoordinator(), nil, nil, nil,
	)
	handler.ConfigureMediaTasks(store)
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	return engine, handler, store, manager, registry, forwarder
}

func zhipuJSON(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	return body
}

func zhipuAsyncCreateResponse(id string) UpstreamResult {
	return UpstreamResult{
		StatusCode: http.StatusOK, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   []byte(`{"id":"` + id + `","request_id":"req_` + id + `","model":"CogView-3-Flash","task_status":"PROCESSING"}`),
	}
}

func TestZhipuSyncImageUsesModelRemap(t *testing.T) {
	engine, _, _, _, _, forwarder := newZhipuGatewayFixture(t)
	forwarder.results = []UpstreamResult{{
		StatusCode: http.StatusOK, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   []byte(`{"created":123,"data":[{"url":"https://cdn.example/cogview.png"}]}`),
	}}
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"cogview-3-flash","prompt":"fox"}`))
	request.Header.Set("Authorization", "Bearer gl-zhipu")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	body := zhipuJSON(t, recorder)
	data, _ := body["data"].([]any)
	if len(data) != 1 || data[0].(map[string]any)["url"] != "https://cdn.example/cogview.png" {
		t.Fatalf("body = %#v", body)
	}
	if len(forwarder.inputs) != 1 || forwarder.inputs[0].Request.Path != "/v1/images/generations" {
		t.Fatalf("inputs = %#v", forwarder.inputs)
	}
	if !strings.Contains(string(forwarder.inputs[0].Request.Body), `"model":"CogView-3-Flash"`) {
		t.Fatalf("upstream body = %s", forwarder.inputs[0].Request.Body)
	}
}

func TestZhipuAsyncImageCreateRetrieveAndPinning(t *testing.T) {
	engine, _, store, _, registry, forwarder := newZhipuGatewayFixture(t)
	forwarder.results = []UpstreamResult{zhipuAsyncCreateResponse("up_async_1")}
	request := httptest.NewRequest(http.MethodPost, "/v1/async/images/generations", strings.NewReader(`{"model":"CogView-3-Flash","prompt":"fox"}`))
	request.Header.Set("Authorization", "Bearer gl-zhipu")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("create status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	created := zhipuJSON(t, recorder)
	id, _ := created["id"].(string)
	if !strings.HasPrefix(id, "zhipu_") || created["task_status"] != "in_progress" {
		t.Fatalf("create body = %#v", created)
	}
	task, err := store.Get(request.Context(), 1, id)
	if err != nil {
		t.Fatal(err)
	}
	if task.Kind != media.KindImage || task.Operation != media.OperationZhipuImagesCreateAsync || task.UpstreamTaskID != "up_async_1" {
		t.Fatalf("binding = %#v", task)
	}
	bound := task.CredentialID

	forwarder.results = append(forwarder.results, UpstreamResult{
		StatusCode: http.StatusOK, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   []byte(`{"task_status":"SUCCESS","image_result":[{"url":"https://cdn.example/img.png"}]}`),
	})
	request = httptest.NewRequest(http.MethodGet, "/v1/async-result/"+id, nil)
	request.Header.Set("Authorization", "Bearer gl-zhipu")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("retrieve status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	retrieved := zhipuJSON(t, recorder)
	if retrieved["task_status"] != "completed" {
		t.Fatalf("retrieve body = %#v", retrieved)
	}
	if _, ok := retrieved["image_result"].([]any); !ok {
		t.Fatalf("image_result missing: %#v", retrieved)
	}
	if _, ok := retrieved["data"].([]any); !ok {
		t.Fatalf("data mirror missing: %#v", retrieved)
	}
	if forwarder.inputs[1].Credential.ID != bound || forwarder.inputs[1].Request.Path != "/v1/async-result/up_async_1" {
		t.Fatalf("retrieve input = %#v", forwarder.inputs[1])
	}
	if _, changed := registry.SetCooldownWithChange(bound, time.Now().Add(time.Hour)); !changed {
		t.Fatal("credential cooldown was not applied")
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/async-result/"+id, nil)
	request.Header.Set("Authorization", "Bearer gl-zhipu")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("cooldown status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if zhipuJSON(t, recorder)["task_id"] != id {
		t.Fatalf("cooldown body = %s", recorder.Body.String())
	}
}

func TestZhipuVideoCreateRetrieve(t *testing.T) {
	engine, _, store, _, _, forwarder := newZhipuGatewayFixture(t)
	forwarder.results = []UpstreamResult{{
		StatusCode: http.StatusOK, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   []byte(`{"id":"up_video_1","request_id":"req_v","model":"CogVideoX-Flash","task_status":"PROCESSING"}`),
	}}
	request := httptest.NewRequest(http.MethodPost, "/v1/videos/generations", strings.NewReader(`{"model":"CogVideoX-Flash","prompt":"fox","size":"1920x1080","fps":30,"duration":5,"with_audio":true}`))
	request.Header.Set("Authorization", "Bearer gl-zhipu")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("create status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	id := zhipuJSON(t, recorder)["id"].(string)
	task, err := store.Get(request.Context(), 1, id)
	if err != nil || task.Kind != media.KindVideo || task.Duration != 5 || !task.WithAudio {
		t.Fatalf("binding = %#v err = %v", task, err)
	}
	forwarder.results = append(forwarder.results, UpstreamResult{
		StatusCode: http.StatusOK, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   []byte(`{"task_status":"SUCCESS","video_result":[{"url":"https://cdn.example/video.mp4"}]}`),
	})
	request = httptest.NewRequest(http.MethodGet, "/v1/async-result/"+id, nil)
	request.Header.Set("Authorization", "Bearer gl-zhipu")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	body := zhipuJSON(t, recorder)
	if body["task_status"] != "completed" {
		t.Fatalf("retrieve body = %#v", body)
	}
	results, _ := body["video_result"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["url"] != "https://cdn.example/video.mp4" {
		t.Fatalf("video_result = %#v", body)
	}
}

func TestZhipuValidationAndProtocolAuthorization(t *testing.T) {
	engine, _, _, _, _, forwarder := newZhipuGatewayFixture(t)
	cases := []struct {
		path string
		body string
	}{
		{"/v1/async/images/generations", `{"model":"CogView-3-Flash","prompt":"fox","size":"123x456"}`},
		{"/v1/videos/generations", `{"model":"CogVideoX-Flash","prompt":"fox","fps":45}`},
		{"/v1/videos/generations", `{"model":"CogVideoX-Flash","prompt":"fox","duration":15}`},
	}
	for _, test := range cases {
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
		request.Header.Set("Authorization", "Bearer gl-zhipu")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d response = %s", test.body, recorder.Code, recorder.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"CogView-3-Flash","prompt":"fox"}`))
	request.Header.Set("Authorization", "Bearer gl-images")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("protocol status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if zhipuJSON(t, recorder)["error"].(map[string]any)["code"] != "protocol_not_allowed" {
		t.Fatalf("protocol body = %s", recorder.Body.String())
	}
	if len(forwarder.inputs) != 0 {
		t.Fatalf("validation reached upstream %d times", len(forwarder.inputs))
	}
}

func TestZhipuAsyncCreateRotatesAndIsIdempotent(t *testing.T) {
	engine, _, store, _, _, forwarder := newZhipuGatewayFixture(t)
	forwarder.results = []UpstreamResult{
		{StatusCode: http.StatusTooManyRequests, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, Body: []byte(`{}`)},
		zhipuAsyncCreateResponse("up_async_rotated"),
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/async/images/generations", strings.NewReader(`{"model":"CogView-3-Flash","prompt":"fox"}`))
	request.Header.Set("Authorization", "Bearer gl-zhipu")
	request.Header.Set("Idempotency-Key", "zhipu-idem")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || len(forwarder.inputs) != 2 {
		t.Fatalf("status = %d attempts = %d body = %s", recorder.Code, len(forwarder.inputs), recorder.Body.String())
	}
	first := zhipuJSON(t, recorder)
	task, err := store.Get(request.Context(), 1, first["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if task.CredentialID != forwarder.inputs[1].Credential.ID {
		t.Fatalf("binding credential = %d", task.CredentialID)
	}
	request = httptest.NewRequest(http.MethodPost, "/v1/async/images/generations", strings.NewReader(`{"model":"CogView-3-Flash","prompt":"fox"}`))
	request.Header.Set("Authorization", "Bearer gl-zhipu")
	request.Header.Set("Idempotency-Key", "zhipu-idem")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || zhipuJSON(t, recorder)["id"] != first["id"] {
		t.Fatalf("idempotent replay = %s", recorder.Body.String())
	}
	if len(forwarder.inputs) != 2 {
		t.Fatalf("idempotent upstream calls = %d", len(forwarder.inputs))
	}
}

func TestZhipuCancelUnsupported(t *testing.T) {
	engine, _, _, _, _, forwarder := newZhipuGatewayFixture(t)
	forwarder.results = []UpstreamResult{zhipuAsyncCreateResponse("up_cancel")}
	request := httptest.NewRequest(http.MethodPost, "/v1/async/images/generations", strings.NewReader(`{"model":"CogView-3-Flash","prompt":"fox"}`))
	request.Header.Set("Authorization", "Bearer gl-zhipu")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	id := zhipuJSON(t, recorder)["id"].(string)
	request = httptest.NewRequest(http.MethodDelete, "/v1/async-result/"+id, nil)
	request.Header.Set("Authorization", "Bearer gl-zhipu")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("cancel status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if zhipuJSON(t, recorder)["error"].(map[string]any)["code"] != "cancel_not_supported" {
		t.Fatalf("cancel body = %s", recorder.Body.String())
	}
	if len(forwarder.inputs) != 1 {
		t.Fatalf("unsupported cancel reached upstream: %d calls", len(forwarder.inputs))
	}
}

func TestZhipuModelListShowsMediaModels(t *testing.T) {
	engine, _, _, _, _, _ := newZhipuGatewayFixture(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer gl-zhipu")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("models status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, model := range []string{"CogView-3-Flash", "CogVideoX-Flash"} {
		if !strings.Contains(body, model) {
			t.Fatalf("model list missing %s: %s", model, body)
		}
	}
}

func TestZhipuCreateWithTaskIDNeverRetries(t *testing.T) {
	engine, _, _, _, _, forwarder := newZhipuGatewayFixture(t)
	forwarder.results = []UpstreamResult{
		{
			StatusCode: http.StatusTooManyRequests, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			Body: []byte(`{"id":"up_ambiguous","task_status":"PROCESSING","error":{"message":"retryable"}}`),
		},
		zhipuAsyncCreateResponse("up_should_not_run"),
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/async/images/generations", strings.NewReader(`{"model":"CogView-3-Flash","prompt":"fox"}`))
	request.Header.Set("Authorization", "Bearer gl-zhipu")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if len(forwarder.inputs) != 1 {
		t.Fatalf("attempts = %d, want 1", len(forwarder.inputs))
	}
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestZhipuSyncImageMissingURLReturns502(t *testing.T) {
	engine, _, _, _, _, forwarder := newZhipuGatewayFixture(t)
	forwarder.results = []UpstreamResult{{
		StatusCode: http.StatusOK, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   []byte(`{"data":[{"b64":"x"}]}`),
	}}
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"CogView-3-Flash","prompt":"fox"}`))
	request.Header.Set("Authorization", "Bearer gl-zhipu")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if zhipuJSON(t, recorder)["error"].(map[string]any)["code"] != "upstream_error" {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestZhipuVideoDoesNotInjectDefaults(t *testing.T) {
	engine, _, _, _, _, forwarder := newZhipuGatewayFixture(t)
	forwarder.results = []UpstreamResult{{
		StatusCode: http.StatusOK, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   []byte(`{"id":"up_minimal","request_id":"req_minimal","task_status":"PROCESSING"}`),
	}}
	request := httptest.NewRequest(http.MethodPost, "/v1/videos/generations", strings.NewReader(`{"model":"CogVideoX-Flash","prompt":"fox"}`))
	request.Header.Set("Authorization", "Bearer gl-zhipu")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if len(forwarder.inputs) != 1 {
		t.Fatalf("inputs = %d", len(forwarder.inputs))
	}
	body := string(forwarder.inputs[0].Request.Body)
	for _, field := range []string{`"size"`, `"fps"`, `"quality"`, `"duration"`, `"with_audio"`} {
		if strings.Contains(body, field) {
			t.Fatalf("server injected %s into minimal video body: %s", field, body)
		}
	}
}
