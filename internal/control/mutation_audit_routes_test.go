package control

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type mutationAuditRequest struct {
	method         string
	path           string
	body           string
	idempotencyKey string
}

type groupMutationAuditCase struct {
	operation string
	success   func(*testing.T, serviceFixture) (mutationAuditRequest, string)
	rejected  func(*testing.T, serviceFixture) (mutationAuditRequest, string, string)
	database  func(*testing.T, serviceFixture) (mutationAuditRequest, string)
}

func TestGroupMutationAuditRoutes(t *testing.T) {
	for _, test := range groupMutationAuditCases() {
		t.Run(test.operation+"/success", func(t *testing.T) {
			fixture := newServiceFixture(t)
			request, locator := test.success(t, fixture)
			event, status := runMutationAuditRequest(t, fixture, request)
			if status != http.StatusOK {
				t.Fatalf("response status = %d, want 200", status)
			}
			assertMutationEvent(
				t,
				event,
				test.operation,
				groupMutationResourceType(test.operation),
				locator,
				"192.0.2.1",
				"succeeded",
				http.StatusOK,
				"",
				"info",
			)
		})

		t.Run(test.operation+"/rejected", func(t *testing.T) {
			fixture := newServiceFixture(t)
			request, locator, errorCode := test.rejected(t, fixture)
			event, status := runMutationAuditRequest(t, fixture, request)
			if status < http.StatusBadRequest ||
				status >= http.StatusInternalServerError {
				t.Fatalf("response status = %d, want 4xx", status)
			}
			assertMutationEvent(
				t,
				event,
				test.operation,
				groupMutationResourceType(test.operation),
				locator,
				"192.0.2.1",
				"rejected",
				status,
				errorCode,
				"warning",
			)
		})

		t.Run(test.operation+"/database", func(t *testing.T) {
			fixture := newServiceFixture(t)
			request, locator := test.database(t, fixture)
			closeMutationAuditDB(t, fixture)
			event, status := runMutationAuditRequest(t, fixture, request)
			if status != http.StatusInternalServerError {
				t.Fatalf("response status = %d, want 500", status)
			}
			assertMutationEvent(
				t,
				event,
				test.operation,
				groupMutationResourceType(test.operation),
				locator,
				"192.0.2.1",
				"failed",
				http.StatusInternalServerError,
				app_errors.ErrDatabase.Code,
				"warning",
			)
		})
	}
}

func TestGroupMutationAuditIncompleteAndBlocked(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.reconcileRegistryGroup = func(
		uint,
		[]state.KeyEntry,
	) (bool, error) {
		return false, errors.New("injected registry failure")
	}
	var logs bytes.Buffer
	server, engine := newMutationAuditRouteServer(t, fixture, &logs)
	_ = server

	first := newMutationAuditHTTPRequest(mutationAuditRequest{
		method: http.MethodPost,
		path:   "/api/groups",
		body: groupCreateAuditBody(
			"incomplete",
			"https://incomplete.example.com",
		),
		idempotencyKey: "00000000-0000-4000-8000-00000000a001",
	})
	firstRecorder := httptest.NewRecorder()
	engine.ServeHTTP(firstRecorder, first)
	firstEvent := oneMutationAuditEvent(t, logs.Bytes())
	assertMutationEvent(
		t,
		firstEvent,
		"group_create",
		"group",
		"new",
		"192.0.2.1",
		"incomplete",
		http.StatusServiceUnavailable,
		app_errors.ErrControlOperationIncomplete.Code,
		"warning",
	)

	logs.Reset()
	second := newMutationAuditHTTPRequest(mutationAuditRequest{
		method: http.MethodPost,
		path:   "/api/groups",
		body: groupCreateAuditBody(
			"blocked",
			"https://blocked.example.com",
		),
		idempotencyKey: "00000000-0000-4000-8000-00000000a002",
	})
	secondRecorder := httptest.NewRecorder()
	engine.ServeHTTP(secondRecorder, second)
	secondEvent := oneMutationAuditEvent(t, logs.Bytes())
	assertMutationEvent(
		t,
		secondEvent,
		"group_create",
		"group",
		"new",
		"192.0.2.1",
		"blocked",
		http.StatusServiceUnavailable,
		app_errors.ErrControlRecoveryPending.Code,
		"warning",
	)
}

func TestGroupMutationAuditExcludesInternalCalls(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(
		t,
		fixture,
		"sk-direct-service",
	)
	var logs bytes.Buffer
	server := NewServer(
		&config.Config{AuthKey: authTestKey},
		fixture.service,
	)
	server.logger = newControlJSONLogger(&logs)

	_, err := fixture.service.UpdateGroup(
		t.Context(),
		groupID,
		GroupUpdateRequest{
			Name: optionalField[string]{
				Set: true, Value: "direct-update",
			},
		},
	)
	if err != nil {
		t.Fatalf("direct UpdateGroup(): %v", err)
	}
	recoveryContext, cancelRecovery := context.WithCancel(t.Context())
	cancelRecovery()
	fixture.service.RunOperationRecovery(recoveryContext)

	events := controlEventsNamed(
		decodeControlJSONLogs(t, logs.Bytes()),
		"control_plane_mutation",
	)
	if len(events) != 0 {
		t.Fatalf("mutation events = %#v, want none", events)
	}
}

