// Package webui serves the embedded management UI on explicit page routes.
package webui

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
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
}

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
	engine.GET("/assets/*filepath", s.serveAsset)
}

func (s *Server) serveIndex(c *gin.Context) {
	c.Header("Cache-Control", "no-cache")
	c.Header("Content-Security-Policy", indexCSP)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Frame-Options", "DENY")
	c.Data(http.StatusOK, "text/html; charset=utf-8", s.index)
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
