package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestControlJSONBodyLimitBoundary(t *testing.T) {
	if maxControlJSONBodyBytes != 32<<20 {
		t.Fatalf("maxControlJSONBodyBytes = %d, want %d", maxControlJSONBodyBytes, 32<<20)
	}
	if apiErr := app_errors.ErrRequestTooLarge; apiErr.HTTPStatus != http.StatusRequestEntityTooLarge ||
		apiErr.Code != "REQUEST_TOO_LARGE" || apiErr.Message != "Request body is too large" {
		t.Fatalf("ErrRequestTooLarge = %#v, want 413 REQUEST_TOO_LARGE contract", apiErr)
	}

	const prefix = `{"name":"client"}`
	for _, test := range []struct {
		name    string
		size    int64
		wantErr bool
	}{
		{name: "exact limit", size: maxControlJSONBodyBytes},
		{name: "one byte over limit", size: maxControlJSONBodyBytes + 1, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := prefix + strings.Repeat(" ", int(test.size)-len(prefix))
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest(http.MethodPost, "/api/groups", strings.NewReader(body))
			context.Request.ContentLength = -1

			var target struct {
				Name string `json:"name"`
			}
			err := bindStrictJSON(context, &target)
			if !test.wantErr {
				if err != nil {
					t.Fatalf("bindStrictJSON() error = %v", err)
				}
				if target.Name != "client" {
					t.Fatalf("target.Name = %q, want client", target.Name)
				}
				return
			}

			var maxBytesError *http.MaxBytesError
			if !errors.As(err, &maxBytesError) {
				t.Fatalf("bindStrictJSON() error = %T %v, want *http.MaxBytesError", err, err)
			}
			if got := mapControlJSONError(err); got != app_errors.ErrRequestTooLarge {
				t.Fatalf("mapControlJSONError() = %#v, want ErrRequestTooLarge", got)
			}
		})
	}
}

type controlWhitespaceReader struct{}

func (controlWhitespaceReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = ' '
	}
	return len(buffer), nil
}

func oversizedControlJSONBody(prefix string) io.Reader {
	padding := maxControlJSONBodyBytes + 1 - int64(len(prefix))
	return io.MultiReader(
		strings.NewReader(prefix),
		io.LimitReader(controlWhitespaceReader{}, padding),
	)
}

type controlJSONBodyLimitState struct {
	rowCounts       [3]int64
	snapshot        *state.ConfigSnapshot
	group           models.Group
	upstreamKey     models.UpstreamKey
	registryRuntime []state.KeyRuntimeView
	accessKey       models.AccessKey
}

func captureControlJSONBodyLimitState(
	t *testing.T,
	fixture serviceFixture,
	groupID uint,
	accessKeyID uint,
) controlJSONBodyLimitState {
	t.Helper()
	var group models.Group
	if err := fixture.db.First(&group, groupID).Error; err != nil {
		t.Fatalf("load body-limit Group: %v", err)
	}
	var upstreamKey models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", groupID).
		Order("id ASC").Take(&upstreamKey).Error; err != nil {
		t.Fatalf("load body-limit UpstreamKey: %v", err)
	}
	return controlJSONBodyLimitState{
		rowCounts:       discoveryRowCounts(t, fixture.db),
		snapshot:        fixture.manager.Current(),
		group:           group,
		upstreamKey:     upstreamKey,
		registryRuntime: fixture.registry.Snapshot(),
		accessKey:       loadAccessKeyRow(t, fixture.db, accessKeyID),
	}
}

func assertControlJSONBodyLimitStateUnchanged(
	t *testing.T,
	fixture serviceFixture,
	groupID uint,
	accessKeyID uint,
	want controlJSONBodyLimitState,
) {
	t.Helper()
	got := captureControlJSONBodyLimitState(t, fixture, groupID, accessKeyID)
	if got.rowCounts != want.rowCounts {
		t.Errorf("database row counts = %v, want unchanged %v", got.rowCounts, want.rowCounts)
	}
	if got.snapshot != want.snapshot {
		t.Errorf(
			"snapshot pointer/revision changed: got=%p/%d want=%p/%d",
			got.snapshot,
			got.snapshot.Revision,
			want.snapshot,
			want.snapshot.Revision,
		)
	}
	if !reflect.DeepEqual(got.group, want.group) {
		t.Errorf("persisted Group changed")
	}
	if !reflect.DeepEqual(got.upstreamKey, want.upstreamKey) {
		t.Errorf("persisted UpstreamKey changed")
	}
	if !reflect.DeepEqual(got.registryRuntime, want.registryRuntime) {
		t.Errorf(
			"Registry runtime changed: got=%#v want=%#v",
			got.registryRuntime,
			want.registryRuntime,
		)
	}
	if !reflect.DeepEqual(got.accessKey, want.accessKey) {
		t.Errorf("persisted AccessKey changed")
	}
}