func groupMutationAuditCases() []groupMutationAuditCase {
	return []groupMutationAuditCase{
		{
			operation: "group_create",
			success: func(
				_ *testing.T,
				_ serviceFixture,
			) (mutationAuditRequest, string) {
				return mutationAuditRequest{
					method: http.MethodPost,
					path:   "/api/groups",
					body: groupCreateAuditBody(
						"audit",
						"https://audit.example.com",
					),
					idempotencyKey: "00000000-0000-4000-8000-000000001001",
				}, "group:1"
			},
			rejected: func(
				_ *testing.T,
				_ serviceFixture,
			) (mutationAuditRequest, string, string) {
				return mutationAuditRequest{
					method: http.MethodPost,
					path:   "/api/groups",
					body: groupCreateAuditBody(
						"audit",
						"https://audit.example.com",
					),
				}, "new", app_errors.ErrIdempotencyKeyRequired.Code
			},
			database: func(
				_ *testing.T,
				_ serviceFixture,
			) (mutationAuditRequest, string) {
				return mutationAuditRequest{
					method: http.MethodPost,
					path:   "/api/groups",
					body: groupCreateAuditBody(
						"audit",
						"https://audit.example.com",
					),
					idempotencyKey: "00000000-0000-4000-8000-000000001002",
				}, "new"
			},
		},
		{
			operation: "group_update",
			success:   groupAuditSeedRequest(http.MethodPut, "", `{"name":"audit-renamed"}`),
			rejected: func(
				_ *testing.T,
				_ serviceFixture,
			) (mutationAuditRequest, string, string) {
				return mutationAuditRequest{
						method: http.MethodPut,
						path:   "/api/groups/not-a-number",
						body:   `{"name":"x"}`,
					},
					"group:unknown",
					app_errors.ErrBadRequest.Code
			},
			database: groupAuditSeedRequest(http.MethodPut, "", `{"name":"audit-renamed"}`),
		},
		{
			operation: "group_update_models",
			success: groupAuditSeedRequest(
				http.MethodPut,
				"/models",
				`{"models":[{"id":"gpt-4o-mini"}]}`,
			),
			rejected: func(
				_ *testing.T,
				_ serviceFixture,
			) (mutationAuditRequest, string, string) {
				return mutationAuditRequest{
						method: http.MethodPut,
						path:   "/api/groups/0/models",
						body:   `{"models":[]}`,
					},
					"group:unknown",
					app_errors.ErrBadRequest.Code
			},
			database: groupAuditSeedRequest(
				http.MethodPut,
				"/models",
				`{"models":[{"id":"gpt-4o-mini"}]}`,
			),
		},
		{
			operation: "group_delete",
			success:   groupAuditSeedRequest(http.MethodDelete, "", ""),
			rejected: func(
				_ *testing.T,
				_ serviceFixture,
			) (mutationAuditRequest, string, string) {
				return mutationAuditRequest{
						method: http.MethodDelete,
						path:   "/api/groups/-1",
					},
					"group:unknown",
					app_errors.ErrBadRequest.Code
			},
			database: groupAuditSeedRequest(http.MethodDelete, "", ""),
		},
		{
			operation: "group_key_update",
			success: groupKeyAuditSeedRequest(
				http.MethodPut,
				`{"status":"disabled"}`,
			),
			rejected: func(
				_ *testing.T,
				_ serviceFixture,
			) (mutationAuditRequest, string, string) {
				return mutationAuditRequest{
						method: http.MethodPut,
						path:   "/api/groups/raw-secret/keys/1",
						body:   `{"status":"disabled"}`,
					},
					"group:unknown/key:1",
					app_errors.ErrBadRequest.Code
			},
			database: groupKeyAuditSeedRequest(
				http.MethodPut,
				`{"status":"disabled"}`,
			),
		},
		{
			operation: "group_key_delete",
			success: groupKeyAuditSeedRequest(
				http.MethodDelete,
				"",
			),
			rejected: func(
				_ *testing.T,
				_ serviceFixture,
			) (mutationAuditRequest, string, string) {
				return mutationAuditRequest{
						method: http.MethodDelete,
						path:   "/api/groups/1/keys/raw-secret",
					},
					"group:1/key:unknown",
					app_errors.ErrBadRequest.Code
			},
			database: groupKeyAuditSeedRequest(
				http.MethodDelete,
				"",
			),
		},
		{
			operation: "group_key_import",
			success: groupKeyImportAuditSeedRequest(
				"00000000-0000-4000-8000-000000001003",
			),
			rejected: func(
				t *testing.T,
				fixture serviceFixture,
			) (mutationAuditRequest, string, string) {
				groupID := createGroupForKeyImport(
					t,
					fixture,
					"sk-import-existing",
				)
				return mutationAuditRequest{
						method: http.MethodPost,
						path: "/api/groups/" +
							strconv.FormatUint(uint64(groupID), 10) +
							"/keys/import",
						body: `{"keys":"sk-new-audit-key"}`,
					},
					fmt.Sprintf("group:%d/keys", groupID),
					app_errors.ErrIdempotencyKeyRequired.Code
			},
			database: groupKeyImportAuditSeedRequest(
				"00000000-0000-4000-8000-000000001004",
			),
		},
	}
}

