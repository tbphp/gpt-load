package control

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/storage/models"
)

func TestModelPriceMutationAuditSuccessRejectionAndFailure(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		for _, test := range []struct {
			operation string
			prepare   func(*testing.T, serviceFixture, models.ModelPrice)
			request   func(models.ModelPrice) mutationAuditRequest
		}{
			{
				operation: "model_price_update",
				prepare:   func(*testing.T, serviceFixture, models.ModelPrice) {},
				request: func(row models.ModelPrice) mutationAuditRequest {
					return mutationAuditRequest{
						method: http.MethodPut,
						path:   fmt.Sprintf("/api/model-prices/%d", row.ID),
						body:   modelPriceHTTPUpdateBody(`"1234.56789"`, "false"),
					}
				},
			},
			{
				operation: "model_price_reset",
				prepare:   func(*testing.T, serviceFixture, models.ModelPrice) {},
				request: func(row models.ModelPrice) mutationAuditRequest {
					return mutationAuditRequest{
						method: http.MethodPost,
						path:   fmt.Sprintf("/api/model-prices/%d/reset", row.ID),
						body:   `{}`,
					}
				},
			},
			{
				operation: "model_price_delete",
				prepare: func(t *testing.T, fixture serviceFixture, row models.ModelPrice) {
					if err := fixture.db.Model(&models.ModelPrice{}).Where("id = ?", row.ID).
						Update("is_manual", true).Error; err != nil {
						t.Fatal(err)
					}
				},
				request: func(row models.ModelPrice) mutationAuditRequest {
					return mutationAuditRequest{
						method: http.MethodDelete,
						path:   fmt.Sprintf("/api/model-prices/%d", row.ID),
					}
				},
			},
		} {
			t.Run(test.operation, func(t *testing.T) {
				fixture, row := newModelPriceAuditFixture(t)
				test.prepare(t, fixture, row)
				var logs bytes.Buffer
				_, engine := newMutationAuditRouteServer(t, fixture, &logs)
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, newMutationAuditHTTPRequest(test.request(row)))
				if recorder.Code != http.StatusOK {
					t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
				}
				assertMutationEvent(
					t,
					oneMutationAuditEvent(t, logs.Bytes()),
					test.operation,
					"model_price",
					fmt.Sprintf("model-price:%d", row.ID),
					"192.0.2.1",
					"succeeded",
					http.StatusOK,
					"",
					"info",
				)
				for _, secret := range []string{"provider-audit-secret", "model-audit-secret", "1234.56789"} {
					if strings.Contains(logs.String(), secret) {
						t.Fatalf("mutation audit leaked %q: %s", secret, logs.String())
					}
				}
			})
		}
	})

	t.Run("business rejection", func(t *testing.T) {
		for _, test := range []struct {
			name      string
			operation string
			request   func(models.ModelPrice) mutationAuditRequest
			locator   func(models.ModelPrice) string
			code      string
		}{
			{
				name: "update confirmation", operation: "model_price_update",
				request: func(row models.ModelPrice) mutationAuditRequest {
					return mutationAuditRequest{method: http.MethodPut, path: fmt.Sprintf("/api/model-prices/%d", row.ID), body: modelPriceHTTPUpdateBody("null", "false")}
				},
				locator: func(row models.ModelPrice) string { return fmt.Sprintf("model-price:%d", row.ID) },
				code:    "MODEL_PRICE_UNPRICED_CONFIRMATION_REQUIRED",
			},
			{
				name: "reset invalid id", operation: "model_price_reset",
				request: func(models.ModelPrice) mutationAuditRequest {
					return mutationAuditRequest{method: http.MethodPost, path: "/api/model-prices/01/reset", body: `{}`}
				},
				locator: func(models.ModelPrice) string { return "model-price:unknown" },
				code:    "BAD_REQUEST",
			},
			{
				name: "automatic delete", operation: "model_price_delete",
				request: func(row models.ModelPrice) mutationAuditRequest {
					return mutationAuditRequest{method: http.MethodDelete, path: fmt.Sprintf("/api/model-prices/%d", row.ID)}
				},
				locator: func(row models.ModelPrice) string { return fmt.Sprintf("model-price:%d", row.ID) },
				code:    "MODEL_PRICE_AUTOMATIC_DELETE_FORBIDDEN",
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				fixture, row := newModelPriceAuditFixture(t)
				request := test.request(row)
				event, status := runMutationAuditRequest(t, fixture, request)
				if status < http.StatusBadRequest || status >= http.StatusInternalServerError {
					t.Fatalf("status = %d, want 4xx", status)
				}
				assertMutationEvent(t, event, test.operation, "model_price", test.locator(row),
					"192.0.2.1", "rejected", status, test.code, "warning")
			})
		}
	})

	t.Run("database failure", func(t *testing.T) {
		for _, test := range []struct {
			operation string
			request   func(models.ModelPrice) mutationAuditRequest
		}{
			{
				operation: "model_price_update",
				request: func(row models.ModelPrice) mutationAuditRequest {
					return mutationAuditRequest{method: http.MethodPut, path: fmt.Sprintf("/api/model-prices/%d", row.ID), body: modelPriceHTTPUpdateBody(`"1"`, "false")}
				},
			},
			{
				operation: "model_price_reset",
				request: func(row models.ModelPrice) mutationAuditRequest {
					return mutationAuditRequest{method: http.MethodPost, path: fmt.Sprintf("/api/model-prices/%d/reset", row.ID), body: `{}`}
				},
			},
			{
				operation: "model_price_delete",
				request: func(row models.ModelPrice) mutationAuditRequest {
					return mutationAuditRequest{method: http.MethodDelete, path: fmt.Sprintf("/api/model-prices/%d", row.ID)}
				},
			},
		} {
			t.Run(test.operation, func(t *testing.T) {
				fixture, row := newModelPriceAuditFixture(t)
				closeMutationAuditDB(t, fixture)
				event, status := runMutationAuditRequest(t, fixture, test.request(row))
				if status != http.StatusInternalServerError {
					t.Fatalf("status = %d, want 500", status)
				}
				assertMutationEvent(t, event, test.operation, "model_price", fmt.Sprintf("model-price:%d", row.ID),
					"192.0.2.1", "failed", status, "DATABASE_ERROR", "warning")
			})
		}
	})
}

