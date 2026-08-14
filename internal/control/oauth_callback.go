package control

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/storage/models"
)

const defaultOAuthCallbackAddress = "127.0.0.1:1455"

// OAuthCallbackServer owns the fixed localhost callback required by Codex's
// OAuth client. It is deliberately separate from the authenticated /api server.
type OAuthCallbackServer struct {
	service *Service
	address string
	listen  func(string, string) (net.Listener, error)

	mu       sync.Mutex
	server   *http.Server
	listener net.Listener
}

func NewOAuthCallbackServer(service *Service) *OAuthCallbackServer {
	return &OAuthCallbackServer{service: service, address: defaultOAuthCallbackAddress, listen: net.Listen}
}

func (server *OAuthCallbackServer) configureForServerHost(host string) {
	if server == nil || (host != "0.0.0.0" && host != "::") {
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.listener == nil && server.address == defaultOAuthCallbackAddress {
		server.address = net.JoinHostPort(host, "1455")
	}
}

func (server *OAuthCallbackServer) EnsureStarted() error {
	if server == nil || server.service == nil {
		return errors.New("OAuth callback server is unavailable")
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.listener != nil {
		return nil
	}
	listener, err := server.listen("tcp", server.address)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Handler:           http.HandlerFunc(server.serveHTTP),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    32 * 1024,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	server.listener = listener
	server.server = httpServer
	go func() {
		if serveErr := httpServer.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logrus.WithField("event", "oauth.callback_serve_failed").WithError(serveErr).Error("OAuth callback listener stopped")
		}
	}()
	return nil
}

func (server *OAuthCallbackServer) Addr() string {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.listener == nil {
		return ""
	}
	return server.listener.Addr().String()
}

func (server *OAuthCallbackServer) Stop(ctx context.Context) error {
	if server == nil {
		return nil
	}
	server.mu.Lock()
	httpServer := server.server
	server.server = nil
	server.listener = nil
	server.mu.Unlock()
	if httpServer == nil {
		return nil
	}
	return httpServer.Shutdown(ctx)
}

func (server *OAuthCallbackServer) Run(ctx context.Context) {
	if server == nil || server.service == nil {
		return
	}
	var pending int64
	nowMS := server.service.now().UnixMilli()
	if err := server.service.db.WithContext(ctx).Model(&models.CredentialStage{}).
		Where("status = ? AND expires_at_ms > ?", models.CredentialStagePendingAuthorization, nowMS).
		Count(&pending).Error; err == nil && pending > 0 {
		if startErr := server.EnsureStarted(); startErr != nil {
			logrus.WithField("event", "oauth.callback_start_failed").WithError(startErr).Warn("OAuth callback listener is unavailable")
		}
	}
	<-ctx.Done()
	shutdownContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Stop(shutdownContext)
}

func (server *OAuthCallbackServer) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	setOAuthCallbackHeaders(writer)
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		http.Error(writer, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch request.URL.Path {
	case "/auth/result/success":
		writeOAuthResult(writer, true)
		return
	case "/auth/result/failure":
		writeOAuthResult(writer, false)
		return
	case "/auth/callback":
	default:
		http.NotFound(writer, request)
		return
	}
	query := request.URL.Query()
	stateValues, codeValues, errorValues := query["state"], query["code"], query["error"]
	if len(stateValues) != 1 || stateValues[0] == "" {
		http.Redirect(writer, request, "/auth/result/failure", http.StatusSeeOther)
		return
	}
	if len(errorValues) == 1 && errorValues[0] != "" && len(codeValues) == 0 {
		_ = server.service.FailCredentialAuthorization(request.Context(), stateValues[0], errorValues[0])
		http.Redirect(writer, request, "/auth/result/failure", http.StatusSeeOther)
		return
	}
	if len(codeValues) != 1 || codeValues[0] == "" || len(errorValues) != 0 {
		http.Redirect(writer, request, "/auth/result/failure", http.StatusSeeOther)
		return
	}
	if _, err := server.service.CompleteCredentialAuthorization(request.Context(), stateValues[0], codeValues[0]); err != nil {
		http.Redirect(writer, request, "/auth/result/failure", http.StatusSeeOther)
		return
	}
	http.Redirect(writer, request, "/auth/result/success", http.StatusSeeOther)
}

func setOAuthCallbackHeaders(writer http.ResponseWriter) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Pragma", "no-cache")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
}

func writeOAuthResult(writer http.ResponseWriter, success bool) {
	title, message := "授权失败", "未能连接订阅账号，请关闭此窗口后重试。"
	if success {
		title, message = "授权成功", "订阅账号已连接，可以关闭此窗口返回 GPT-Load。"
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(writer, "<!doctype html><html lang=\"zh-CN\"><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>"+title+"</title><style>body{font-family:system-ui,sans-serif;margin:0;display:grid;place-items:center;min-height:100vh;background:#f6f7f9;color:#1f2937}.card{max-width:30rem;margin:1rem;padding:2rem;border:1px solid #dfe3e8;border-radius:12px;background:white;box-shadow:0 8px 24px #0000000d}h1{font-size:1.25rem;margin:0 0 .75rem}p{margin:0;line-height:1.6;color:#5b6472}</style><main class=\"card\"><h1>"+title+"</h1><p>"+message+"</p></main></html>")
}
