package i18n

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMiddlewareSelectsJapanese(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(nil)
	context.Request = httptest.NewRequest("GET", "/", nil)
	context.Request.Header.Set("Accept-Language", "ja-JP,ja;q=0.9")

	Middleware()(context)

	if got := Message(context, "bad_request"); got != "不正なリクエスト" {
		t.Fatalf("Message() = %q, want %q", got, "不正なリクエスト")
	}
}

func TestUnsupportedLanguageFallsBackToChinese(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(nil)
	context.Request = httptest.NewRequest("GET", "/", nil)
	context.Request.Header.Set("Accept-Language", "fr-FR")
	Middleware()(context)

	if got := Message(context, "bad_request"); got != "请求错误" {
		t.Fatalf("Message() = %q, want %q", got, "请求错误")
	}
}

func TestMessageWithoutLocalizerUsesChinese(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(nil)

	if got := Message(context, "bad_request"); got != "请求错误" {
		t.Fatalf("Message() = %q, want %q", got, "请求错误")
	}
}
