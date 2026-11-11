package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/testutil/encryptiontest"
	"gpt-load/internal/testutil/sqlitetest"
	"gpt-load/internal/video"
	"gpt-load/internal/videostore"
)

func newVideoGatewayFixture(t *testing.T) (*gin.Engine, *Handler, *videostore.Store, *state.Manager, *state.CredentialRegistry, *scriptedForwarder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	keyService := encryptiontest.Service(t, "video-handler-test-master-key")
	manager := state.NewManager()
	forwarder := &scriptedForwarder{}
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ConnectionType: "api_key", ID: 1, Name: "video", ChannelID: channel.OpenAI,
			Params:  json.RawMessage(`{}`),
			Models:  []state.ModelConfig{{ID: "agnes-video-2.5-flash"}, {ID: "sora-2"}},
			Enabled: true,
		}},
		Credentials: []state.CredentialConfig{
			{ID: 1, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1"},
			{ID: 2, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 2, Fingerprint: "credential-2"},
			{ID: 3, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 3, Fingerprint: "credential-3"},
			{ID: 4, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 4, Fingerprint: "credential-4"},
		},
		SystemSettings: config.Settings{state.SettingRetryCount: 4},
		AccessKeys: []state.AccessKeyConfig{
			{
				ID: 1, Name: "video-client", KeyHash: keyService.Hash("gl-video"), Status: state.AccessKeyStatusActive,
				Filters: state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.OpenAIVideos: {}}},
			},
			{
				ID: 2, Name: "text-client", KeyHash: keyService.Hash("gl-text"), Status: state.AccessKeyStatusActive,
				Filters: state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.OpenAICompletions: {}}},
			},
		},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	registry := state.NewCredentialRegistry()
	entries := make([]state.CredentialEntry, 0, 4)
	for index, secret := range []string{"key-one", "key-two", "key-three", "key-four"} {
		encrypted, err := keyService.Encrypt(`{"api_key":"` + secret + `"}`)
		if err != nil {
			t.Fatalf("Encrypt() error = %v", err)
		}
		entries = append(entries, state.CredentialEntry{
			ID: uint(index + 1), GroupID: 1, Version: 1, IdentityGeneration: uint64(index + 1),
			Fingerprint: "credential-" + string(rune('1'+index)), Status: state.CredentialStatusActive,
			EncryptedValue: encrypted,
		})
	}
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatalf("ReplaceCredentials() error = %v", err)
	}
	store := videostore.NewStore(sqlitetest.OpenMigrated(t))
	handler := NewHandler(
		manager, registry, keyService, forwarder, dialect.NewSet(dialect.NewOpenAIVideos()),
		health.NewStatsStore(), health.NewMutationCoordinator(), nil, nil, nil,
	)
	handler.ConfigureVideoTasks(store)
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	return engine, handler, store, manager, registry, forwarder
}

func videoJSONResponse(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	return body
}

func videoUpstreamSuccess(id string) UpstreamResult {
	return UpstreamResult{
		StatusCode: http.StatusOK, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   []byte(`{"id":"` + id + `","task_id":"` + id + `","status":"queued","created_at":1794405405}`),
	}
}

func videoError(code string) map[string]any {
	return map[string]any{"error": map[string]any{"code": code}}
}

