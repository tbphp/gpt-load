package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/i18n"

	"github.com/gin-gonic/gin"
)

func TestErrorUsesAPIErrorStatusAndCode(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)

	Error(context, app_errors.ErrBadRequest)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	var body ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != app_errors.ErrBadRequest.Code || body.Message != app_errors.ErrBadRequest.Message {
		t.Fatalf("response = %#v", body)
	}

	var raw map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw response: %v", err)
	}
	if _, ok := raw["data"]; ok {
		t.Fatalf("response unexpectedly includes data: %#v", raw)
	}
}

func TestErrorI18nFromAPIErrorIncludesOptionalData(t *testing.T) {
	if err := i18n.Init(); err != nil {
		t.Fatalf("i18n.Init() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	context.Request.Header.Set("Accept-Language", "en-US")
	i18n.Middleware()(context)

	apiErr := app_errors.NewAPIErrorWithData(app_errors.ErrUpstreamURLConflict, map[string]any{
		"groups": []string{"group-a", "group-b"},
	})
	ErrorI18nFromAPIError(context, apiErr, "group.upstream_url_conflict")

	var body struct {
		Code string `json:"code"`
		Data struct {
			Groups []string `json:"groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != app_errors.ErrUpstreamURLConflict.Code || len(body.Data.Groups) != 2 {
		t.Fatalf("response = %#v", body)
	}
}