func TestControlJSONBodyLimitAppliesToEveryJSONEndpoint(t *testing.T) {
	initControlI18n(t)

	for _, endpoint := range []struct {
		name       string
		method     string
		path       func(groupID, upstreamKeyID, accessKeyID uint) string
		jsonPrefix string
	}{
		{
			name: "create group", method: http.MethodPost,
			path: func(uint, uint, uint) string { return "/api/groups" },
			jsonPrefix: `{"name":"body-limit-group","upstream_url":"https://body-limit-create.example.com/v1",` +
				`"protocols":["openai"],"keys":"sk-body-limit-create","config":{}}`,
		},
		{
			name: "import group keys", method: http.MethodPost,
			path: func(groupID, _, _ uint) string {
				return "/api/groups/" + strconv.FormatUint(uint64(groupID), 10) + "/keys/import"
			},
			jsonPrefix: `{"keys":"sk-body-limit-import"}`,
		},
		{
			name: "update group", method: http.MethodPut,
			path: func(groupID, _, _ uint) string {
				return fmt.Sprintf("/api/groups/%d", groupID)
			},
			jsonPrefix: `{"name":"body-limit-updated-group"}`,
		},
		{
			name: "discover group models", method: http.MethodPost,
			path: func(groupID, _, _ uint) string {
				return "/api/groups/" + strconv.FormatUint(uint64(groupID), 10) + "/models/discover"
			},
			jsonPrefix: `{}`,
		},
		{
			name: "save group models", method: http.MethodPut,
			path: func(groupID, _, _ uint) string {
				return "/api/groups/" + strconv.FormatUint(uint64(groupID), 10) + "/models"
			},
			jsonPrefix: `{"models":[]}`,
		},
		{
			name: "update upstream key", method: http.MethodPut,
			path: func(groupID, upstreamKeyID, _ uint) string {
				return fmt.Sprintf("/api/groups/%d/keys/%d", groupID, upstreamKeyID)
			},
			jsonPrefix: `{"status":"disabled"}`,
		},
		{
			name: "discover draft models", method: http.MethodPost,
			path: func(uint, uint, uint) string { return "/api/models/discover" },
			jsonPrefix: `{"upstream_url":"https://body-limit-discover.example.com/v1",` +
				`"protocols":["openai"],"keys":"sk-body-limit-discover","config":{}}`,
		},
		{
			name: "create access key", method: http.MethodPost,
			path:       func(uint, uint, uint) string { return "/api/access-keys" },
			jsonPrefix: `{"name":"body-limit-created-access-key"}`,
		},
		{
			name: "update access key", method: http.MethodPut,
			path: func(_, _ uint, accessKeyID uint) string {
				return "/api/access-keys/" + strconv.FormatUint(uint64(accessKeyID), 10)
			},
			jsonPrefix: `{"name":"body-limit-updated-access-key"}`,
		},
		{
			name: "route inspector", method: http.MethodPost,
			path:       func(uint, uint, uint) string { return "/api/route/inspect" },
			jsonPrefix: `{"protocol":"openai","external_model":"gpt-4o","access_key_id":1}`,
		},
	} {
		t.Run(endpoint.method+" "+endpoint.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			groupID := createGroupForKeyImport(t, fixture, "sk-body-limit-seed")
			accessKey, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{
				Name: "body-limit-seed-access-key",
			})
			if err != nil {
				t.Fatalf("seed CreateAccessKey() error = %v", err)
			}
			discoveryCalls := 0
			fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
				value: protocol.OpenAI,
				listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
					discoveryCalls++
					return []string{"body-limit-model"}, nil
				},
			})
			engine := gin.New()
			NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
			before := captureControlJSONBodyLimitState(t, fixture, groupID, accessKey.ID)

			path := endpoint.path(groupID, before.upstreamKey.ID, accessKey.ID)
			request := httptest.NewRequest(endpoint.method, path, oversizedControlJSONBody(endpoint.jsonPrefix))
			request.ContentLength = -1
			request.Header.Set("Authorization", "Bearer test-auth-key")
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()

			engine.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusRequestEntityTooLarge {
				t.Errorf("response = %d %s, want 413", recorder.Code, recorder.Body.String())
			} else {
				var envelope struct {
					Code string          `json:"code"`
					Data json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
					t.Errorf("decode response: %v", err)
				} else {
					if envelope.Code != app_errors.ErrRequestTooLarge.Code {
						t.Errorf("code = %q, want %q", envelope.Code, app_errors.ErrRequestTooLarge.Code)
					}
					if len(envelope.Data) != 0 {
						t.Errorf("data = %s, want omitted", envelope.Data)
					}
				}
			}
			assertControlJSONBodyLimitStateUnchanged(t, fixture, groupID, accessKey.ID, before)
			if discoveryCalls != 0 {
				t.Errorf("model discovery calls = %d, want 0", discoveryCalls)
			}
		})
	}
}

func TestControlJSONBodyLimitContentLengthFastPathPreservesAuthenticationPriority(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	for _, auth := range []struct {
		name       string
		header     string
		wantStatus int
		wantCode   string
	}{
		{name: "valid", header: "Bearer test-auth-key", wantStatus: http.StatusRequestEntityTooLarge, wantCode: "REQUEST_TOO_LARGE"},
		{name: "missing", wantStatus: http.StatusUnauthorized, wantCode: "UNAUTHORIZED"},
		{name: "invalid", header: "Bearer wrong-auth-key", wantStatus: http.StatusUnauthorized, wantCode: "UNAUTHORIZED"},
	} {
		t.Run(auth.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/groups", strings.NewReader("{}"))
			request.ContentLength = maxControlJSONBodyBytes + 1
			if auth.header != "" {
				request.Header.Set("Authorization", auth.header)
			}
			recorder := httptest.NewRecorder()

			engine.ServeHTTP(recorder, request)

			if recorder.Code != auth.wantStatus {
				t.Fatalf("response = %d %s, want %d", recorder.Code, recorder.Body.String(), auth.wantStatus)
			}
			var envelope struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if envelope.Code != auth.wantCode {
				t.Fatalf("code = %q, want %q", envelope.Code, auth.wantCode)
			}
		})
	}
}

func TestControlJSONBodyLimitLocalizes413(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	for _, test := range []struct {
		language string
		message  string
	}{
		{language: "zh-CN", message: "请求体过大"},
		{language: "en-US", message: "Request body is too large"},
		{language: "ja-JP", message: "リクエストボディが大きすぎます"},
	} {
		t.Run(test.language, func(t *testing.T) {
			const prefix = `{"name":"localized-limit","upstream_url":"https://localized-limit.example.com/v1",` +
				`"protocols":["openai"],"keys":"sk-localized-limit","config":{}}`
			request := httptest.NewRequest(http.MethodPost, "/api/groups", oversizedControlJSONBody(prefix))
			request.ContentLength = -1
			request.Header.Set("Authorization", "Bearer test-auth-key")
			request.Header.Set("Accept-Language", test.language)
			recorder := httptest.NewRecorder()

			engine.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("response = %d %s, want 413", recorder.Code, recorder.Body.String())
			}
			var envelope struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if envelope.Code != app_errors.ErrRequestTooLarge.Code || envelope.Message != test.message {
				t.Fatalf("envelope = %#v, want code %q message %q", envelope, app_errors.ErrRequestTooLarge.Code, test.message)
			}
		})
	}
}

func TestManagementAuthRequiresConstantShapeBearerToken(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	tests := []struct {
		name       string
		header     string
		wantStatus int
	}{
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "empty bearer", header: "Bearer", wantStatus: http.StatusUnauthorized},
		{name: "wrong different length", header: "Bearer x", wantStatus: http.StatusUnauthorized},
		{name: "extra field", header: "Bearer test-auth-key extra", wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme", header: "Basic test-auth-key", wantStatus: http.StatusUnauthorized},
		{name: "case insensitive", header: "bEaReR test-auth-key", wantStatus: http.StatusOK},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/groups", nil)
			request.RemoteAddr = "192.0.2." + strconv.Itoa(index+1) + ":1234"
			if test.header != "" {
				request.Header.Set("Authorization", test.header)
			}
			engine.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if test.wantStatus == http.StatusUnauthorized {
				var body struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Code != "UNAUTHORIZED" {
					t.Fatalf("unauthorized body = %s, error=%v", recorder.Body.String(), err)
				}
			}
		})
	}
}