func groupCreateAuditBody(name string, upstreamURL string) string {
	return fmt.Sprintf(
		`{"name":%q,"upstream_url":%q,`+
			`"protocols":["openai"],`+
			`"models":[{"id":"gpt-4o"}],`+
			`"config":{},"keys":"sk-audit-upstream"}`,
		name,
		upstreamURL,
	)
}

func groupAuditSeedRequest(
	method string,
	suffix string,
	body string,
) func(*testing.T, serviceFixture) (mutationAuditRequest, string) {
	return func(
		t *testing.T,
		fixture serviceFixture,
	) (mutationAuditRequest, string) {
		groupID := createGroupForKeyImport(
			t,
			fixture,
			"sk-group-audit",
		)
		locator := fmt.Sprintf("group:%d", groupID)
		return mutationAuditRequest{
			method: method,
			path: "/api/groups/" +
				strconv.FormatUint(uint64(groupID), 10) +
				suffix,
			body: body,
		}, locator
	}
}

func groupKeyAuditSeedRequest(
	method string,
	body string,
) func(*testing.T, serviceFixture) (mutationAuditRequest, string) {
	return func(
		t *testing.T,
		fixture serviceFixture,
	) (mutationAuditRequest, string) {
		groupID := createGroupForKeyImport(
			t,
			fixture,
			"sk-group-key-audit",
		)
		var key models.UpstreamKey
		if err := fixture.db.Where("group_id = ?", groupID).
			First(&key).Error; err != nil {
			t.Fatalf("query seeded key: %v", err)
		}
		locator := fmt.Sprintf(
			"group:%d/key:%d",
			groupID,
			key.ID,
		)
		return mutationAuditRequest{
			method: method,
			path: fmt.Sprintf(
				"/api/groups/%d/keys/%d",
				groupID,
				key.ID,
			),
			body: body,
		}, locator
	}
}

func groupKeyImportAuditSeedRequest(
	idempotencyKey string,
) func(*testing.T, serviceFixture) (mutationAuditRequest, string) {
	return func(
		t *testing.T,
		fixture serviceFixture,
	) (mutationAuditRequest, string) {
		groupID := createGroupForKeyImport(
			t,
			fixture,
			"sk-import-audit",
		)
		return mutationAuditRequest{
			method: http.MethodPost,
			path: fmt.Sprintf(
				"/api/groups/%d/keys/import",
				groupID,
			),
			body:           `{"keys":"sk-new-audit-key"}`,
			idempotencyKey: idempotencyKey,
		}, fmt.Sprintf("group:%d/keys", groupID)
	}
}

func groupMutationResourceType(operation string) string {
	if operation == "group_key_update" ||
		operation == "group_key_delete" ||
		operation == "group_key_import" {
		return "group_key"
	}
	return "group"
}

func runMutationAuditRequest(
	t *testing.T,
	fixture serviceFixture,
	request mutationAuditRequest,
) (map[string]any, int) {
	t.Helper()
	var logs bytes.Buffer
	_, engine := newMutationAuditRouteServer(t, fixture, &logs)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, newMutationAuditHTTPRequest(request))
	return oneMutationAuditEvent(t, logs.Bytes()), recorder.Code
}

func newMutationAuditRouteServer(
	t *testing.T,
	fixture serviceFixture,
	logs *bytes.Buffer,
) (*Server, *gin.Engine) {
	t.Helper()
	initControlI18n(t)
	server := NewServer(
		&config.Config{AuthKey: authTestKey},
		fixture.service,
	)
	server.logger = newControlJSONLogger(logs)
	engine := gin.New()
	server.RegisterRoutes(engine)
	return server, engine
}

func newMutationAuditHTTPRequest(
	request mutationAuditRequest,
) *http.Request {
	httpRequest := httptest.NewRequest(
		request.method,
		request.path,
		bytes.NewBufferString(request.body),
	)
	httpRequest.Header.Set("Authorization", "Bearer "+authTestKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	if request.idempotencyKey != "" {
		httpRequest.Header.Set(
			"Idempotency-Key",
			request.idempotencyKey,
		)
	}
	return httpRequest
}

func oneMutationAuditEvent(
	t *testing.T,
	logs []byte,
) map[string]any {
	t.Helper()
	events := controlEventsNamed(
		decodeControlJSONLogs(t, logs),
		"control_plane_mutation",
	)
	if len(events) != 1 {
		t.Fatalf("mutation events = %#v, want one", events)
	}
	return events[0]
}

func closeMutationAuditDB(t *testing.T, fixture serviceFixture) {
	t.Helper()
	sqlDB, err := fixture.db.DB()
	if err != nil {
		t.Fatalf("fixture DB(): %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close fixture DB: %v", err)
	}
}
