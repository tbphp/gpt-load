package control

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/storage/models"
)

const defaultOAuthCallbackAddress = "127.0.0.1:1455"

const oauthResultScript = `const button = document.getElementById("close-and-return")
const hint = document.getElementById("close-hint")
button.addEventListener("click", () => {
  if (window.opener && !window.opener.closed) window.opener.focus()
  window.close()
  window.setTimeout(() => {
    hint.hidden = false
    button.disabled = true
    button.textContent = "请手动关闭此页面"
  }, 100)
})`

type oauthCallbackParameters struct {
	State         string
	Code          string
	ProviderError string
}

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
	if request.URL.Path != "/auth/callback" {
		http.NotFound(writer, request)
		return
	}
	callback, err := parseOAuthCallbackQuery(request.URL.Query())
	if err != nil {
		writeOAuthResult(writer, false)
		return
	}
	if callback.ProviderError != "" {
		_ = server.service.FailCredentialAuthorization(request.Context(), callback.State, callback.ProviderError)
		writeOAuthResult(writer, false)
		return
	}
	if _, err := server.service.CompleteCredentialAuthorization(request.Context(), callback.State, callback.Code); err != nil {
		writeOAuthResult(writer, false)
		return
	}
	writeOAuthResult(writer, true)
}

func parseManualOAuthCallbackURL(raw string) (oauthCallbackParameters, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 16*1024 {
		return oauthCallbackParameters{}, errors.New("invalid OAuth callback URL")
	}
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || !strings.EqualFold(parsed.Scheme, "http") ||
		parsed.User != nil || parsed.Fragment != "" || !strings.EqualFold(parsed.Hostname(), "localhost") ||
		parsed.Port() != "1455" || parsed.EscapedPath() != "/auth/callback" {
		return oauthCallbackParameters{}, errors.New("invalid OAuth callback URL")
	}
	return parseOAuthCallbackQuery(parsed.Query())
}

func parseOAuthCallbackQuery(query url.Values) (oauthCallbackParameters, error) {
	stateValues, codeValues, errorValues := query["state"], query["code"], query["error"]
	if len(stateValues) != 1 || strings.TrimSpace(stateValues[0]) == "" {
		return oauthCallbackParameters{}, errors.New("invalid OAuth callback state")
	}
	if len(errorValues) == 1 && strings.TrimSpace(errorValues[0]) != "" && len(codeValues) == 0 {
		if descriptions := query["error_description"]; len(descriptions) > 1 {
			return oauthCallbackParameters{}, errors.New("invalid OAuth callback error")
		}
		return oauthCallbackParameters{State: stateValues[0], ProviderError: errorValues[0]}, nil
	}
	if len(codeValues) != 1 || strings.TrimSpace(codeValues[0]) == "" || len(errorValues) != 0 || len(query["error_description"]) != 0 {
		return oauthCallbackParameters{}, errors.New("invalid OAuth callback code")
	}
	return oauthCallbackParameters{State: stateValues[0], Code: codeValues[0]}, nil
}

func setOAuthCallbackHeaders(writer http.ResponseWriter) {
	scriptHash := sha256.Sum256([]byte(oauthResultScript))
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Pragma", "no-cache")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'sha256-"+base64.StdEncoding.EncodeToString(scriptHash[:])+"'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
}

func writeOAuthResult(writer http.ResponseWriter, success bool) {
	stateClass := "failure"
	title := "授权失败"
	message := "未能连接订阅账号，请返回 GPT-Load 后重试。"
	icon := `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 8l8 8M16 8l-8 8"/></svg>`
	if success {
		stateClass = "success"
		title = "授权成功"
		message = "认证信息已准备好，请返回 GPT-Load 完成账号连接。"
		icon = `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6.5 12.5 3.5 3.5 7.5-8"/></svg>`
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Content-Language", "zh-CN")
	writer.WriteHeader(http.StatusOK)
	page := `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>` + title + `</title>
  <style>
    :root { color-scheme: light; font-family: Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    * { box-sizing: border-box; }
    body { margin: 0; min-height: 100vh; min-height: 100dvh; display: grid; place-items: center; background: #f5f7fb; color: #172033; }
    .shell { width: min(100%, 34rem); padding: 1.5rem; }
    .card { padding: clamp(1.75rem, 6vw, 2.75rem); text-align: center; background: #fff; border: 1px solid #dfe4ec; border-radius: 1.25rem; }
    .status-icon { display: grid; place-items: center; width: 3.75rem; height: 3.75rem; margin: 0 auto 1.25rem; border-radius: 999px; }
    .status-icon svg { width: 1.75rem; height: 1.75rem; fill: none; stroke: currentColor; stroke-width: 2.25; stroke-linecap: round; stroke-linejoin: round; }
    .success .status-icon { color: #15803d; background: #dcfce7; }
    .failure .status-icon { color: #b42318; background: #fee4e2; }
    .eyebrow { margin: 0 0 .5rem; color: #64748b; font-size: .75rem; font-weight: 700; letter-spacing: .08em; text-transform: uppercase; }
    h1 { margin: 0; font-size: clamp(1.6rem, 5vw, 2rem); line-height: 1.25; letter-spacing: -.02em; }
    .message { max-width: 27rem; margin: 1rem auto 0; color: #475569; font-size: 1rem; line-height: 1.7; }
    .url-note { margin: .75rem 0 0; color: #64748b; font-size: .875rem; line-height: 1.6; }
    .actions { margin-top: 1.75rem; }
    .close-button { min-width: 11rem; min-height: 3rem; padding: .75rem 1.25rem; border: 0; border-radius: .75rem; color: #fff; background: #2563eb; font: inherit; font-weight: 650; cursor: pointer; transition: background-color .18s ease; }
    .close-button:hover { background: #1d4ed8; }
    .close-button:focus-visible { outline: 3px solid #93c5fd; outline-offset: 3px; }
    .close-button:disabled { cursor: default; background: #64748b; }
    .close-hint { margin: .75rem 0 0; color: #64748b; font-size: .8125rem; line-height: 1.5; }
    [hidden] { display: none; }
    @media (max-width: 30rem) { .shell { padding: 1rem; } .card { border-radius: 1rem; } }
    @media (prefers-reduced-motion: reduce) { .close-button { transition: none; } }
  </style>
</head>
<body>
  <main class="shell">
    <section class="card ` + stateClass + `" aria-labelledby="result-title" aria-describedby="result-message">
      <div class="status-icon">` + icon + `</div>
      <p class="eyebrow">Codex OAuth</p>
      <h1 id="result-title">` + title + `</h1>
      <p id="result-message" class="message">` + message + `</p>
      <p class="url-note">当前回调 URL 已保留在浏览器地址栏中。</p>
      <div class="actions">
        <button id="close-and-return" class="close-button" type="button">关闭并返回</button>
        <p id="close-hint" class="close-hint" hidden>浏览器未允许自动关闭，请手动关闭此页面并返回 GPT-Load。</p>
      </div>
    </section>
  </main>
  <script>` + oauthResultScript + `</script>
</body>
</html>`
	_, _ = io.WriteString(writer, page)
}
