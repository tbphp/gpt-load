package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/httplifecycle"
	"gpt-load/internal/platform/httproute"
)

func bindGatewayRoutesForTest(
	t *testing.T,
	engine *gin.Engine,
	handler *Handler,
) {
	t.Helper()
	engine.Use(handler.DownstreamHeadersMiddleware())
	registry, err := httproute.NewRegistry(handler.HTTPModule())
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if err := registry.Bind(engine); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
}

func TestHTTPModuleCancelsInFlightDataPlaneHandlerOnShutdown(t *testing.T) {
	coordinator := httplifecycle.NewCoordinator()
	handler := &Handler{lifecycle: coordinator}
	module := handler.HTTPModule()
	if len(module.BeforeAuth) != 1 {
		t.Fatalf("data module before-auth middleware count = %d, want 1", len(module.BeforeAuth))
	}

	engine := gin.New()
	engine.Use(coordinator.TrackAll())
	entered := make(chan struct{})
	cause := make(chan error, 1)
	engine.POST("/v1/test", module.BeforeAuth[0], func(c *gin.Context) {
		close(entered)
		<-c.Request.Context().Done()
		cause <- context.Cause(c.Request.Context())
		c.Status(http.StatusNoContent)
	})
	server := httptest.NewServer(engine)
	defer server.Close()

	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/test", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	result := make(chan error, 1)
	go func() {
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr == nil {
			defer response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				requestErr = context.Canceled
			}
		}
		result <- requestErr
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("data-plane handler was not entered")
	}

	coordinator.BeginShutdown()
	if got := <-cause; got != httplifecycle.ErrServerShutdown {
		t.Fatalf("request context cause = %v, want %v", got, httplifecycle.ErrServerShutdown)
	}
	if err := <-result; err != nil {
		t.Fatalf("data-plane request error = %v", err)
	}
}

func prepareAndAuthenticateGatewayContextForTest(
	t *testing.T,
	handler *Handler,
	ginContext *gin.Context,
	routeName string,
) {
	t.Helper()
	module := handler.HTTPModule()
	for _, route := range module.Routes {
		if route.Name != routeName {
			continue
		}
		if route.PathValidator != nil &&
			!route.PathValidator(ginContext.Request) {
			t.Fatalf("route %q rejected test request path", routeName)
		}
		for _, beforeAuth := range module.BeforeAuth {
			beforeAuth(ginContext)
		}
		for _, prepare := range route.Prepare {
			prepare(ginContext)
		}
		module.Authenticate(ginContext)
		if ginContext.IsAborted() {
			t.Fatalf(
				"route %q preparation/authentication aborted: status=%d",
				routeName,
				ginContext.Writer.Status(),
			)
		}
		return
	}
	t.Fatalf("route %q not found", routeName)
}
