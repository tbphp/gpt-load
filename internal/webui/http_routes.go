package webui

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/httproute"
)

// HTTPModule declares the management UI pages, static assets, and SPA fallback.
func (s *Server) HTTPModule() httproute.Module {
	routes := make([]httproute.Route, 0, len(s.pages)+3)
	for _, page := range s.pages {
		routes = append(routes, httproute.Route{
			Name:     "web.page." + page.Name,
			Methods:  []string{http.MethodGet},
			Path:     page.Path,
			Handlers: gin.HandlersChain{s.serveIndex},
		})
	}
	routes = append(
		routes,
		httproute.Route{
			Name:     "web.asset.favicon",
			Methods:  []string{http.MethodGet},
			Path:     "/favicon.ico",
			Handlers: gin.HandlersChain{s.serveFavicon},
		},
		httproute.Route{
			Name:     "web.asset.theme-bootstrap",
			Methods:  []string{http.MethodGet},
			Path:     "/theme-bootstrap.js",
			Handlers: gin.HandlersChain{s.serveThemeBootstrap},
		},
		httproute.Route{
			Name:     "web.assets",
			Methods:  []string{http.MethodGet},
			Path:     "/assets/*filepath",
			Handlers: gin.HandlersChain{s.serveAsset},
		},
	)

	return httproute.Module{
		Name:  "web",
		Owner: httproute.OwnerWeb,
		Auth:  httproute.AuthNone,
		NamespacePrefixes: []string{
			"/assets",
			"/favicon.ico",
			"/theme-bootstrap.js",
		},
		Routes: routes,
		Fallback: &httproute.Fallback{
			Name:    "web.spa-not-found",
			Match:   isBrowserNavigation,
			Handler: s.serveNotFoundIndex,
		},
	}
}

func isBrowserNavigation(request *http.Request) bool {
	return request != nil &&
		request.Method == http.MethodGet &&
		acceptsHTML(request.Header.Get("Accept"))
}