func TestVideoCreateRetrieveAndPinning(t *testing.T) {
	engine, _, store, _, registry, forwarder := newVideoGatewayFixture(t)
	forwarder.results = []UpstreamResult{videoUpstreamSuccess("task_up_1")}

	request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(
		`{"model":"agnes-video-2.5-flash","prompt":"a fox running through snow","mode":"text","seconds":"5"}`,
	))
	request.Header.Set("Authorization", "Bearer gl-video")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("create status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	created := videoJSONResponse(t, recorder)
	id, _ := created["id"].(string)
	if !strings.HasPrefix(id, "video_") {
		t.Fatalf("gateway task id = %q", id)
	}
	if created["task_id"] != "task_up_1" || created["status"] != "queued" {
		t.Fatalf("create response = %#v", created)
	}
	task, err := store.Get(request.Context(), 1, id)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if task.GroupID != 1 || task.CredentialID != forwarder.inputs[0].Credential.ID ||
		task.UpstreamTaskID != "task_up_1" || task.UpstreamModel != "agnes-video-2.5-flash" {
		t.Fatalf("binding = %#v", task)
	}
	boundCredential := task.CredentialID
	if boundCredential == 0 {
		t.Fatal("binding did not record a credential")
	}
	if len(forwarder.inputs) != 1 {
		t.Fatalf("create forward calls = %d, want 1", len(forwarder.inputs))
	}

	forwarder.results = []UpstreamResult{
		videoUpstreamSuccess("task_up_1"),
		{
			StatusCode: http.StatusOK, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			Header: http.Header{"Content-Type": {"application/json"}},
			Body:   []byte(`{"id":"task_up_1","status":"processing","progress":42,"url":""}`),
		},
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/videos/"+id, nil)
	request.Header.Set("Authorization", "Bearer gl-video")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("retrieve status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	retrieved := videoJSONResponse(t, recorder)
	if retrieved["status"] != "in_progress" || retrieved["progress"].(float64) != 42 {
		t.Fatalf("retrieve response = %#v", retrieved)
	}
	if len(forwarder.inputs) != 2 {
		t.Fatalf("retrieve forward calls = %d", len(forwarder.inputs))
	}
	if forwarder.inputs[1].Credential.ID != boundCredential {
		t.Fatalf("retrieve credential = %d, want pinned %d", forwarder.inputs[1].Credential.ID, boundCredential)
	}
	if forwarder.inputs[1].Request.Path != "/v1/videos/task_up_1" {
		t.Fatalf("retrieve path = %q", forwarder.inputs[1].Request.Path)
	}

	if _, changed := registry.SetCooldownWithChange(boundCredential, time.Now().Add(time.Hour)); !changed {
		t.Fatal("credential cooldown was not applied")
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/videos/"+id, nil)
	request.Header.Set("Authorization", "Bearer gl-video")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("cooldown status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	cooldownBody := videoJSONResponse(t, recorder)
	if code := cooldownBody["error"].(map[string]any)["code"]; code != "task_credential_unavailable" {
		t.Fatalf("cooldown code = %v", code)
	}
	if cooldownBody["task_id"] != id {
		t.Fatalf("cooldown task_id = %v, want %q", cooldownBody["task_id"], id)
	}
	if len(forwarder.inputs) != 2 {
		t.Fatalf("pinned cooldown forwarded again: %d calls", len(forwarder.inputs))
	}
}

func TestVideoCreateRotatesRateLimitedCredential(t *testing.T) {
	engine, _, store, _, _, forwarder := newVideoGatewayFixture(t)
	forwarder.results = []UpstreamResult{
		{StatusCode: http.StatusTooManyRequests, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			Body: []byte(`{"error":{"message":"rate limited"}}`)},
		videoUpstreamSuccess("task_up_2"),
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(
		`{"model":"agnes-video-2.5-flash","prompt":"fox"}`,
	))
	request.Header.Set("Authorization", "Bearer gl-video")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("create status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if len(forwarder.inputs) != 2 {
		t.Fatalf("attempts = %d, want 2", len(forwarder.inputs))
	}
	created := videoJSONResponse(t, recorder)
	task, err := store.Get(request.Context(), 1, created["id"].(string))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if task.CredentialID != forwarder.inputs[1].Credential.ID || task.CredentialID == forwarder.inputs[0].Credential.ID {
		t.Fatalf("binding credential = %d, attempts = %d,%d", task.CredentialID,
			forwarder.inputs[0].Credential.ID, forwarder.inputs[1].Credential.ID)
	}
}

func TestVideoCreateRotatesTransportFailure(t *testing.T) {
	engine, _, _, _, _, forwarder := newVideoGatewayFixture(t)
	forwarder.results = []UpstreamResult{
		{Err: errors.New("dial tcp: connection refused"), DispatchState: execution.DispatchNotSent},
		videoUpstreamSuccess("task_up_transport"),
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"sora-2","prompt":"fox"}`))
	request.Header.Set("Authorization", "Bearer gl-video")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || len(forwarder.inputs) != 2 {
		t.Fatalf("status = %d attempts = %d body = %s", recorder.Code, len(forwarder.inputs), recorder.Body.String())
	}
}

func TestVideoCreateUnusual403CoolsAndRotates(t *testing.T) {
	engine, _, _, _, registry, forwarder := newVideoGatewayFixture(t)
	forwarder.results = []UpstreamResult{
		{StatusCode: http.StatusForbidden, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			Body: []byte(`{"error":{"message":"unusual activity detected"}}`)},
		videoUpstreamSuccess("task_up_3"),
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"sora-2","prompt":"fox"}`))
	request.Header.Set("Authorization", "Bearer gl-video")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || len(forwarder.inputs) != 2 {
		t.Fatalf("status = %d attempts = %d body = %s", recorder.Code, len(forwarder.inputs), recorder.Body.String())
	}
	cooled := forwarder.inputs[0].Credential.ID
	for _, candidate := range registry.CollectCredentialCandidates([]uint{1}, nil, time.Now()) {
		if candidate.ID == cooled {
			t.Fatalf("credential %d was not cooled after unusual activity", cooled)
		}
	}
}

func TestVideoCreateMissingTaskIDDoesNotRetry(t *testing.T) {
	engine, _, _, _, _, forwarder := newVideoGatewayFixture(t)
	forwarder.results = []UpstreamResult{
		{StatusCode: http.StatusBadGateway, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			Body: []byte(`{"task_id":"task_up_ambiguous","status":"queued"}`)},
		videoUpstreamSuccess("task_up_should_not_run"),
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"sora-2","prompt":"fox"}`))
	request.Header.Set("Authorization", "Bearer gl-video")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if len(forwarder.inputs) != 1 {
		t.Fatalf("attempts = %d, want 1", len(forwarder.inputs))
	}
	if code := videoJSONResponse(t, recorder)["error"].(map[string]any)["code"]; code != "upstream_error" {
		t.Fatalf("error code = %v", code)
	}
}

func TestVideoCreateRetriesServerErrorWithoutTaskID(t *testing.T) {
	engine, _, _, _, _, forwarder := newVideoGatewayFixture(t)
	forwarder.results = []UpstreamResult{
		{StatusCode: http.StatusInternalServerError, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, Body: []byte(`{}`)},
		videoUpstreamSuccess("task_up_4"),
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"sora-2","prompt":"fox"}`))
	request.Header.Set("Authorization", "Bearer gl-video")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || len(forwarder.inputs) != 2 {
		t.Fatalf("status = %d attempts = %d body = %s", recorder.Code, len(forwarder.inputs), recorder.Body.String())
	}
}