func TestManagementAuthLocalizesUnauthorized(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	for _, test := range []struct {
		language string
		message  string
	}{
		{language: "en-US", message: "Invalid authorization key"},
		{language: "zh-CN", message: "无效的授权密钥"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/groups", nil)
		request.Header.Set("Accept-Language", test.language)
		engine.ServeHTTP(recorder, request)
		if !strings.Contains(recorder.Body.String(), test.message) {
			t.Fatalf("%s body = %s, want message %q", test.language, recorder.Body.String(), test.message)
		}
	}
}

func TestManagementAuthDoesNotLogSecretOrDigest(t *testing.T) {
	initControlI18n(t)
	const authKey = "distinctive-control-auth-key"
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authKey}, fixture.service).RegisterRoutes(engine)

	var logs bytes.Buffer
	previousOutput := logrus.StandardLogger().Out
	logrus.SetOutput(&logs)
	t.Cleanup(func() { logrus.SetOutput(previousOutput) })

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/groups", nil)
	request.Header.Set("Authorization", "Bearer "+authKey+"-wrong")
	engine.ServeHTTP(recorder, request)

	digest := sha256.Sum256([]byte(authKey))
	for _, forbidden := range []string{authKey, hex.EncodeToString(digest[:])} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("auth logs expose %q: %s", forbidden, logs.String())
		}
	}
}

func TestCreateGroupEndpointReturnsSuccessAndConflictEnvelopes(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	request := httptest.NewRequest(http.MethodPost, "/api/groups", strings.NewReader(
		`{"name":"primary","upstream_url":"https://api.example.com/v1/","protocols":["openai"],"models":[{"id":"gpt-4o"}],"config":{},"keys":"sk-first"}`,
	))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("create status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var success struct {
		Code int               `json:"code"`
		Data GroupCreateResult `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &success); err != nil {
		t.Fatalf("decode success response: %v", err)
	}
	if success.Code != 0 || success.Data.GroupID == 0 || success.Data.GroupName != "primary" ||
		success.Data.KeysAdded != 1 {
		t.Fatalf("success response = %#v", success)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/groups", strings.NewReader(
		`{"upstream_url":" HTTPS://API.example.com/v1 ","protocols":["anthropic"],"keys":"sk-second"}`,
	))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var conflict struct {
		Code string                      `json:"code"`
		Data map[string][]map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("decode conflict response: %v", err)
	}
	if conflict.Code != "UPSTREAM_URL_CONFLICT" || len(conflict.Data) != 1 {
		t.Fatalf("conflict response = %#v", conflict)
	}
	groups := conflict.Data["groups"]
	if len(groups) != 1 || len(groups[0]) != 2 || groups[0]["id"] != float64(success.Data.GroupID) ||
		groups[0]["name"] != success.Data.GroupName {
		t.Fatalf("conflict groups = %#v", groups)
	}
}

func TestImportGroupKeysEndpointReturnsSuccessEnvelope(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-existing")
	beforeSnapshot := fixture.manager.Current()
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/groups/"+strconv.FormatUint(uint64(groupID), 10)+"/keys/import", strings.NewReader(
		`{"keys":"sk-existing\nsk-new\nsk-new"}`,
	))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("import status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int                        `json:"code"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode import response: %v", err)
	}
	if envelope.Code != 0 || len(envelope.Data) != 3 {
		t.Fatalf("success envelope = %#v", envelope)
	}
	for _, field := range []string{"group_id", "keys_added", "keys_duplicated"} {
		if _, ok := envelope.Data[field]; !ok {
			t.Fatalf("success data lacks %q: %#v", field, envelope.Data)
		}
	}
	var result GroupKeyImportResult
	data, err := json.Marshal(envelope.Data)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.GroupID != groupID || result.KeysAdded != 1 || result.KeysDuplicated != 2 {
		t.Fatalf("result = %#v", result)
	}
	if fixture.manager.Current() != beforeSnapshot {
		t.Fatal("endpoint import published Snapshot")
	}
}

