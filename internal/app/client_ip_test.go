package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/utils"
)

func TestEngineResolvesClientIPBeforeRoutes(t *testing.T) {
	engine, err := NewEngineWithLifecycle(nil, &config.Config{
		ClientIPHeader: "CF-Connecting-IP", TrustedProxies: []string{"192.0.2.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	engine.GET("/probe", func(c *gin.Context) {
		ip, err := utils.ClientIP(c.Request)
		if err != nil {
			t.Fatal(err)
		}
		if c.Request.RemoteAddr != "192.0.2.10:1234" {
			t.Fatal("connection address was overwritten")
		}
		c.String(http.StatusOK, ip)
	})
	request := httptest.NewRequest(http.MethodGet, "/probe", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Set("CF-Connecting-IP", "2001:db8::5")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "2001:db8::5" {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}
