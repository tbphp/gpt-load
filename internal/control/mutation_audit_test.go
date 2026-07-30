package control

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
)

func TestClassifyMutationOutcome(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		errorCode  string
		want       string
	}{
		{name: "ok", statusCode: 200, want: "succeeded"},
		{name: "no content", statusCode: 204, want: "succeeded"},
		{
			name: "two hundred ignores stale error marker", statusCode: 200,
			errorCode: app_errors.ErrControlRecoveryPending.Code,
			want:      "succeeded",
		},
		{
			name: "bad request", statusCode: 400,
			errorCode: app_errors.ErrBadRequest.Code, want: "rejected",
		},
		{
			name: "precondition failed", statusCode: 412,
			errorCode: app_errors.ErrSettingsVersionConflict.Code,
			want:      "rejected",
		},
		{
			name: "recovery pending", statusCode: 503,
			errorCode: app_errors.ErrControlRecoveryPending.Code,
			want:      "blocked",
		},
		{
			name: "operation incomplete", statusCode: 503,
			errorCode: app_errors.ErrControlOperationIncomplete.Code,
			want:      "incomplete",
		},
		{
			name: "database", statusCode: 500,
			errorCode: app_errors.ErrDatabase.Code, want: "failed",
		},
		{name: "unmarked internal", statusCode: 500, want: "failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyMutationOutcome(
				test.statusCode,
				test.errorCode,
			); got != test.want {
				t.Fatalf(
					"classifyMutationOutcome(%d, %q) = %q, want %q",
					test.statusCode,
					test.errorCode,
					got,
					test.want,
				)
			}
		})
	}
}

func TestMutationLocatorsOnlyExposeValidatedIDs(t *testing.T) {
	tests := []struct {
		name   string
		params gin.Params
		locate func(*gin.Context) string
		want   string
	}{
		{
			name: "group", params: gin.Params{{Key: "group_id", Value: "7"}},
			locate: groupMutationLocator, want: "group:7",
		},
		{
			name:   "group zero",
			params: gin.Params{{Key: "group_id", Value: "0"}},
			locate: groupMutationLocator, want: "group:unknown",
		},
		{
			name:   "group raw secret",
			params: gin.Params{{Key: "group_id", Value: "1/raw-secret"}},
			locate: groupMutationLocator, want: "group:unknown",
		},
		{
			name: "group key",
			params: gin.Params{
				{Key: "group_id", Value: "7"},
				{Key: "key_id", Value: "9"},
			},
			locate: groupKeyMutationLocator, want: "group:7/key:9",
		},
		{
			name: "group key invalid key",
			params: gin.Params{
				{Key: "group_id", Value: "7"},
				{Key: "key_id", Value: "-7"},
			},
			locate: groupKeyMutationLocator, want: "group:7/key:unknown",
		},
		{
			name:   "group keys",
			params: gin.Params{{Key: "group_id", Value: "12"}},
			locate: groupKeysMutationLocator, want: "group:12/keys",
		},
		{
			name:   "access key",
			params: gin.Params{{Key: "id", Value: "23"}},
			locate: accessKeyMutationLocator, want: "access-key:23",
		},
		{
			name: "access key overflow",
			params: gin.Params{{
				Key: "id", Value: "18446744073709551616",
			}},
			locate: accessKeyMutationLocator, want: "access-key:unknown",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Params = test.params
			if got := test.locate(context); got != test.want {
				t.Fatalf("locator = %q, want %q", got, test.want)
			}
		})
	}

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	for value, want := range map[string]string{
		"new":                 "new",
		"settings:global":     "settings:global",
		"model-price:unknown": "model-price:unknown",
	} {
		if got := staticMutationLocator(value)(context); got != want {
			t.Errorf("static locator = %q, want %q", got, want)
		}
	}
}