func TestVideoCreateValidationDoesNotReachUpstream(t *testing.T) {
	engine, _, _, _, _, forwarder := newVideoGatewayFixture(t)
	cases := []string{
		`{"model":"sora-2","prompt":"fox","mode":"keyframe"}`,
		`{"model":"sora-2","prompt":"fox","mode":"reference"}`,
		`{"model":"sora-2","prompt":"fox","seconds":"3"}`,
		`{"model":"sora-2","prompt":"fox","n":2}`,
	}
	for _, body := range cases {
		request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer gl-video")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d response = %s", body, recorder.Code, recorder.Body.String())
		}
	}
	if len(forwarder.inputs) != 0 {
		t.Fatalf("validation reached upstream %d times", len(forwarder.inputs))
	}
}

func TestVideoProtocolAuthorizationAndIdempotency(t *testing.T) {
	engine, _, _, _, _, forwarder := newVideoGatewayFixture(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"sora-2","prompt":"fox"}`))
	request.Header.Set("Authorization", "Bearer gl-text")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("unauthorized protocol status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if code := videoJSONResponse(t, recorder)["error"].(map[string]any)["code"]; code != "protocol_not_allowed" {
		t.Fatalf("unauthorized protocol code = %v", code)
	}

	forwarder.results = []UpstreamResult{videoUpstreamSuccess("task_up_5")}
	firstID := ""
	for i := 0; i < 2; i++ {
		request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"sora-2","prompt":"fox"}`))
		request.Header.Set("Authorization", "Bearer gl-video")
		request.Header.Set("Idempotency-Key", "same-key")
		recorder = httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("idempotent create %d status = %d body = %s", i, recorder.Code, recorder.Body.String())
		}
		id, _ := videoJSONResponse(t, recorder)["id"].(string)
		if i == 0 {
			firstID = id
		} else if id != firstID {
			t.Fatalf("idempotent id = %q, want %q", id, firstID)
		}
	}
	if len(forwarder.inputs) != 1 {
		t.Fatalf("idempotent upstream calls = %d, want 1", len(forwarder.inputs))
	}
}

