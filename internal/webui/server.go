// Package webui serves the embedded management UI on explicit page routes.
package webui

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	distRoot = "dist"
	indexCSP = "default-src 'self'; script-src 'self'; style-src 'self'; " +
		"style-src-elem 'self'; style-src-attr 'unsafe-inline'; " +
		"img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; " +
		"base-uri 'self'; frame-ancestors 'none'; form-action 'self'"
	fallbackIndex = `<!doctype html>
<html lang="zh-CN">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>GPT-Load</title></head>
<body><main><h1>GPT-Load</h1><p>前端资源尚未构建，请运行 make build。</p></main></body>
</html>`
)

var pageRoutes = []string{
	"/",
	"/login",
	"/import",
	"/groups/:id",
	"/access-keys",
	"/monitor",
	"/settings",
	"/settings/model-prices",
}

var backendPathPrefixes = []string{"/api", "/assets", "/health", "/v1", "/v1beta"}

//go:embed all:dist
var embeddedFiles embed.FS

// Server serves immutable assets and the SPA index for known UI routes.
type Server struct {
	files fs.FS
	root  string
	index []byte
}

// NewServer creates an embedded UI server.
func NewServer() *Server {
	return newServer(embeddedFiles, distRoot)
}

func newServer(files fs.FS, root string) *Server {
	index, err := fs.ReadFile(files, path.Join(root, "index.html"))
	if err != nil {
		index = []byte(fallbackIndex)
	}

	return &Server{files: files, root: root, index: index}
}

// RegisterRoutes registers only the documented management UI paths.
func (s *Server) RegisterRoutes(engine *gin.Engine) {
	for _, route := range pageRoutes {
		engine.GET(route, s.serveIndex)
	}
	engine.GET("/theme-bootstrap.js", s.serveThemeBootstrap)
	engine.GET("/assets/*filepath", s.serveAsset)
}

// RegisterFallback serves the SPA for browser navigation without consuming backend namespaces.
func (s *Server) RegisterFallback(engine *gin.Engine, fallback gin.HandlerFunc) {
	engine.NoRoute(func(c *gin.Context) {
		if shouldServeIndexFallback(c.Request) {
			s.serveIndex(c)
			return
		}
		fallback(c)
	})
}

func shouldServeIndexFallback(request *http.Request) bool {
	if request.Method != http.MethodGet || !acceptsHTML(request.Header.Get("Accept")) {
		return false
	}
	for _, prefix := range backendPathPrefixes {
		if request.URL.Path == prefix || strings.HasPrefix(request.URL.Path, prefix+"/") {
			return false
		}
	}
	return true
}

func acceptsHTML(value string) bool {
	for _, candidate := range strings.Split(value, ",") {
		mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(candidate))
		if err != nil || (mediaType != "text/html" && mediaType != "application/xhtml+xml") {
			continue
		}
		quality := 1.0
		if rawQuality, ok := parameters["q"]; ok {
			quality, err = strconv.ParseFloat(rawQuality, 64)
		}
		if err == nil && quality > 0 && quality <= 1 {
			return true
		}
	}
	return false
}

func (s *Server) serveIndex(c *gin.Context) {
	c.Header("Cache-Control", "no-cache")
	c.Header("Content-Security-Policy", indexCSP)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Frame-Options", "DENY")
	c.Data(http.StatusOK, "text/html; charset=utf-8", s.index)
}

func (s *Server) serveThemeBootstrap(c *gin.Context) {
	content, err := fs.ReadFile(s.files, path.Join(s.root, "theme-bootstrap.js"))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}

	c.Header("Cache-Control", "no-cache")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "text/javascript; charset=utf-8", content)
}

func (s *Server) serveAsset(c *gin.Context) {
	assetPath := path.Clean(strings.TrimPrefix(c.Param("filepath"), "/"))
	if assetPath == "." || assetPath == "" || strings.HasPrefix(assetPath, "../") {
		c.Status(http.StatusNotFound)
		return
	}

	content, err := fs.ReadFile(s.files, path.Join(s.root, "assets", assetPath))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}

	contentType := mime.TypeByExtension(path.Ext(assetPath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, contentType, content)
}
