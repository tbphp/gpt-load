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

func TestSuccessUsesStandardEnvelopeAndLocalizedMessage(t *testing.T) {
	if err := i18n.Init(); err != nil {
		t.Fatalf("i18n.Init() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	context.Request.Header.Set("Accept-Language", "en-US")
	i18n.Middleware()(context)

	Success(context, map[string]string{"id": "m0"})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var body struct {
		Code    int               `json:"code"`
		Message string            `json:"message"`
		Data    map[string]string `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != 0 || body.Message != "Success" || body.Data["id"] != "m0" {
		t.Fatalf("response = %#v", body)
	}
}

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
}