func TestVideoTaskExpiryAndDelete(t *testing.T) {
	engine, _, store, _, _, forwarder := newVideoGatewayFixture(t)
	forwarder.results = []UpstreamResult{videoUpstreamSuccess("task_up_6")}
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"sora-2","prompt":"fox"}`))
	request.Header.Set("Authorization", "Bearer gl-video")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("create status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	id := videoJSONResponse(t, recorder)["id"].(string)

	forwarder.results = []UpstreamResult{
		videoUpstreamSuccess("task_up_6"),
		{
			StatusCode: http.StatusOK, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			Header: http.Header{"Content-Type": {"application/json"}},
			Body:   []byte(`{"id":"task_up_6","status":"completed","url":"https://cdn.example/video.mp4"}`),
		},
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/videos/"+id, nil)
	request.Header.Set("Authorization", "Bearer gl-video")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("retrieve status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if got := videoJSONResponse(t, recorder)["status"]; got != "completed" {
		t.Fatalf("completed status = %v", got)
	}

	forwarder.results = []UpstreamResult{
		videoUpstreamSuccess("task_up_6"),
		{
			StatusCode: http.StatusOK, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			Header: http.Header{"Content-Type": {"application/json"}},
			Body:   []byte(`{"id":"task_up_6","status":"completed","url":"https://cdn.example/video.mp4"}`),
		},
		{StatusCode: http.StatusNoContent, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true},
	}
	request = httptest.NewRequest(http.MethodDelete, "/v1/videos/"+id, nil)
	request.Header.Set("Authorization", "Bearer gl-video")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	task, err := store.Get(request.Context(), 1, id)
	if err != nil || task.Status != video.StatusCancelled {
		t.Fatalf("cancelled task = %#v err = %v", task, err)
	}

	store.SetClock(func() time.Time { return time.Now().Add(25 * time.Hour) })
	request = httptest.NewRequest(http.MethodGet, "/v1/videos/"+id, nil)
	request.Header.Set("Authorization", "Bearer gl-video")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expired status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if code := videoJSONResponse(t, recorder)["error"].(map[string]any)["code"]; code != "task_not_found" {
		t.Fatalf("expired code = %v", code)
	}
}

func TestVideoErrorEnvelopeKeepsOpenAIErrorShape(t *testing.T) {
	engine, _, _, _, _, _ := newVideoGatewayFixture(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader([]byte(`{"model":"missing","prompt":"fox"}`)))
	request.Header.Set("Authorization", "Bearer gl-video")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	envelope := videoJSONResponse(t, recorder)["error"].(map[string]any)
	if envelope["type"] != "invalid_request_error" || envelope["code"] != "model_not_found" {
		t.Fatalf("error envelope = %#v", envelope)
	}
}

func TestVideoCreateWithTaskIDNeverRetries(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			engine, _, _, _, _, forwarder := newVideoGatewayFixture(t)
			forwarder.results = []UpstreamResult{
				{
					StatusCode: status, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
					Body: []byte(`{"task_id":"task_up_ambiguous","error":{"message":"retryable"}}`),
				},
				videoUpstreamSuccess("task_up_should_not_run"),
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"sora-2","prompt":"fox"}`))
			request.Header.Set("Authorization", "Bearer gl-video")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if len(forwarder.inputs) != 1 {
				t.Fatalf("attempts = %d, want 1", len(forwarder.inputs))
			}
			wantStatus := http.StatusBadGateway
			wantCode := "upstream_error"
			if status == http.StatusTooManyRequests {
				wantStatus, wantCode = http.StatusTooManyRequests, "rate_limit_exceeded"
			}
			if recorder.Code != wantStatus {
				t.Fatalf("status = %d, want %d body = %s", recorder.Code, wantStatus, recorder.Body.String())
			}
			if code := videoJSONResponse(t, recorder)["error"].(map[string]any)["code"]; code != wantCode {
				t.Fatalf("code = %v, want %q", code, wantCode)
			}
		})
	}
}

