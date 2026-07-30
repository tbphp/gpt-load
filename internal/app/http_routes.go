package app

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/httproute"
	"gpt-load/internal/platform/version"
)

// HTTPModule declares process-level HTTP endpoints.
func HTTPModule() httproute.Module {
	return httproute.Module{
		Name:              "system",
		Owner:             httproute.OwnerSystem,
		Auth:              httproute.AuthNone,
		NamespacePrefixes: []string{"/health"},
		Routes: []httproute.Route{
			{
				Name:    "system.health",
				Methods: []string{http.MethodGet},
				Path:    "/health",
				Handlers: gin.HandlersChain{
					func(c *gin.Context) {
						c.JSON(http.StatusOK, gin.H{
							"status":  "ok",
							"version": version.Version,
						})
					},
				},
			},
		},
	}
}