func TestModelPriceMutationAuditExcludesAuthFailureAndWrongMethod(t *testing.T) {
	fixture, row := newModelPriceAuditFixture(t)
	var logs bytes.Buffer
	_, engine := newMutationAuditRouteServer(t, fixture, &logs)

	unauthorized := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/api/model-prices/%d", row.ID),
		strings.NewReader(modelPriceHTTPUpdateBody(`"1"`, "false")),
	)
	engine.ServeHTTP(httptest.NewRecorder(), unauthorized)
	wrongMethod := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/model-prices/%d", row.ID), nil)
	engine.ServeHTTP(httptest.NewRecorder(), wrongMethod)

	events := controlEventsNamed(decodeControlJSONLogs(t, logs.Bytes()), "mutation")
	if len(events) != 0 {
		t.Fatalf("mutation events = %#v, want none", events)
	}
}

func TestModelPriceSyncStaticPathOwnsDynamicMutationMethodsBeforeAuthAndAudit(t *testing.T) {
	for _, test := range []struct {
		method        string
		body          string
		authorization string
	}{
		{
			method:        http.MethodPut,
			body:          modelPriceHTTPUpdateBody(`"1"`, "false"),
			authorization: "Bearer wrong-static-owner-key",
		},
		{
			method:        http.MethodDelete,
			authorization: "Bearer " + authTestKey,
		},
	} {
		t.Run(test.method, func(t *testing.T) {
			fixture := newServiceFixture(t)
			var logs bytes.Buffer
			_, engine := newMutationAuditRouteServer(t, fixture, &logs)
			request := httptest.NewRequest(
				test.method,
				"/api/model-prices/sync",
				strings.NewReader(test.body),
			)
			request.Header.Set("Authorization", test.authorization)
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusMethodNotAllowed {
				t.Fatalf("response = %d %s, want 405", recorder.Code, recorder.Body.String())
			}
			if allow := recorder.Header().Get("Allow"); allow != http.MethodPost {
				t.Fatalf("Allow = %q, want POST", allow)
			}
			if events := decodeControlJSONLogs(t, logs.Bytes()); len(events) != 0 {
				t.Fatalf("static ownership emitted auth/audit events: %#v", events)
			}
		})
	}
}

func newModelPriceAuditFixture(t *testing.T) (serviceFixture, models.ModelPrice) {
	t.Helper()
	fixture := newServiceFixture(t)
	input := int64(1_000_000_000)
	row := models.ModelPrice{
		ChannelID:                         string(channel.OpenAICompatible),
		ModelID:                           "model-audit-secret",
		InputPriceNanoUSDPerMillionTokens: &input,
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	return fixture, row
}