func TestVideoCreateStopsAtThreeAttempts(t *testing.T) {
	engine, _, _, _, _, forwarder := newVideoGatewayFixture(t)
	for i := 0; i < 4; i++ {
		forwarder.results = append(forwarder.results, UpstreamResult{
			StatusCode: http.StatusInternalServerError, DispatchState: execution.DispatchMaybeSent,
			ResponseStarted: true, Body: []byte(`{}`),
		})
	}
	forwarder.results = append(forwarder.results, videoUpstreamSuccess("task_up_should_not_run"))
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"sora-2","prompt":"fox"}`))
	request.Header.Set("Authorization", "Bearer gl-video")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if len(forwarder.inputs) != 3 {
		t.Fatalf("attempts = %d, want 3", len(forwarder.inputs))
	}
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestVideoDeleteFailureDoesNotMarkTaskCancelled(t *testing.T) {
	engine, _, store, _, _, forwarder := newVideoGatewayFixture(t)
	forwarder.results = []UpstreamResult{videoUpstreamSuccess("task_up_delete_fail")}
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"sora-2","prompt":"fox"}`))
	request.Header.Set("Authorization", "Bearer gl-video")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("create status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	id := videoJSONResponse(t, recorder)["id"].(string)

	forwarder.results = append(forwarder.results, UpstreamResult{
		StatusCode: http.StatusConflict, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		Body: []byte(`{"error":{"message":"task cannot be cancelled"}}`),
	})
	request = httptest.NewRequest(http.MethodDelete, "/v1/videos/"+id, nil)
	request.Header.Set("Authorization", "Bearer gl-video")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("delete status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	task, err := store.Get(request.Context(), 1, id)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status == video.StatusCancelled {
		t.Fatal("failed delete was persisted as cancelled")
	}
}

type cancelAfterForward struct {
	*scriptedForwarder
	cancel context.CancelFunc
}

func (forwarder *cancelAfterForward) Forward(ctx context.Context, input ForwardInput) UpstreamResult {
	result := forwarder.scriptedForwarder.Forward(ctx, input)
	forwarder.cancel()
	return result
}

func TestVideoBindingSurvivesClientDisconnect(t *testing.T) {
	engine, handler, store, _, _, forwarder := newVideoGatewayFixture(t)
	forwarder.results = []UpstreamResult{videoUpstreamSuccess("task_up_disconnect")}
	ctx, cancel := context.WithCancel(context.Background())
	handler.forwarder = &cancelAfterForward{scriptedForwarder: forwarder, cancel: cancel}
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"sora-2","prompt":"fox"}`)).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer gl-video")
	request.Header.Set("Idempotency-Key", "disconnect-key")
	engine.ServeHTTP(httptest.NewRecorder(), request)

	replay := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"sora-2","prompt":"fox"}`))
	replay.Header.Set("Authorization", "Bearer gl-video")
	replay.Header.Set("Idempotency-Key", "disconnect-key")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, replay)
	if recorder.Code != http.StatusOK {
		t.Fatalf("replay status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	id, _ := videoJSONResponse(t, recorder)["id"].(string)
	task, err := store.Get(context.Background(), 1, id)
	if err != nil {
		t.Fatalf("binding was not persisted after disconnect: %v", err)
	}
	if task.UpstreamTaskID != "task_up_disconnect" {
		t.Fatalf("binding = %#v", task)
	}
	if len(forwarder.inputs) != 1 {
		t.Fatalf("upstream calls = %d, want 1", len(forwarder.inputs))
	}
}

func TestValidatePublicVideoURLRejectsPrivateTargets(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1/x", "http://127.1.2.3/x", "http://169.254.169.254/latest/meta-data",
		"http://10.0.0.1/x", "http://192.168.1.1/x", "http://100.64.0.1/x", "http://[::1]/x",
		"http://0.0.0.0/x", "http://localhost/x", "ftp://1.1.1.1/x", "http://user:pass@1.1.1.1/x",
	} {
		if err := validatePublicVideoURL(context.Background(), raw); err == nil {
			t.Fatalf("validatePublicVideoURL(%q) accepted a private/invalid target", raw)
		}
	}
	if err := validatePublicVideoURL(context.Background(), "http://1.1.1.1/x"); err != nil {
		t.Fatalf("public literal rejected: %v", err)
	}
}

func TestVideoHTTPClientRedirectRejectsPrivateTarget(t *testing.T) {
	client, err := newVideoHTTPClient(outboundproxy.Effective{}, state.TimeoutConfig{}, true)
	if err != nil {
		t.Fatal(err)
	}
	original := httptest.NewRequest(http.MethodGet, "http://1.1.1.1/first", nil)
	redirect := httptest.NewRequest(http.MethodGet, "http://169.254.169.254/latest/meta-data", nil)
	if err := client.CheckRedirect(redirect, []*http.Request{original}); err == nil {
		t.Fatal("private redirect target was accepted")
	}
	public := httptest.NewRequest(http.MethodGet, "http://1.1.1.1/second", nil)
	if err := client.CheckRedirect(public, []*http.Request{original}); err != nil {
		t.Fatalf("public redirect target rejected: %v", err)
	}
}