func TestImportGroupKeysEndpointRejectsUnknownFieldsAndInvalidGroupID(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-existing")
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	tests := []struct {
		name    string
		groupID string
		body    string
	}{
		{name: "unknown field", groupID: strconv.FormatUint(uint64(groupID), 10), body: `{"keys":"sk-new","name":"must-not-change"}`},
		{name: "multiple JSON values", groupID: strconv.FormatUint(uint64(groupID), 10), body: `{"keys":"sk-new"} {"keys":"sk-other"}`},
		{name: "zero group ID", groupID: "0", body: `{"keys":"sk-new"}`},
		{name: "non-numeric group ID", groupID: "not-a-number", body: `{"keys":"sk-new"}`},
		{name: "overflowing group ID", groupID: "18446744073709551616", body: `{"keys":"sk-new"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			beforeSnapshot := fixture.manager.Current()
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/groups/"+test.groupID+"/keys/import", strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer test-auth-key")
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body=%s, want 400", recorder.Code, recorder.Body.String())
			}
			if fixture.manager.Current() != beforeSnapshot {
				t.Fatal("invalid endpoint request published Snapshot")
			}
			assertImportedKeyState(t, fixture, groupID, 1)
		})
	}
}

func TestImportGroupKeysEndpointReturnsGroupNotFound(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/groups/999/keys/import", strings.NewReader(`{"keys":"sk-new"}`))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Language", "zh-CN")
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body=%s, want 404", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != "NOT_FOUND" || envelope.Message != "分组不存在" {
		t.Fatalf("not-found envelope = %#v", envelope)
	}
}

func TestLegacyImportRouteIsNotRegistered(t *testing.T) {
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	for _, route := range engine.Routes() {
		if route.Method == http.MethodPost && route.Path == "/api/import" {
			t.Fatalf("legacy route remains registered: %#v", route)
		}
	}
}

func TestManagementWritesRejectUnknownFieldsAndMultipleJSONValues(t *testing.T) {
	initControlI18n(t)

	t.Run("group create rejects unknown top-level field", func(t *testing.T) {
		fixture := newServiceFixture(t)
		engine := gin.New()
		NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
		beforeRevision := fixture.manager.Current().Revision

		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/groups", strings.NewReader(
			`{"upstream_url":"https://api.example.com","protocols":["openai"],"keys":"sk-test","unexpected":true}`,
		))
		request.Header.Set("Authorization", "Bearer test-auth-key")
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/groups = %d %s, want 400", recorder.Code, recorder.Body.String())
		}
		assertGroupCount(t, fixture.db, 0)
		if got := fixture.manager.Current().Revision; got != beforeRevision {
			t.Fatalf("snapshot revision = %d, want unchanged %d", got, beforeRevision)
		}
	})

	t.Run("access key rejects unknown nested filter field", func(t *testing.T) {
		fixture := newServiceFixture(t)
		engine := gin.New()
		NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
		beforeRevision := fixture.manager.Current().Revision

		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/access-keys", strings.NewReader(
			`{"name":"client","filters":{"gropus":[1]}}`,
		))
		request.Header.Set("Authorization", "Bearer test-auth-key")
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/access-keys = %d %s, want 400", recorder.Code, recorder.Body.String())
		}
		var count int64
		if err := fixture.db.Table("access_keys").Count(&count).Error; err != nil {
			t.Fatalf("count access keys: %v", err)
		}
		if count != 0 {
			t.Fatalf("access key count = %d, want 0", count)
		}
		if got := fixture.manager.Current().Revision; got != beforeRevision {
			t.Fatalf("snapshot revision = %d, want unchanged %d", got, beforeRevision)
		}
	})

	t.Run("access key update rejects a second JSON value", func(t *testing.T) {
		fixture := newServiceFixture(t)
		fixture.service.random = bytes.NewReader(make([]byte, 16))
		created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "original"})
		if err != nil {
			t.Fatalf("CreateAccessKey() error = %v", err)
		}
		engine := gin.New()
		NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
		beforeRevision := fixture.manager.Current().Revision

		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(
			http.MethodPut,
			"/api/access-keys/"+strconv.FormatUint(uint64(created.ID), 10),
			strings.NewReader(`{"name":"changed"}{"status":"disabled"}`),
		)
		request.Header.Set("Authorization", "Bearer test-auth-key")
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("PUT /api/access-keys/:id = %d %s, want 400", recorder.Code, recorder.Body.String())
		}
		row := loadAccessKeyRow(t, fixture.db, created.ID)
		if row.Name != "original" || row.Status != string(state.AccessKeyStatusActive) {
			t.Fatalf("access key row = %#v, want original active row", row)
		}
		if got := fixture.manager.Current().Revision; got != beforeRevision {
			t.Fatalf("snapshot revision = %d, want unchanged %d", got, beforeRevision)
		}
	})
}

func TestUpdateGroupEndpointRejectsStrictInvalidBodies(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-update-http")
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	for _, test := range []struct {
		name string
		body string
		code string
	}{
		{name: "empty object", body: `{}`, code: app_errors.ErrBadRequest.Code},
		{name: "confirmation only", body: `{"confirm_upstream_url_change":true}`, code: app_errors.ErrBadRequest.Code},
		{name: "unknown field", body: `{"name":"changed","unknown":true}`, code: app_errors.ErrInvalidJSON.Code},
		{name: "multiple JSON values", body: `{"name":"changed"} {"enabled":false}`, code: app_errors.ErrInvalidJSON.Code},
		{name: "null name", body: `{"name":null}`, code: app_errors.ErrValidation.Code},
		{name: "negative weight", body: `{"weight_manual":-1}`, code: app_errors.ErrValidation.Code},
		{name: "empty protocols", body: `{"protocols":[]}`, code: app_errors.ErrValidation.Code},
		{name: "invalid config", body: `{"config":{"first_byte_timeout":-1}}`, code: app_errors.ErrValidation.Code},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := fixture.manager.Current().Revision
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPut,
				"/api/groups/"+strconv.FormatUint(uint64(groupID), 10),
				strings.NewReader(test.body),
			)
			request.Header.Set("Authorization", "Bearer test-auth-key")
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)

			var envelope struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if recorder.Code != http.StatusBadRequest || envelope.Code != test.code {
				t.Fatalf("response = %d %#v, want 400 %q", recorder.Code, envelope, test.code)
			}
			if fixture.manager.Current().Revision != before {
				t.Fatal("invalid update published a Snapshot")
			}
		})
	}
}

func TestUpdateGroupEndpointRejectsTopLevelNullWithoutMutation(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-update-null")
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	var beforeGroup models.Group
	if err := fixture.db.First(&beforeGroup, groupID).Error; err != nil {
		t.Fatal(err)
	}
	var beforeKeys []models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", groupID).Order("id ASC").Find(&beforeKeys).Error; err != nil {
		t.Fatal(err)
	}
	beforeRevision := fixture.manager.Current().Revision
	beforeRegistry := fixture.registry.CollectCandidates([]uint{groupID}, nil, time.Time{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/groups/"+strconv.FormatUint(uint64(groupID), 10),
		strings.NewReader(`null`),
	)
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)
	assertUpdateGroupErrorResponse(t, recorder, http.StatusBadRequest, app_errors.ErrInvalidJSON.Code, "请求错误")

	var afterGroup models.Group
	if err := fixture.db.First(&afterGroup, groupID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterGroup, beforeGroup) {
		t.Fatalf("persisted group changed: got=%#v want=%#v", afterGroup, beforeGroup)
	}
	var afterKeys []models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", groupID).Order("id ASC").Find(&afterKeys).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterKeys, beforeKeys) {
		t.Fatalf("persisted keys changed: got=%#v want=%#v", afterKeys, beforeKeys)
	}
	if got := fixture.manager.Current().Revision; got != beforeRevision {
		t.Fatalf("snapshot revision = %d, want %d", got, beforeRevision)
	}
	if got := fixture.registry.CollectCandidates([]uint{groupID}, nil, time.Time{}); !reflect.DeepEqual(got, beforeRegistry) {
		t.Fatalf("Registry candidates changed: got=%#v want=%#v", got, beforeRegistry)
	}
}

func TestUpdateGroupEndpointURLConflictsSuccessI18nAndAuth(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	firstID := createGroupForKeyImport(t, fixture, "sk-update-first")
	second, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name:        stringPointer("other-group"),
		UpstreamURL: "https://conflict.example.com/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Keys:        "sk-update-second",
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	path := "/api/groups/" + strconv.FormatUint(uint64(firstID), 10)

	request := httptest.NewRequest(http.MethodPut, path, strings.NewReader(
		`{"upstream_url":"https://unique.example.com/v1"}`,
	))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Language", "ja-JP")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	assertUpdateGroupErrorResponse(t, recorder, http.StatusConflict, app_errors.ErrUpstreamURLChangeConfirmationRequired.Code, "アップストリームURLの変更には明示的な確認が必要です")

	request = httptest.NewRequest(http.MethodPut, path, strings.NewReader(
		`{"upstream_url":"https://conflict.example.com/v1/","confirm_upstream_url_change":true}`,
	))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	assertUpdateGroupErrorResponse(t, recorder, http.StatusConflict, app_errors.ErrUpstreamURLConflict.Code, "已有分组使用该上游地址")

	request = httptest.NewRequest(http.MethodPut, path, strings.NewReader(
		`{"name":"other-group"}`,
	))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	assertUpdateGroupErrorResponse(t, recorder, http.StatusConflict, app_errors.ErrDuplicateResource.Code, "分组名称已存在")

	request = httptest.NewRequest(http.MethodPut, path, strings.NewReader(
		`{"upstream_url":" HTTPS://UNIQUE.example.com/v1/ ","confirm_upstream_url_change":true,"enabled":false}`,
	))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Language", "ja-JP")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("success response = %d %s", recorder.Code, recorder.Body.String())
	}
	var success struct {
		Code    int               `json:"code"`
		Message string            `json:"message"`
		Data    GroupUpdateResult `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &success); err != nil {
		t.Fatal(err)
	}
	if success.Code != 0 || success.Message != "成功" ||
		!success.Data.ModelRediscoveryRecommended || success.Data.Group.Enabled ||
		success.Data.Group.UpstreamURL != "https://unique.example.com/v1" {
		t.Fatalf("success envelope = %#v", success)
	}

	unauthorized := httptest.NewRecorder()
	engine.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"enabled":true}`)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized response = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	if second.GroupID == 0 {
		t.Fatal("second Group was not created")
	}
}

func TestUpdateGroupEndpointRejectsOversizedJSON(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-update-limit")
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	before := fixture.manager.Current().Revision

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/groups/"+strconv.FormatUint(uint64(groupID), 10),
		oversizedControlJSONBody(`{"name":"changed"}`),
	)
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	request.ContentLength = -1
	engine.ServeHTTP(recorder, request)
	assertUpdateGroupErrorResponse(t, recorder, http.StatusRequestEntityTooLarge, app_errors.ErrRequestTooLarge.Code, "请求体过大")
	if fixture.manager.Current().Revision != before {
		t.Fatal("oversized update published a Snapshot")
	}
}

func TestUpdateGroupModelsEndpointRejectsStrictInvalidBodiesWithoutMutation(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://model-save-http-invalid.example.com/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Models: optionalGroupModels{
			Set:    true,
			Values: []GroupModel{{ID: "provider-old", Alias: "old-public"}},
		},
		Keys: "sk-model-save-http-invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	for _, test := range []struct {
		name string
		body string
		code string
	}{
		{name: "missing models", body: `{}`, code: app_errors.ErrValidation.Code},
		{name: "null models", body: `{"models":null}`, code: app_errors.ErrValidation.Code},
		{name: "models object", body: `{"models":{}}`, code: app_errors.ErrInvalidJSON.Code},
		{name: "unknown top-level field", body: `{"models":[],"unknown":true}`, code: app_errors.ErrInvalidJSON.Code},
		{name: "unknown nested field", body: `{"models":[{"id":"provider","unknown":true}]}`, code: app_errors.ErrInvalidJSON.Code},
		{name: "blank upstream model ID", body: `{"models":[{"id":" "}]}`, code: app_errors.ErrValidation.Code},
		{
			name: "duplicate external model",
			body: `{"models":[{"id":"provider-a","alias":"public"},` +
				`{"id":"provider-b","alias":"public"}]}`,
			code: app_errors.ErrValidation.Code,
		},
		{name: "multiple JSON values", body: `{"models":[]} {"models":[]}`, code: app_errors.ErrInvalidJSON.Code},
		{name: "top-level null", body: `null`, code: app_errors.ErrInvalidJSON.Code},
	} {
		t.Run(test.name, func(t *testing.T) {
			beforeRevision := fixture.manager.Current().Revision
			beforeModels := loadCreatedGroupModels(t, fixture, created.GroupID)
			recorder := serveRawGroupModelsUpdateRequest(
				t,
				engine,
				"test-auth-key",
				"en-US",
				strconv.FormatUint(uint64(created.GroupID), 10),
				test.body,
			)
			var envelope struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if recorder.Code != http.StatusBadRequest || envelope.Code != test.code {
				t.Fatalf("response = %d %#v, want 400 %q", recorder.Code, envelope, test.code)
			}
			if fixture.manager.Current().Revision != beforeRevision {
				t.Fatal("invalid models save published a Snapshot")
			}
			if got := loadCreatedGroupModels(t, fixture, created.GroupID); !reflect.DeepEqual(got, beforeModels) {
				t.Fatalf("invalid models save changed persistence: got=%#v want=%#v", got, beforeModels)
			}
		})
	}
}

func TestUpdateGroupModelsEndpointIDsAuthNotFoundAndSuccessDTO(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://model-save-http.example.com/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Models: optionalGroupModels{
			Set:    true,
			Values: []GroupModel{{ID: "provider-old", Alias: "old-public"}},
		},
		Keys: "sk-model-save-http",
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	body := `{"models":[{"id":"provider-new","alias":"new-public"}]}`

	for _, rawID := range []string{"0", "-1", "not-a-number", "18446744073709551616"} {
		beforeRevision := fixture.manager.Current().Revision
		recorder := serveRawGroupModelsUpdateRequest(t, engine, "test-auth-key", "en-US", rawID, body)
		if recorder.Code != http.StatusBadRequest ||
			!strings.Contains(recorder.Body.String(), `"code":"BAD_REQUEST"`) {
			t.Fatalf("Group ID %q response = %d %s", rawID, recorder.Code, recorder.Body.String())
		}
		if fixture.manager.Current().Revision != beforeRevision {
			t.Fatalf("invalid Group ID %q published a Snapshot", rawID)
		}
	}

	beforeRevision := fixture.manager.Current().Revision
	unauthorized := serveRawGroupModelsUpdateRequest(
		t,
		engine,
		"",
		"en-US",
		strconv.FormatUint(uint64(created.GroupID), 10),
		body,
	)
	if unauthorized.Code != http.StatusUnauthorized ||
		!strings.Contains(unauthorized.Body.String(), `"code":"UNAUTHORIZED"`) {
		t.Fatalf("unauthorized response = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	if fixture.manager.Current().Revision != beforeRevision {
		t.Fatal("unauthorized models save published a Snapshot")
	}

	missing := serveRawGroupModelsUpdateRequest(t, engine, "test-auth-key", "zh-CN", "999", body)
	if missing.Code != http.StatusNotFound ||
		!strings.Contains(missing.Body.String(), `"code":"NOT_FOUND"`) ||
		!strings.Contains(missing.Body.String(), "分组不存在") {
		t.Fatalf("missing Group response = %d %s", missing.Code, missing.Body.String())
	}

	success := serveRawGroupModelsUpdateRequest(
		t,
		engine,
		"test-auth-key",
		"ja-JP",
		strconv.FormatUint(uint64(created.GroupID), 10),
		body,
	)
	if success.Code != http.StatusOK {
		t.Fatalf("success response = %d %s", success.Code, success.Body.String())
	}
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(success.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != 0 || envelope.Message != "成功" {
		t.Fatalf("success envelope = %#v", envelope)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, wrapped := fields["group"]; wrapped {
		t.Fatalf("models save returned GroupUpdateResult wrapper: %s", envelope.Data)
	}
	if _, wrapped := fields["model_rediscovery_recommended"]; wrapped {
		t.Fatalf("models save returned discovery diff metadata: %s", envelope.Data)
	}
	var detail GroupDetailResponse
	if err := json.Unmarshal(envelope.Data, &detail); err != nil {
		t.Fatal(err)
	}
	wantModels := []GroupModel{{ID: "provider-new", Alias: "new-public"}}
	if detail.ID != created.GroupID || !reflect.DeepEqual(detail.Models, wantModels) || detail.KeyCount != 1 {
		t.Fatalf("success detail = %#v", detail)
	}
}

func serveRawGroupModelsUpdateRequest(
	t *testing.T,
	engine *gin.Engine,
	authKey, language, groupID, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/groups/"+groupID+"/models",
		strings.NewReader(body),
	)
	if authKey != "" {
		request.Header.Set("Authorization", "Bearer "+authKey)
	}
	request.Header.Set("Accept-Language", language)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)
	return recorder
}

func assertUpdateGroupErrorResponse(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	wantStatus int,
	wantCode string,
	wantMessage string,
) {
	t.Helper()
	var envelope struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if recorder.Code != wantStatus || envelope.Code != wantCode || envelope.Message != wantMessage {
		t.Fatalf("response = %d %#v, want %d %q %q", recorder.Code, envelope, wantStatus, wantCode, wantMessage)
	}
}

func TestUpdateAccessKeyRoutesParseIDsAndPreservePointerSemantics(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(make([]byte, 16))
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "route-key"})
	if err != nil {
		t.Fatalf("CreateAccessKey() error = %v", err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	request := httptest.NewRequest(http.MethodPut, "/api/access-keys/"+strconv.FormatUint(uint64(created.ID), 10), strings.NewReader(`{"status":"disabled"}`))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), created.Key) {
		t.Fatalf("PUT access key = %d %s", recorder.Code, recorder.Body.String())
	}
	row := loadAccessKeyRow(t, fixture.db, created.ID)
	if row.Name != "route-key" || row.Status != string(state.AccessKeyStatusDisabled) {
		t.Fatalf("PUT row = %#v", row)
	}

	for _, path := range []string{
		"/api/access-keys/0",
		"/api/access-keys/-1",
		"/api/access-keys/not-a-number",
		"/api/access-keys/18446744073709551616",
	} {
		request = httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"name":"ignored"}`))
		request.Header.Set("Authorization", "Bearer test-auth-key")
		request.Header.Set("Content-Type", "application/json")
		recorder = httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("PUT %s = %d %s, want 400", path, recorder.Code, recorder.Body.String())
		}
	}

	request = httptest.NewRequest(http.MethodPut, "/api/access-keys/"+strconv.FormatUint(uint64(created.ID), 10), strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("empty PUT = %d %s, want 400", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/access-keys/"+strconv.FormatUint(uint64(created.ID), 10), nil)
	request.Header.Set("Authorization", "Bearer test-auth-key")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("DELETE access key = %d %s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/access-keys/"+strconv.FormatUint(uint64(created.ID), 10), nil)
	request.Header.Set("Authorization", "Bearer test-auth-key")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("repeated DELETE = %d %s, want 404", recorder.Code, recorder.Body.String())
	}
}

