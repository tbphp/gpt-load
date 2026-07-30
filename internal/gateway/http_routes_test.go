package gateway

import (
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/httproute"
)

func bindGatewayRoutesForTest(
	t *testing.T,
	engine *gin.Engine,
	handler *Handler,
) {
	t.Helper()
	registry, err := httproute.NewRegistry(handler.HTTPModule())
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if err := registry.Bind(engine); err != nil {
		t.Fatalf("Bind() error = %v", err)
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
