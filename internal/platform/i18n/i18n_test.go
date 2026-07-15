package i18n

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLocalizerSelectsSupportedLanguageAndRendersTemplate(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	localizer := GetLocalizer("en-US,en;q=0.9")
	if got := T(localizer, "validation.duplicate_header", map[string]any{"key": "X-Test"}); got != "Duplicate header: X-Test" {
		t.Fatalf("T() = %q, want %q", got, "Duplicate header: X-Test")
	}
}

func TestMiddlewareStoresNormalizedLanguage(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(nil)
	context.Request = newRequestWithLanguage(t, "ja-JP,ja;q=0.9")

	Middleware()(context)

	if got := GetLangFromContext(context); got != "ja-JP" {
		t.Fatalf("GetLangFromContext() = %q, want %q", got, "ja-JP")
	}
	if got := Message(context, "common.success"); got != "成功" {
		t.Fatalf("Message() = %q, want %q", got, "成功")
	}
}

func TestUnsupportedLanguageFallsBackToChinese(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	localizer := GetLocalizer("fr-FR")
	if got := T(localizer, "common.success"); got != "操作成功" {
		t.Fatalf("fallback translation = %q, want %q", got, "操作成功")
	}
}

func newRequestWithLanguage(t *testing.T, language string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, "/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	request.Header.Set("Accept-Language", language)
	return request
}
