package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/catalog"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/platform/utils"
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

func TestModelPriceSyncMutationAudit(t *testing.T) {
	t.Parallel()
	t.Run("success", func(t *testing.T) {
		fixture := newServiceFixture(t)
		fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{}})
		client := catalogSyncClientFunc(func(context.Context, catalog.Metadata) (catalog.SyncResult, error) {
			return catalog.SyncResult{
				Metadata: catalog.Metadata{
					CheckedAtMillis:         20,
					SuccessfulFetchAtMillis: 10,
				},
				NotModified: true,
			}, nil
		})
		newCatalogSyncCoordinator(fixture.service, client, "unused", catalog.Metadata{
			CheckedAtMillis: 10, SuccessfulFetchAtMillis: 10,
		}, true)

		var logs bytes.Buffer
		_, engine := newMutationAuditRouteServer(t, fixture, &logs)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, newMutationAuditHTTPRequest(mutationAuditRequest{
			method: http.MethodPost,
			path:   "/api/model-prices/sync",
		}))
		if recorder.Code != http.StatusOK {
			t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
		}
		assertMutationEvent(
			t,
			oneMutationAuditEvent(t, logs.Bytes()),
			"model_prices_sync",
			"model_price",
			"model-prices:catalog",
			"192.0.2.1",
			"succeeded",
			http.StatusOK,
			"",
			"info",
		)
	})

	t.Run("upstream failure", func(t *testing.T) {
		fixture := newServiceFixture(t)
		const rawFailure = "secret catalog transport response"
		client := catalogSyncClientFunc(func(context.Context, catalog.Metadata) (catalog.SyncResult, error) {
			return catalog.SyncResult{}, errors.New(rawFailure)
		})
		newCatalogSyncCoordinator(fixture.service, client, "unused", catalog.Metadata{}, false)

		var logs bytes.Buffer
		_, engine := newMutationAuditRouteServer(t, fixture, &logs)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, newMutationAuditHTTPRequest(mutationAuditRequest{
			method: http.MethodPost,
			path:   "/api/model-prices/sync",
		}))
		if recorder.Code != http.StatusBadGateway {
			t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
		}
		assertMutationEvent(
			t,
			oneMutationAuditEvent(t, logs.Bytes()),
			"model_prices_sync",
			"model_price",
			"model-prices:catalog",
			"192.0.2.1",
			"failed",
			http.StatusBadGateway,
			app_errors.ErrBadGateway.Code,
			"warning",
		)
		assertControlLogExcludes(t, logs.String(), rawFailure)
		if strings.Contains(recorder.Body.String(), rawFailure) {
			t.Fatalf("response leaked raw failure: %s", recorder.Body.String())
		}
	})
}