func TestAuditMutationRecordsExactlyOneOutcome(t *testing.T) {
	initControlI18n(t)
	tests := []struct {
		name          string
		handler       gin.HandlerFunc
		wantStatus    int
		wantOutcome   string
		wantErrorCode string
	}{
		{
			name: "success",
			handler: func(c *gin.Context) {
				c.Status(http.StatusOK)
			},
			wantStatus: http.StatusOK, wantOutcome: "succeeded",
		},
		{
			name: "success clears stale marker",
			handler: func(c *gin.Context) {
				setMutationErrorCode(c, app_errors.ErrBadRequest.Code)
				c.Status(http.StatusNoContent)
			},
			wantStatus: http.StatusNoContent, wantOutcome: "succeeded",
		},
		{
			name: "rejected",
			handler: func(c *gin.Context) {
				writeServiceErrorResponse(
					c,
					"probe",
					app_errors.ErrBadRequest,
				)
			},
			wantStatus: http.StatusBadRequest, wantOutcome: "rejected",
			wantErrorCode: app_errors.ErrBadRequest.Code,
		},
		{
			name: "blocked",
			handler: func(c *gin.Context) {
				writeServiceErrorResponse(
					c,
					"probe",
					app_errors.ErrControlRecoveryPending,
				)
			},
			wantStatus: http.StatusServiceUnavailable, wantOutcome: "blocked",
			wantErrorCode: app_errors.ErrControlRecoveryPending.Code,
		},
		{
			name: "incomplete",
			handler: func(c *gin.Context) {
				writeServiceErrorResponse(
					c,
					"probe",
					app_errors.ErrControlOperationIncomplete,
				)
			},
			wantStatus: http.StatusServiceUnavailable, wantOutcome: "incomplete",
			wantErrorCode: app_errors.ErrControlOperationIncomplete.Code,
		},
		{
			name: "failed",
			handler: func(c *gin.Context) {
				writeServiceErrorResponse(
					c,
					"probe",
					errors.New("injected service failure"),
				)
			},
			wantStatus: http.StatusInternalServerError, wantOutcome: "failed",
			wantErrorCode: app_errors.ErrInternalServer.Code,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			server := NewServer(&config.Config{AuthKey: authTestKey}, nil)
			server.logger = newControlJSONLogger(&logs)
			engine := gin.New()
			engine.POST(
				"/probe",
				func(c *gin.Context) {
					c.Set(controlPeerContextKey, "192.0.2.70")
				},
				server.auditMutation(newMutationDescriptor(
					"probe_update",
					"probe",
					staticMutationLocator("probe:1"),
				)),
				test.handler,
			)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/probe", nil)
			engine.ServeHTTP(recorder, request)

			if recorder.Code != test.wantStatus {
				t.Fatalf(
					"response = %d %s, want %d",
					recorder.Code,
					recorder.Body.String(),
					test.wantStatus,
				)
			}
			events := controlEventsNamed(
				decodeControlJSONLogs(t, logs.Bytes()),
				"control_plane_mutation",
			)
			if len(events) != 1 {
				t.Fatalf("mutation events = %#v, want one", events)
			}
			event := events[0]
			wantLevel := "warning"
			if test.wantOutcome == "succeeded" {
				wantLevel = "info"
			}
			assertMutationEvent(
				t,
				event,
				"probe_update",
				"probe",
				"probe:1",
				"192.0.2.70",
				test.wantOutcome,
				test.wantStatus,
				test.wantErrorCode,
				wantLevel,
			)
		})
	}
}

func TestAuditMutationUsesSuccessfulLocatorOverride(t *testing.T) {
	var logs bytes.Buffer
	server := NewServer(&config.Config{AuthKey: authTestKey}, nil)
	server.logger = newControlJSONLogger(&logs)
	engine := gin.New()
	engine.POST(
		"/probe",
		func(c *gin.Context) {
			c.Set(controlPeerContextKey, "192.0.2.71")
		},
		server.auditMutation(newMutationDescriptor(
			"probe_create",
			"probe",
			staticMutationLocator("new"),
		)),
		func(c *gin.Context) {
			setMutationResourceLocator(c, "probe:42")
			c.Status(http.StatusOK)
		},
	)

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodPost, "/probe", nil),
	)

	events := controlEventsNamed(
		decodeControlJSONLogs(t, logs.Bytes()),
		"control_plane_mutation",
	)
	if len(events) != 1 || events[0]["resource_locator"] != "probe:42" {
		t.Fatalf("mutation events = %#v, want probe:42", events)
	}
}