func TestServerDraftModelDiscoveryContract(t *testing.T) {
	initControlI18n(t)
	const authKey = "test-auth-key"

	newServer := func(value *recordingDiscoveryDialect) (*Service, *gin.Engine) {
		t.Helper()
		fixture := newServiceFixture(t)
		if value == nil {
			fixture.service.dialects = dialect.NewSet()
		} else {
			fixture.service.dialects = dialect.NewSet(value)
		}
		engine := gin.New()
		NewServer(&config.Config{AuthKey: authKey}, fixture.service).RegisterRoutes(engine)
		return fixture.service, engine
	}

	t.Run("success preserves order", func(t *testing.T) {
		_, engine := newServer(&recordingDiscoveryDialect{
			value: protocol.OpenAI,
			listFn: func(
				context.Context,
				string,
				string,
				state.HeaderRules,
			) ([]string, error) {
				return []string{"z-model", "a-model"}, nil
			},
		})
		recorder := serveDiscoveryRequest(t, engine, authKey,
			`{"upstream_url":"https://api.example.com","protocols":["openai"],`+
				`"keys":"sk-upstream","config":{}}`,
		)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
		}
		var response struct {
			Code int                        `json:"code"`
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if response.Code != 0 || len(response.Data) != 1 {
			t.Fatalf("response = %#v", response)
		}
		var models []string
		if err := json.Unmarshal(response.Data["models"], &models); err != nil ||
			!reflect.DeepEqual(models, []string{"z-model", "a-model"}) {
			t.Fatalf("models = %#v, error=%v", models, err)
		}
	})

	t.Run("empty list remains an array", func(t *testing.T) {
		_, engine := newServer(&recordingDiscoveryDialect{
			value: protocol.OpenAI,
			listFn: func(
				context.Context,
				string,
				string,
				state.HeaderRules,
			) ([]string, error) {
				return nil, nil
			},
		})
		recorder := serveDiscoveryRequest(t, engine, authKey,
			`{"upstream_url":"https://api.example.com","protocols":["openai"],`+
				`"keys":"sk-upstream","config":{}}`,
		)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"models":[]`) {
			t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("authentication is inherited", func(t *testing.T) {
		_, engine := newServer(&recordingDiscoveryDialect{
			value: protocol.OpenAI,
			listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
				t.Fatal("ListModels called without valid management authentication")
				return nil, nil
			},
		})
		for _, token := range []string{"", "wrong-key"} {
			recorder := serveDiscoveryRequest(t, engine, token,
				`{"upstream_url":"https://api.example.com","protocols":["openai"],`+
					`"keys":"sk-upstream","config":{}}`,
			)
			if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), `"code":"UNAUTHORIZED"`) {
				t.Fatalf("auth %q response = %d %s", token, recorder.Code, recorder.Body.String())
			}
		}
	})

	t.Run("strict JSON", func(t *testing.T) {
		_, engine := newServer(&recordingDiscoveryDialect{
			value: protocol.OpenAI,
			listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
				t.Fatal("ListModels called for invalid JSON")
				return nil, nil
			},
		})
		for _, payload := range []string{
			`{"upstream_url":"https://api.example.com","protocols":["openai"],"keys":"sk-upstream","config":{},"unknown":true}`,
			`{"upstream_url":"https://api.example.com","protocols":["openai"],"keys":"sk-upstream","config":{}}{}`,
			`{"upstream_url":"https://api.example.com","protocol":"openai","key":"sk-upstream"}`,
		} {
			recorder := serveDiscoveryRequest(t, engine, authKey, payload)
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"INVALID_JSON"`) {
				t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
			}
		}
	})

	t.Run("validation and invariant errors are fixed", func(t *testing.T) {
		_, engine := newServer(nil)
		for _, test := range []struct {
			payload string
			status  int
			code    string
		}{
			{payload: `{"upstream_url":"/relative","protocols":["openai"],"keys":"secret-key","config":{}}`, status: http.StatusBadRequest, code: "VALIDATION_FAILED"},
			{payload: `{"upstream_url":"https://api.example.com?token=query-secret","protocols":["openai"],"keys":"secret-key","config":{}}`, status: http.StatusInternalServerError, code: "INTERNAL_SERVER_ERROR"},
		} {
			recorder := serveDiscoveryRequest(t, engine, authKey, test.payload)
			if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
			}
			for _, secret := range []string{"secret-key", "query-secret", "api.example.com"} {
				if strings.Contains(recorder.Body.String(), secret) {
					t.Fatalf("response exposes %q: %s", secret, recorder.Body.String())
				}
			}
		}
	})

	t.Run("upstream failures map to localized bad gateway", func(t *testing.T) {
		service, engine := newServer(&recordingDiscoveryDialect{
			value: protocol.OpenAI,
			listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
				return nil, fmt.Errorf("raw upstream failure with secret-body")
			},
		})
		service.modelDiscoveryTimeout = 20 * time.Millisecond
		for _, test := range []struct {
			language string
			message  string
		}{
			{language: "en-US", message: "Upstream service error"},
			{language: "zh-CN", message: "上游服务错误"},
		} {
			recorder := serveDiscoveryRequestWithLanguage(t, engine, authKey,
				`{"upstream_url":"https://api.example.com?token=query-secret",`+
					`"protocols":["openai"],"keys":"secret-key","config":{}}`,
				test.language,
			)
			if recorder.Code != http.StatusBadGateway ||
				!strings.Contains(recorder.Body.String(), `"code":"BAD_GATEWAY"`) ||
				!strings.Contains(recorder.Body.String(), test.message) {
				t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
			}
			for _, forbidden := range []string{"secret-key", "query-secret", "secret-body", "raw upstream failure"} {
				if strings.Contains(recorder.Body.String(), forbidden) {
					t.Fatalf("response exposes %q: %s", forbidden, recorder.Body.String())
				}
			}
		}
	})

	t.Run("timeout maps to bad gateway", func(t *testing.T) {
		service, engine := newServer(&recordingDiscoveryDialect{
			value: protocol.OpenAI,
			listFn: func(ctx context.Context, _ string, _ string, _ state.HeaderRules) ([]string, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		})
		service.modelDiscoveryTimeout = 20 * time.Millisecond
		recorder := serveDiscoveryRequest(t, engine, authKey,
			`{"upstream_url":"https://api.example.com","protocols":["openai"],`+
				`"keys":"sk-upstream","config":{}}`,
		)
		if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), `"code":"BAD_GATEWAY"`) {
			t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("broken upstream JSON maps to bad gateway", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"data":[`))
		}))
		defer upstream.Close()

		fixture := newServiceFixture(t)
		fixture.service.dialects = dialect.NewSet(dialect.NewOpenAI(upstream.Client()))
		engine := gin.New()
		NewServer(&config.Config{AuthKey: authKey}, fixture.service).RegisterRoutes(engine)
		recorder := serveDiscoveryRequest(t, engine, authKey,
			`{"upstream_url":"`+upstream.URL+`","protocols":["openai"],`+
				`"keys":"sk-upstream","config":{}}`,
		)
		if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), `"code":"BAD_GATEWAY"`) {
			t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), `{"data":[`) {
			t.Fatalf("response exposes upstream body: %s", recorder.Body.String())
		}
	})
}

func TestServerDraftModelDiscoveryLogsOnlyMetadata(t *testing.T) {
	initControlI18n(t)
	const (
		authSecret  = "distinctive-auth-secret"
		keySecret   = "distinctive-upstream-key"
		querySecret = "distinctive-query-secret"
		bodySecret  = "distinctive-upstream-body"
	)
	fixture := newServiceFixture(t)
	fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
		value: protocol.Anthropic,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			return nil, fmt.Errorf("provider error: %s", bodySecret)
		},
	})
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authSecret}, fixture.service).RegisterRoutes(engine)

	var logs bytes.Buffer
	previousOutput := logrus.StandardLogger().Out
	logrus.SetOutput(&logs)
	t.Cleanup(func() { logrus.SetOutput(previousOutput) })

	recorder := serveDiscoveryRequest(t, engine, authSecret,
		`{"upstream_url":"https://api.example.com?token=`+querySecret+`",`+
			`"protocols":["anthropic"],"keys":"`+keySecret+`","config":{}}`,
	)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	logText := logs.String()
	for _, required := range []string{"operation=discover_models", "error_code=BAD_GATEWAY", "error_type="} {
		if !strings.Contains(logText, required) {
			t.Fatalf("logs missing %q: %s", required, logText)
		}
	}
	if strings.Contains(logText, "protocol=") {
		t.Fatalf("logs retain submitted protocol metadata: %s", logText)
	}
	for _, forbidden := range []string{authSecret, keySecret, querySecret, bodySecret, "provider error", "Authorization"} {
		if strings.Contains(logText, forbidden) {
			t.Fatalf("logs expose %q: %s", forbidden, logText)
		}
	}
}

func TestServerGroupModelDiscoveryBodyContract(t *testing.T) {
	initControlI18n(t)
	const authKey = "test-auth-key"

	newServer := func(t *testing.T, activeKey bool) (serviceFixture, *gin.Engine, uint) {
		t.Helper()
		fixture := newServiceFixture(t)
		created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
			UpstreamURL: "https://persisted-server.example.com/v1",
			Protocols:   []protocol.Protocol{protocol.OpenAI},
			Keys:        "persisted-server-key",
		})
		if err != nil {
			t.Fatalf("seed CreateGroup() error = %v", err)
		}
		if !activeKey {
			if err := fixture.db.Model(&models.UpstreamKey{}).
				Where("group_id = ?", created.GroupID).
				Update("status", models.UpstreamKeyStatusDisabled).Error; err != nil {
				t.Fatalf("disable persisted discovery key: %v", err)
			}
		}
		fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
			value: protocol.OpenAI,
			listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
				return []string{"z-model", "a-model"}, nil
			},
		})
		engine := gin.New()
		NewServer(&config.Config{AuthKey: authKey}, fixture.service).RegisterRoutes(engine)
		return fixture, engine, created.GroupID
	}

	t.Run("optional empty object accepts only no body whitespace and empty object", func(t *testing.T) {
		_, engine, groupID := newServer(t, true)
		for _, test := range []struct {
			name    string
			payload *string
		}{
			{name: "no body"},
			{name: "whitespace", payload: stringPointer(" \n\t ")},
			{name: "empty object", payload: stringPointer("{}")},
		} {
			t.Run(test.name, func(t *testing.T) {
				recorder := serveGroupDiscoveryRequest(t, engine, authKey, "en-US", groupID, test.payload)
				if recorder.Code != http.StatusOK {
					t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
				}
				var body struct {
					Code int                        `json:"code"`
					Data map[string]json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode success response: %v", err)
				}
				if body.Code != 0 || len(body.Data) != 1 {
					t.Fatalf("success response = %#v", body)
				}
				var gotModels []string
				if err := json.Unmarshal(body.Data["models"], &gotModels); err != nil ||
					!reflect.DeepEqual(gotModels, []string{"z-model", "a-model"}) {
					t.Fatalf("models = %#v, error=%v", gotModels, err)
				}
			})
		}

		for _, payload := range []string{"null", "[]", `{"refresh":true}`, "{} {}"} {
			t.Run("reject "+payload, func(t *testing.T) {
				recorder := serveGroupDiscoveryRequest(
					t, engine, authKey, "en-US", groupID, stringPointer(payload),
				)
				if recorder.Code != http.StatusBadRequest ||
					!strings.Contains(recorder.Body.String(), `"code":"INVALID_JSON"`) {
					t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
				}
			})
		}
	})

	t.Run("invalid Group IDs return bad request", func(t *testing.T) {
		_, engine, _ := newServer(t, true)
		for _, rawID := range []string{"0", "not-a-number", "18446744073709551616"} {
			recorder := serveRawGroupDiscoveryRequest(t, engine, authKey, "en-US", rawID, nil)
			if recorder.Code != http.StatusBadRequest ||
				!strings.Contains(recorder.Body.String(), `"code":"BAD_REQUEST"`) {
				t.Fatalf("Group ID %q response = %d %s", rawID, recorder.Code, recorder.Body.String())
			}
		}
	})

	t.Run("missing Group is localized not found", func(t *testing.T) {
		_, engine, _ := newServer(t, true)
		recorder := serveRawGroupDiscoveryRequest(t, engine, authKey, "zh-CN", "999", nil)
		if recorder.Code != http.StatusNotFound ||
			!strings.Contains(recorder.Body.String(), `"code":"NOT_FOUND"`) ||
			!strings.Contains(recorder.Body.String(), "分组不存在") {
			t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("no active key is localized conflict", func(t *testing.T) {
		_, engine, groupID := newServer(t, false)
		for _, test := range []struct {
			language string
			message  string
		}{
			{language: "en-US", message: "No active upstream key is available for this group"},
			{language: "zh-CN", message: "该分组没有可用的活跃上游密钥"},
			{language: "ja-JP", message: "このグループには利用可能な有効なアップストリームキーがありません"},
		} {
			recorder := serveGroupDiscoveryRequest(
				t, engine, authKey, test.language, groupID, nil,
			)
			if recorder.Code != http.StatusConflict ||
				!strings.Contains(recorder.Body.String(), `"code":"NO_ACTIVE_UPSTREAM_KEY"`) ||
				!strings.Contains(recorder.Body.String(), test.message) {
				t.Fatalf("%s response = %d %s", test.language, recorder.Code, recorder.Body.String())
			}
		}
	})
}

func serveGroupDiscoveryRequest(
	t *testing.T,
	engine *gin.Engine,
	authKey, language string,
	groupID uint,
	payload *string,
) *httptest.ResponseRecorder {
	t.Helper()
	return serveRawGroupDiscoveryRequest(
		t, engine, authKey, language, strconv.FormatUint(uint64(groupID), 10), payload,
	)
}

func serveRawGroupDiscoveryRequest(
	t *testing.T,
	engine *gin.Engine,
	authKey, language, groupID string,
	payload *string,
) *httptest.ResponseRecorder {
	t.Helper()
	var request *http.Request
	if payload != nil {
		request = httptest.NewRequest(
			http.MethodPost,
			"/api/groups/"+groupID+"/models/discover",
			strings.NewReader(*payload),
		)
	} else {
		request = httptest.NewRequest(
			http.MethodPost,
			"/api/groups/"+groupID+"/models/discover",
			nil,
		)
	}
	recorder := httptest.NewRecorder()
	request.Header.Set("Authorization", "Bearer "+authKey)
	request.Header.Set("Accept-Language", language)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)
	return recorder
}

func serveDiscoveryRequest(t *testing.T, engine *gin.Engine, authKey, payload string) *httptest.ResponseRecorder {
	t.Helper()
	return serveDiscoveryRequestWithLanguage(t, engine, authKey, payload, "en-US")
}

func serveDiscoveryRequestWithLanguage(
	t *testing.T,
	engine *gin.Engine,
	authKey, payload, language string,
) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/models/discover", strings.NewReader(payload))
	if authKey != "" {
		request.Header.Set("Authorization", "Bearer "+authKey)
	}
	request.Header.Set("Accept-Language", language)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)
	return recorder
}