func TestGroupMutationAuditRoutes(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	fixture := newServiceFixture(t)
	fixture.service.reconcileRegistryGroup = func(
		uint,
		[]state.CredentialEntry,
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
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(
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

	_, err := fixture.service.UpdateGroupSettings(
		t.Context(),
		groupID,
		GroupSettingsUpdateRequest{
			Name: optionalField[string]{
				Set: true, Value: "direct-update",
			},
		},
	)
	if err != nil {
		t.Fatalf("direct UpdateGroupSettings(): %v", err)
	}
	recoveryContext, cancelRecovery := context.WithCancel(t.Context())
	cancelRecovery()
	fixture.service.RunOperationRecovery(recoveryContext)

	events := controlEventsNamed(
		decodeControlJSONLogs(t, logs.Bytes()),
		"mutation",
	)
	if len(events) != 0 {
		t.Fatalf("mutation events = %#v, want none", events)
	}
}

func TestSettingsMutationAudit(t *testing.T) {
	t.Parallel()
	t.Run("success", func(t *testing.T) {
		fixture := newServiceFixture(t)
		var logs bytes.Buffer
		_, engine := newMutationAuditRouteServer(t, fixture, &logs)
		updated := serveSettingsRequest(
			t, engine, http.MethodPut, authTestKey, `{"settings":{"request_timeout":900}}`,
		)
		if updated.Code != http.StatusOK {
			t.Fatalf(
				"PUT settings = %d %s",
				updated.Code,
				updated.Body.String(),
			)
		}
		assertMutationEvent(
			t,
			oneMutationAuditEvent(t, logs.Bytes()),
			"settings_update",
			"settings",
			"settings:global",
			"192.0.2.1",
			"succeeded",
			http.StatusOK,
			"",
			"info",
		)
	})

	t.Run("rejected", func(t *testing.T) {
		fixture := newServiceFixture(t)
		event, status := runMutationAuditRequest(
			t,
			fixture,
			mutationAuditRequest{
				method: http.MethodPut,
				path:   "/api/settings",
				body:   `{"settings":{"unknown":true}}`,
			},
		)
		assertMutationEvent(
			t,
			event,
			"settings_update",
			"settings",
			"settings:global",
			"192.0.2.1",
			"rejected",
			status,
			app_errors.ErrValidation.Code,
			"warning",
		)
	})

	t.Run("database", func(t *testing.T) {
		fixture := newServiceFixture(t)
		var logs bytes.Buffer
		_, engine := newMutationAuditRouteServer(t, fixture, &logs)
		closeMutationAuditDB(t, fixture)
		updated := serveSettingsRequest(
			t, engine, http.MethodPut, authTestKey, `{"settings":{"request_timeout":900}}`,
		)
		assertMutationEvent(
			t,
			oneMutationAuditEvent(t, logs.Bytes()),
			"settings_update",
			"settings",
			"settings:global",
			"192.0.2.1",
			"failed",
			updated.Code,
			app_errors.ErrDatabase.Code,
			"warning",
		)
	})
}

func TestAccessKeyMutationAudit(t *testing.T) {
	t.Parallel()
	t.Run("create and replay", func(t *testing.T) {
		fixture := newServiceFixture(t)
		fixture.service.random = bytes.NewReader(
			bytes.Repeat([]byte{0xab}, 16),
		)
		var logs bytes.Buffer
		_, engine := newMutationAuditRouteServer(t, fixture, &logs)
		request := mutationAuditRequest{
			method:         http.MethodPost,
			path:           "/api/access-keys",
			body:           `{"name":"audit-client"}`,
			idempotencyKey: "00000000-0000-4000-8000-000000002001",
		}

		firstRecorder := httptest.NewRecorder()
		engine.ServeHTTP(
			firstRecorder,
			newMutationAuditHTTPRequest(request),
		)
		if firstRecorder.Code != http.StatusOK ||
			firstRecorder.Header().Get("Cache-Control") != "no-store" ||
			firstRecorder.Header().Get("Pragma") != "no-cache" {
			t.Fatalf(
				"first create = %d headers=%v body=%s",
				firstRecorder.Code,
				firstRecorder.Header(),
				firstRecorder.Body.String(),
			)
		}
		var firstEnvelope struct {
			Data AccessKeyCreateResult `json:"data"`
		}
		if err := json.Unmarshal(
			firstRecorder.Body.Bytes(),
			&firstEnvelope,
		); err != nil {
			t.Fatalf("decode first create: %v", err)
		}
		if firstEnvelope.Data.ID == 0 ||
			firstEnvelope.Data.Key == "" ||
			firstEnvelope.Data.Replayed {
			t.Fatalf("first create data = %#v", firstEnvelope.Data)
		}

		replayRecorder := httptest.NewRecorder()
		engine.ServeHTTP(
			replayRecorder,
			newMutationAuditHTTPRequest(request),
		)
		if replayRecorder.Code != http.StatusOK {
			t.Fatalf(
				"replay = %d %s",
				replayRecorder.Code,
				replayRecorder.Body.String(),
			)
		}
		var replayEnvelope struct {
			Data AccessKeyCreateResult `json:"data"`
		}
		if err := json.Unmarshal(
			replayRecorder.Body.Bytes(),
			&replayEnvelope,
		); err != nil {
			t.Fatalf("decode replay: %v", err)
		}
		if !replayEnvelope.Data.Replayed ||
			replayEnvelope.Data.ID != firstEnvelope.Data.ID ||
			replayEnvelope.Data.Key != "" {
			t.Fatalf("replay data = %#v", replayEnvelope.Data)
		}

		events := controlEventsNamed(
			decodeControlJSONLogs(t, logs.Bytes()),
			"mutation",
		)
		if len(events) != 2 {
			t.Fatalf("mutation events = %#v, want two", events)
		}
		for _, event := range events {
			assertMutationEvent(
				t,
				event,
				"access_key_create",
				"access_key",
				fmt.Sprintf(
					"access-key:%d",
					firstEnvelope.Data.ID,
				),
				"192.0.2.1",
				"succeeded",
				http.StatusOK,
				"",
				"info",
			)
		}
		assertAccessKeyAuditLogExcludes(
			t,
			logs.String(),
			firstEnvelope.Data.Key,
		)
	})

	t.Run("update success", func(t *testing.T) {
		fixture := newServiceFixture(t)
		created := seedAuditAccessKey(t, fixture)
		event, status := runMutationAuditRequest(
			t,
			fixture,
			mutationAuditRequest{
				method: http.MethodPut,
				path: fmt.Sprintf(
					"/api/access-keys/%d",
					created.ID,
				),
				body: `{"name":"audit-client-updated"}`,
			},
		)
		assertMutationEvent(
			t,
			event,
			"access_key_update",
			"access_key",
			fmt.Sprintf("access-key:%d", created.ID),
			"192.0.2.1",
			"succeeded",
			status,
			"",
			"info",
		)
	})

	t.Run("delete success", func(t *testing.T) {
		fixture := newServiceFixture(t)
		created := seedAuditAccessKey(t, fixture)
		event, status := runMutationAuditRequest(
			t,
			fixture,
			mutationAuditRequest{
				method: http.MethodDelete,
				path: fmt.Sprintf(
					"/api/access-keys/%d",
					created.ID,
				),
			},
		)
		assertMutationEvent(
			t,
			event,
			"access_key_delete",
			"access_key",
			fmt.Sprintf("access-key:%d", created.ID),
			"192.0.2.1",
			"succeeded",
			status,
			"",
			"info",
		)
	})

	for _, test := range []struct {
		name      string
		operation string
		request   mutationAuditRequest
	}{
		{
			name: "create rejected", operation: "access_key_create",
			request: mutationAuditRequest{
				method: http.MethodPost,
				path:   "/api/access-keys",
				body:   `{"name":"audit-client"}`,
			},
		},
		{
			name: "update rejected", operation: "access_key_update",
			request: mutationAuditRequest{
				method: http.MethodPut,
				path:   "/api/access-keys/not-a-number",
				body:   `{"name":"updated"}`,
			},
		},
		{
			name: "delete rejected", operation: "access_key_delete",
			request: mutationAuditRequest{
				method: http.MethodDelete,
				path:   "/api/access-keys/0",
			},
		},
		{
			name: "reveal rejected", operation: "access_key_reveal",
			request: mutationAuditRequest{
				method: http.MethodPost,
				path:   "/api/access-keys/raw-secret/reveal",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			event, status := runMutationAuditRequest(
				t,
				fixture,
				test.request,
			)
			errorCode := app_errors.ErrBadRequest.Code
			locator := "access-key:unknown"
			if test.operation == "access_key_create" {
				errorCode =
					app_errors.ErrIdempotencyKeyRequired.Code
				locator = "new"
			}
			assertMutationEvent(
				t,
				event,
				test.operation,
				"access_key",
				locator,
				"192.0.2.1",
				"rejected",
				status,
				errorCode,
				"warning",
			)
		})
	}

	for _, test := range []struct {
		name      string
		operation string
		request   func(*testing.T, serviceFixture) (
			mutationAuditRequest,
			string,
		)
	}{
		{
			name: "create database", operation: "access_key_create",
			request: func(
				_ *testing.T,
				_ serviceFixture,
			) (mutationAuditRequest, string) {
				return mutationAuditRequest{
					method: http.MethodPost,
					path:   "/api/access-keys",
					body:   `{"name":"audit-client"}`,
					idempotencyKey: "00000000-0000-4000-8000-" +
						"000000002002",
				}, "new"
			},
		},
		{
			name: "update database", operation: "access_key_update",
			request: accessKeyAuditSeedRequest(
				http.MethodPut,
				`{"name":"audit-client-updated"}`,
				"",
			),
		},
		{
			name: "delete database", operation: "access_key_delete",
			request: accessKeyAuditSeedRequest(
				http.MethodDelete,
				"",
				"",
			),
		},
		{
			name: "reveal database", operation: "access_key_reveal",
			request: accessKeyAuditSeedRequest(
				http.MethodPost,
				"",
				"/reveal",
			),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			request, locator := test.request(t, fixture)
			closeMutationAuditDB(t, fixture)
			event, status := runMutationAuditRequest(
				t,
				fixture,
				request,
			)
			assertMutationEvent(
				t,
				event,
				test.operation,
				"access_key",
				locator,
				"192.0.2.1",
				"failed",
				status,
				app_errors.ErrDatabase.Code,
				"warning",
			)
		})
	}
}

func TestAccessKeyRevealAudit(t *testing.T) {
	t.Parallel()
	t.Run("success excludes credential", func(t *testing.T) {
		fixture := newServiceFixture(t)
		created := seedAuditAccessKey(t, fixture)
		var logs bytes.Buffer
		_, engine := newMutationAuditRouteServer(t, fixture, &logs)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(
			recorder,
			newMutationAuditHTTPRequest(mutationAuditRequest{
				method: http.MethodPost,
				path: fmt.Sprintf(
					"/api/access-keys/%d/reveal",
					created.ID,
				),
			}),
		)
		if recorder.Code != http.StatusOK ||
			recorder.Header().Get("Cache-Control") != "no-store" ||
			recorder.Header().Get("Pragma") != "no-cache" {
			t.Fatalf(
				"reveal = %d headers=%v body=%s",
				recorder.Code,
				recorder.Header(),
				recorder.Body.String(),
			)
		}
		var envelope struct {
			Data AccessKeyRevealResult `json:"data"`
		}
		if err := json.Unmarshal(
			recorder.Body.Bytes(),
			&envelope,
		); err != nil {
			t.Fatalf("decode reveal: %v", err)
		}
		if envelope.Data.ID != created.ID ||
			envelope.Data.Key != created.Key {
			t.Fatalf("reveal data = %#v", envelope.Data)
		}
		assertMutationEvent(
			t,
			oneMutationAuditEvent(t, logs.Bytes()),
			"access_key_reveal",
			"access_key",
			fmt.Sprintf("access-key:%d", created.ID),
			"192.0.2.1",
			"succeeded",
			http.StatusOK,
			"",
			"info",
		)
		assertAccessKeyAuditLogExcludes(
			t,
			logs.String(),
			created.Key,
		)
	})

	t.Run("corrupt ciphertext", func(t *testing.T) {
		fixture := newServiceFixture(t)
		created := seedAuditAccessKey(t, fixture)
		const corruptCiphertext = "known-corrupt-ciphertext"
		if err := fixture.db.Model(&models.AccessKey{}).
			Where("id = ?", created.ID).
			Update("key_value", corruptCiphertext).Error; err != nil {
			t.Fatalf("corrupt AccessKey ciphertext: %v", err)
		}
		var logs bytes.Buffer
		_, engine := newMutationAuditRouteServer(t, fixture, &logs)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(
			recorder,
			newMutationAuditHTTPRequest(mutationAuditRequest{
				method: http.MethodPost,
				path: fmt.Sprintf(
					"/api/access-keys/%d/reveal",
					created.ID,
				),
			}),
		)
		assertMutationEvent(
			t,
			oneMutationAuditEvent(t, logs.Bytes()),
			"access_key_reveal",
			"access_key",
			fmt.Sprintf("access-key:%d", created.ID),
			"192.0.2.1",
			"failed",
			recorder.Code,
			app_errors.ErrInternalServer.Code,
			"warning",
		)
		assertControlLogExcludes(
			t,
			logs.String(),
			corruptCiphertext,
			created.Key,
		)
	})
}

func TestControlSecurityEventFormatterSecretMatrix(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	const (
		authKey             = "gl-control-auth-secret-0001"
		accessKeyCanary     = "gl-client-access-secret-0002"
		providerKey         = "QZVX-provider-secret-WKJP"
		authorizationSecret = "gl-authorization-secret-0004"
		xAPISecret          = "sk-x-api-secret-0005"
		xGoogSecret         = "sk-x-goog-secret-0006"
		geminiQuerySecret   = "sk-gemini-query-secret-0007"
		headerSecret        = "sk-header-rule-secret-0008"
		requestBodySecret   = "sk-request-body-secret-0009"
		responseSecret      = "sk-response-summary-secret-0010"
		panicSecret         = "sk-panic-secret-0011"
	)
	formatters := map[string]logrus.Formatter{
		"text": &logrus.TextFormatter{
			DisableTimestamp: true,
			DisableColors:    true,
		},
		"json": &logrus.JSONFormatter{DisableTimestamp: true},
	}
	for formatterName, formatter := range formatters {
		for _, withHook := range []bool{false, true} {
			name := formatterName + "/without-hook"
			if withHook {
				name = formatterName + "/with-hook"
			}
			t.Run(name, func(t *testing.T) {
				fixture := newServiceFixture(t)
				revealed := seedAuditAccessKey(t, fixture)
				var output bytes.Buffer
				logger := logrus.New()
				logger.SetOutput(&output)
				logger.SetFormatter(formatter)
				if withHook {
					logger.AddHook(redact.NewHook(redact.New()))
				}
				server := NewServer(
					&config.Config{AuthKey: authKey},
					fixture.service,
				)
				server.logger = logger
				engine := gin.New()
				server.RegisterRoutes(engine)

				for range authFailureLimit {
					request := httptest.NewRequest(
						http.MethodGet,
						"/api/health",
						nil,
					)
					request.RemoteAddr = "192.0.2.94:1000"
					request.Header.Set(
						"Authorization",
						"Bearer "+authorizationSecret,
					)
					request.Header.Set("X-Api-Key", xAPISecret)
					request.Header.Set(
						"X-Goog-Api-Key",
						xGoogSecret,
					)
					request.Header.Set(
						"X-Access-Key",
						accessKeyCanary,
					)
					engine.ServeHTTP(httptest.NewRecorder(), request)
				}

				settingsBody := fmt.Sprintf(
					`{"settings":{"header_rules":{"set":{`+
						`"X-Provider":%q,"X-Header":%q,`+
						`"X-Request":%q,"X-Response":%q},`+
						`"remove":[]}}}`,
					providerKey,
					headerSecret,
					requestBodySecret,
					responseSecret,
				)
				updated := serveSettingsRequest(
					t, engine, http.MethodPut, authKey, settingsBody,
				)
				if updated.Code != http.StatusOK {
					t.Fatalf(
						"PUT settings = %d %s",
						updated.Code,
						updated.Body.String(),
					)
				}

				rawQuery := httptest.NewRequest(
					http.MethodPut,
					"/api/model-prices/01?search="+
						geminiQuerySecret+"&extra=value",
					strings.NewReader(modelPriceHTTPUpdateBody(`"1"`, "false")),
				)
				rawQuery.Header.Set(
					"Authorization",
					"Bearer "+authKey,
				)
				rawQuery.Header.Set("Content-Type", "application/json")
				engine.ServeHTTP(httptest.NewRecorder(), rawQuery)

				reveal := httptest.NewRequest(
					http.MethodPost,
					fmt.Sprintf(
						"/api/access-keys/%d/reveal",
						revealed.ID,
					),
					nil,
				)
				reveal.Header.Set(
					"Authorization",
					"Bearer "+authKey,
				)
				revealRecorder := httptest.NewRecorder()
				engine.ServeHTTP(revealRecorder, reveal)
				if revealRecorder.Code != http.StatusOK ||
					!strings.Contains(
						revealRecorder.Body.String(),
						revealed.Key,
					) {
					t.Fatalf(
						"reveal = %d %s",
						revealRecorder.Code,
						revealRecorder.Body.String(),
					)
				}

				panicEngine := gin.New()
				panicEngine.Use(func(c *gin.Context) {
					defer func() {
						if recover() != nil {
							c.Status(http.StatusInternalServerError)
							c.Abort()
						}
					}()
					c.Next()
				})
				panicEngine.POST(
					"/panic",
					func(c *gin.Context) {
						c.Set(
							controlPeerContextKey,
							"192.0.2.95",
						)
					},
					server.auditMutation(newMutationDescriptor(
						"panic_probe",
						"probe",
						staticMutationLocator("probe:1"),
					)),
					func(*gin.Context) {
						panic(panicSecret)
					},
				)
				panicEngine.ServeHTTP(
					httptest.NewRecorder(),
					httptest.NewRequest(
						http.MethodPost,
						"/panic",
						nil,
					),
				)

				logText := output.String()
				for _, required := range []string{
					"auth_failed",
					"auth_locked",
					"mutation",
					"settings_update",
					"settings:global",
					"access_key_reveal",
					fmt.Sprintf(
						"access-key:%d",
						revealed.ID,
					),
				} {
					if !strings.Contains(logText, required) {
						t.Fatalf(
							"log missing %q: %s",
							required,
							logText,
						)
					}
				}
				for _, forbidden := range []string{
					authKey,
					accessKeyCanary,
					revealed.Key,
					providerKey,
					providerKey[:4],
					providerKey[len(providerKey)-4:],
					utils.MaskAPIKey(providerKey),
					authorizationSecret,
					xAPISecret,
					xGoogSecret,
					geminiQuerySecret,
					headerSecret,
					requestBodySecret,
					responseSecret,
					panicSecret,
					"stack",
				} {
					if strings.Contains(logText, forbidden) {
						t.Fatalf(
							"log contains %q: %s",
							forbidden,
							logText,
						)
					}
				}
			})
		}
	}
}

func seedAuditAccessKey(
	t *testing.T,
	fixture serviceFixture,
) AccessKeyCreateResult {
	t.Helper()
	fixture.service.random = bytes.NewReader(
		bytes.Repeat([]byte{0xcd}, 16),
	)
	result, err := fixture.service.CreateAccessKey(
		t.Context(),
		AccessKeyCreateRequest{Name: "audit-client"},
	)
	if err != nil {
		t.Fatalf("seed AccessKey: %v", err)
	}
	return result
}

func accessKeyAuditSeedRequest(
	method string,
	body string,
	suffix string,
) func(*testing.T, serviceFixture) (mutationAuditRequest, string) {
	return func(
		t *testing.T,
		fixture serviceFixture,
	) (mutationAuditRequest, string) {
		created := seedAuditAccessKey(t, fixture)
		return mutationAuditRequest{
			method: method,
			path: fmt.Sprintf(
				"/api/access-keys/%d%s",
				created.ID,
				suffix,
			),
			body: body,
		}, fmt.Sprintf("access-key:%d", created.ID)
	}
}

func assertAccessKeyAuditLogExcludes(
	t *testing.T,
	logText string,
	plaintext string,
) {
	t.Helper()
	assertControlLogExcludes(
		t,
		logText,
		plaintext,
		plaintext[:4],
		plaintext[len(plaintext)-4:],
		utils.MaskAPIKey(plaintext),
	)
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
			operation: "group_settings_update",
			success: groupAuditSeedRequest(
				http.MethodPut,
				"/settings",
				`{"name":"audit-renamed"}`,
			),
			rejected: func(
				_ *testing.T,
				_ serviceFixture,
			) (mutationAuditRequest, string, string) {
				return mutationAuditRequest{
						method: http.MethodPut,
						path:   "/api/groups/not-a-number/settings",
						body:   `{"name":"x"}`,
					},
					"group:unknown",
					app_errors.ErrBadRequest.Code
			},
			database: groupAuditSeedRequest(
				http.MethodPut,
				"/settings",
				`{"name":"audit-renamed"}`,
			),
		},
		{
			operation: "group_update_models",
			success: func(t *testing.T, fixture serviceFixture) (mutationAuditRequest, string) {
				mustEnsureInitialPrices(t, fixture)
				return groupAuditSeedRequest(
					http.MethodPut,
					"/models",
					`{"models":[{"id":"gpt-4o-mini","alias_enabled":false}]}`,
				)(t, fixture)
			},
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
				`{"models":[{"id":"gpt-4o-mini","alias_enabled":false}]}`,
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
			operation: "group_credential_update",
			success: groupCredentialAuditSeedRequest(
				http.MethodPut,
				`{"status":"disabled"}`,
			),
			rejected: func(
				_ *testing.T,
				_ serviceFixture,
			) (mutationAuditRequest, string, string) {
				return mutationAuditRequest{
						method: http.MethodPut,
						path:   "/api/groups/raw-secret/credentials/1",
						body:   `{"status":"disabled"}`,
					},
					"group:unknown/credential:1",
					app_errors.ErrBadRequest.Code
			},
			database: groupCredentialAuditSeedRequest(
				http.MethodPut,
				`{"status":"disabled"}`,
			),
		},
		{
			operation: "group_credential_delete",
			success: groupCredentialAuditSeedRequest(
				http.MethodDelete,
				"",
			),
			rejected: func(
				_ *testing.T,
				_ serviceFixture,
			) (mutationAuditRequest, string, string) {
				return mutationAuditRequest{
						method: http.MethodDelete,
						path:   "/api/groups/1/credentials/raw-secret",
					},
					"group:1/credential:unknown",
					app_errors.ErrBadRequest.Code
			},
			database: groupCredentialAuditSeedRequest(
				http.MethodDelete,
				"",
			),
		},
		{
			operation: "group_credential_import",
			success: groupCredentialImportAuditSeedRequest(
				"00000000-0000-4000-8000-000000001003",
			),
			rejected: func(
				t *testing.T,
				fixture serviceFixture,
			) (mutationAuditRequest, string, string) {
				groupID := createGroupForCredentialImport(
					t,
					fixture,
					"sk-import-existing",
				)
				return mutationAuditRequest{
						method: http.MethodPost,
						path: "/api/groups/" +
							strconv.FormatUint(uint64(groupID), 10) +
							"/credentials/import",
						body: `{"credentials":"sk-new-audit-key"}`,
					},
					fmt.Sprintf("group:%d/credentials", groupID),
					app_errors.ErrIdempotencyKeyRequired.Code
			},
			database: groupCredentialImportAuditSeedRequest(
				"00000000-0000-4000-8000-000000001004",
			),
		},
	}
}