func TestAuditMutationLogsPanicThenRethrowsToRecovery(t *testing.T) {
	const panicSecret = "sk-panic-audit-secret"
	var logs bytes.Buffer
	server := NewServer(&config.Config{AuthKey: authTestKey}, nil)
	server.logger = newControlJSONLogger(&logs)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		defer func() {
			if recover() != nil {
				c.Status(http.StatusInternalServerError)
				c.Abort()
			}
		}()
		c.Next()
	})
	engine.POST(
		"/probe",
		func(c *gin.Context) {
			c.Set(controlPeerContextKey, "192.0.2.72")
		},
		server.auditMutation(newMutationDescriptor(
			"probe_update",
			"probe",
			staticMutationLocator("probe:1"),
		)),
		func(*gin.Context) {
			panic(panicSecret)
		},
	)

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodPost, "/probe", nil),
	)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("response = %d, want 500", recorder.Code)
	}
	events := controlEventsNamed(
		decodeControlJSONLogs(t, logs.Bytes()),
		"control_plane_mutation",
	)
	if len(events) != 1 {
		t.Fatalf("mutation events = %#v, want one", events)
	}
	assertMutationEvent(
		t,
		events[0],
		"probe_update",
		"probe",
		"probe:1",
		"192.0.2.72",
		"failed",
		http.StatusInternalServerError,
		app_errors.ErrInternalServer.Code,
		"warning",
	)
	assertControlLogExcludes(t, logs.String(), panicSecret, "stack")
}

func TestAuditMutationLoggerPanicDoesNotChangeResponse(t *testing.T) {
	server := NewServer(&config.Config{AuthKey: authTestKey}, nil)
	server.logger = logrus.New()
	server.logger.AddHook(controlPanicLogHook{})
	engine := gin.New()
	engine.POST(
		"/probe",
		func(c *gin.Context) {
			c.Set(controlPeerContextKey, "192.0.2.73")
		},
		server.auditMutation(newMutationDescriptor(
			"probe_update",
			"probe",
			staticMutationLocator("probe:1"),
		)),
		func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		},
	)

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodPost, "/probe", nil),
	)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("response = %d %s, want 204", recorder.Code, recorder.Body.String())
	}
}

func TestMutationAuditExcludesReadAndDiscoveryRoutes(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	var logs bytes.Buffer
	server := NewServer(
		&config.Config{AuthKey: authTestKey},
		fixture.service,
	)
	server.logger = newControlJSONLogger(&logs)
	engine := gin.New()
	server.RegisterRoutes(engine)

	requests := []struct {
		method string
		path   string
		body   string
		auth   bool
	}{
		{method: http.MethodGet, path: "/api/health", auth: true},
		{method: http.MethodGet, path: "/api/groups", auth: true},
		{
			method: http.MethodPost, path: "/api/route/inspect",
			body: `{}`, auth: true,
		},
		{
			method: http.MethodPost,
			path:   "/api/groups/1/models/discover",
			body:   `{}`, auth: true,
		},
		{
			method: http.MethodPost, path: "/api/models/discover",
			body: `{}`, auth: true,
		},
		{
			method: http.MethodPut, path: "/api/settings",
			body: `{}`,
		},
	}
	for _, test := range requests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(
			test.method,
			test.path,
			bytes.NewBufferString(test.body),
		)
		request.Header.Set("Content-Type", "application/json")
		if test.auth {
			request.Header.Set(
				"Authorization",
				"Bearer "+authTestKey,
			)
		}
		engine.ServeHTTP(recorder, request)
		if !test.auth && recorder.Code != http.StatusUnauthorized {
			t.Fatalf(
				"%s %s response = %d %s, want 401",
				test.method,
				test.path,
				recorder.Code,
				recorder.Body.String(),
			)
		}
	}

	events := controlEventsNamed(
		decodeControlJSONLogs(t, logs.Bytes()),
		"control_plane_mutation",
	)
	if len(events) != 0 {
		t.Fatalf("mutation events = %#v, want none", events)
	}
}

func assertMutationEvent(
	t *testing.T,
	event map[string]any,
	operation string,
	resourceType string,
	resourceLocator string,
	peerIP string,
	outcome string,
	statusCode int,
	errorCode string,
	level string,
) {
	t.Helper()
	want := map[string]any{
		"event":            "control_plane_mutation",
		"peer_ip":          peerIP,
		"operation":        operation,
		"resource_type":    resourceType,
		"resource_locator": resourceLocator,
		"outcome":          outcome,
		"status_code":      float64(statusCode),
		"error_code":       errorCode,
		"level":            level,
		"plane":            "control",
		"msg":              "[CONTROL] Mutation completed",
	}
	for field, wantValue := range want {
		if got := event[field]; got != wantValue {
			t.Errorf("%s = %#v, want %#v; event=%#v", field, got, wantValue, event)
		}
	}
	if len(event) != len(want) {
		t.Errorf("event field count = %d, want %d: %#v", len(event), len(want), event)
	}
}