func groupCreateAuditBody(name string, upstreamURL string) string {
	return fmt.Sprintf(
		`{"name":%q,"channel_id":"openai_compatible","connection_type":"api_key","params":{"base_url":%q},`+
			`"models":[{"id":"gpt-4o","alias_enabled":false}],`+
			`"credentials":"sk-audit-upstream"}`,
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
		groupID := createGroupForCredentialImport(
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

func groupCredentialAuditSeedRequest(
	method string,
	body string,
) func(*testing.T, serviceFixture) (mutationAuditRequest, string) {
	return func(
		t *testing.T,
		fixture serviceFixture,
	) (mutationAuditRequest, string) {
		groupID := createGroupForCredentialImport(
			t,
			fixture,
			"sk-group-key-audit",
		)
		var credential models.Credential
		if err := fixture.db.Where("group_id = ?", groupID).
			First(&credential).Error; err != nil {
			t.Fatalf("query seeded credential: %v", err)
		}
		locator := fmt.Sprintf(
			"group:%d/credential:%d",
			groupID,
			credential.ID,
		)
		return mutationAuditRequest{
			method: method,
			path: fmt.Sprintf(
				"/api/groups/%d/credentials/%d",
				groupID,
				credential.ID,
			),
			body: body,
		}, locator
	}
}

func groupCredentialImportAuditSeedRequest(
	idempotencyKey string,
) func(*testing.T, serviceFixture) (mutationAuditRequest, string) {
	return func(
		t *testing.T,
		fixture serviceFixture,
	) (mutationAuditRequest, string) {
		groupID := createGroupForCredentialImport(
			t,
			fixture,
			"sk-import-audit",
		)
		return mutationAuditRequest{
			method: http.MethodPost,
			path: fmt.Sprintf(
				"/api/groups/%d/credentials/import",
				groupID,
			),
			body:           `{"credentials":"sk-new-audit-key"}`,
			idempotencyKey: idempotencyKey,
		}, fmt.Sprintf("group:%d/credentials", groupID)
	}
}

func groupMutationResourceType(operation string) string {
	if operation == "group_credential_update" ||
		operation == "group_credential_delete" ||
		operation == "group_credential_import" {
		return "group_credential"
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
		"mutation",
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
