package control

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

const defaultOAuthCallbackAddress = "127.0.0.1:1455"

const oauthResultScript = `function closeThisWindow() {
  if (window.opener && !window.opener.closed) window.opener.focus()
  window.close()
  window.setTimeout(() => {
    const hint = document.getElementById("close-hint")
    const button = document.getElementById("close-and-return")
    if (hint) hint.hidden = false
    if (button) {
      button.disabled = true
      button.textContent = "请手动关闭此页面"
    }
  }, 100)
}
const closeButton = document.getElementById("close-and-return")
if (closeButton) closeButton.addEventListener("click", closeThisWindow)
const copyButton = document.getElementById("copy-callback-url")
if (copyButton) {
  const defaultLabel = copyButton.textContent
  copyButton.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(copyButton.getAttribute("data-url") || "")
      copyButton.textContent = "已复制"
    } catch (error) {
      copyButton.textContent = "复制失败，请手动选择文本"
    }
    window.setTimeout(() => {
      copyButton.textContent = defaultLabel
    }, 2000)
  })
}
if (document.body.dataset.autoclose === "1") {
  window.setTimeout(closeThisWindow, 2000)
}`

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
	callbackURL := "http://" + request.Host + request.URL.String()
	callback, err := parseOAuthCallbackQuery(request.URL.Query())
	if err != nil {
		writeOAuthResult(writer, oauthCallbackOutcome{Kind: oauthOutcomeInvalid})
		return
	}
	if callback.ProviderError != "" {
		_ = server.service.FailCredentialAuthorization(request.Context(), callback.State, callback.ProviderError)
		writeOAuthResult(writer, oauthCallbackOutcome{Kind: oauthOutcomeDenied})
		return
	}
	result, err := server.service.CompleteCredentialAuthorization(request.Context(), callback.State, callback.Code)
	if err != nil {
		writeOAuthResult(writer, oauthCallbackOutcome{
			Kind:        classifyOAuthCallbackFailure(err),
			CallbackURL: callbackURL,
		})
		return
	}
	writeOAuthResult(writer, oauthCallbackOutcome{Kind: oauthOutcomeSuccess, AccountMask: result.Account.EmailMask})
}

// classifyOAuthCallbackFailure maps the completion error to a user-facing
// outcome. Anything not explicitly recognized is treated as an exchange
// failure, the only bucket that is honest without knowing more.
func classifyOAuthCallbackFailure(err error) oauthCallbackOutcomeKind {
	switch {
	case errors.Is(err, app_errors.ErrStagedCredentialExpired):
		return oauthOutcomeExpired
	case errors.Is(err, app_errors.ErrAuthorizationStateInvalid):
		return oauthOutcomeInvalid
	default:
		return oauthOutcomeExchangeFailed
	}
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

type oauthCallbackOutcomeKind string

const (
	oauthOutcomeSuccess        oauthCallbackOutcomeKind = "success"
	oauthOutcomeDenied         oauthCallbackOutcomeKind = "denied"
	oauthOutcomeInvalid        oauthCallbackOutcomeKind = "invalid"
	oauthOutcomeExpired        oauthCallbackOutcomeKind = "expired"
	oauthOutcomeExchangeFailed oauthCallbackOutcomeKind = "exchange_failed"
)

// oauthCallbackOutcome carries only what the result page needs to render.
// AccountMask is only ever set alongside oauthOutcomeSuccess; CallbackURL is
// only ever set alongside oauthOutcomeExchangeFailed, where copying it back
// into GPT-Load's manual field is the one path that can still recover.
type oauthCallbackOutcome struct {
	Kind        oauthCallbackOutcomeKind
	AccountMask string
	CallbackURL string
}

type oauthCallbackPresentation struct {
	Title     string
	Message   string
	Tone      string
	Icon      string
	AutoClose bool
}

func presentOAuthCallbackOutcome(outcome oauthCallbackOutcome) oauthCallbackPresentation {
	switch outcome.Kind {
	case oauthOutcomeSuccess:
		return oauthCallbackPresentation{
			Title:     "账号已连接",
			Message:   "GPT-Load 已经收到授权，这个页面可以关闭了。",
			Tone:      "success",
			Icon:      `<path d="m6.5 12.5 3.5 3.5 7.5-8"/>`,
			AutoClose: true,
		}
	case oauthOutcomeDenied:
		return oauthCallbackPresentation{
			Title:   "授权未完成",
			Message: "ChatGPT 没有完成这次授权。回到 GPT-Load 重新开始一次就行。",
			Tone:    "warning",
			Icon:    oauthWarningIconPath,
		}
	case oauthOutcomeExpired:
		return oauthCallbackPresentation{
			Title:   "授权会话已过期",
			Message: "这次授权花的时间超过了有效期。回到 GPT-Load 重新开始一次即可，之前的账号不受影响。",
			Tone:    "warning",
			Icon:    oauthWarningIconPath,
		}
	case oauthOutcomeExchangeFailed:
		return oauthCallbackPresentation{
			Title:   "换取凭据时失败",
			Message: "授权本身成功了，但服务端向 ChatGPT 换取凭据时没有成功。把下面这段网址复制回 GPT-Load 可以直接重试。",
			Tone:    "danger",
			Icon:    `<path d="M8 8l8 8M16 8l-8 8"/>`,
		}
	default:
		return oauthCallbackPresentation{
			Title:   "无法识别这次授权",
			Message: "这个授权请求的信息不完整或已失效。回到 GPT-Load 重新开始一次即可。",
			Tone:    "warning",
			Icon:    oauthWarningIconPath,
		}
	}
}

const oauthWarningIconPath = `<path d="M12 7.5v6"/><path d="M12 16.8v.1"/>`

const oauthBrandMarkSVG = `<svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><g transform="rotate(45 12 12)"><path d="M4 6.6H8V16H14.4V20H4Z"/><path d="M20 17.4H16V8H9.6V4H20Z"/></g></svg>`

const oauthResultStyle = `:root {
  color-scheme: light dark;
  --canvas: #eeede9; --surface: #ffffff; --sunken: #f5f4f1; --text: #15181b; --muted: #4e545b;
  --faint: #687078; --border-subtle: #e6e5e0; --border-control: #cfcfc9; --action: #1c4f6e;
  --action-hover: #163f58; --action-ink: #ffffff; --success: #1a6b3f; --success-bg: #e6f3ec;
  --warning: #8f6212; --warning-bg: #f8f0dd; --danger: #d03b3b; --danger-bg: #fbebe9;
}
@media (prefers-color-scheme: dark) {
  :root {
    --canvas: #0b0d10; --surface: #171b20; --sunken: #12151a; --text: #e8eaec; --muted: #969ca3;
    --faint: #858c94; --border-subtle: #232830; --border-control: #333a43; --action: #6fb2d6;
    --action-hover: #86c0de; --action-ink: #0c1a22; --success: #4fb178; --success-bg: #112a1d;
    --warning: #d5a341; --warning-bg: #241d10; --danger: #e66767; --danger-bg: #2a1613;
  }
}
* { box-sizing: border-box; }
body { margin: 0; min-height: 100vh; min-height: 100dvh; display: grid; place-items: center; background: var(--canvas); color: var(--text); font-family: system-ui, -apple-system, "Segoe UI", sans-serif; }
.shell { width: min(100%, 26rem); padding: 1.5rem; }
.card { display: grid; justify-items: center; gap: 0.85rem; padding: clamp(1.75rem, 6vw, 2.25rem) clamp(1.5rem, 6vw, 2rem); text-align: center; background: var(--surface); border: 1px solid var(--border-subtle); border-radius: 10px; box-shadow: 0 1px 2px rgba(0, 0, 0, .05), 0 12px 32px rgba(0, 0, 0, .06); }
.brand { display: inline-flex; align-items: center; gap: 6px; color: var(--faint); font-size: .75rem; font-weight: 700; letter-spacing: .06em; }
.brand svg { width: 16px; height: 16px; fill: var(--action); }
.status-icon { display: grid; place-items: center; width: 3.25rem; height: 3.25rem; border-radius: 999px; }
.status-icon svg { width: 1.5rem; height: 1.5rem; fill: none; stroke: currentColor; stroke-width: 2.25; stroke-linecap: round; stroke-linejoin: round; }
.status-icon--success { color: var(--success); background: var(--success-bg); }
.status-icon--warning { color: var(--warning); background: var(--warning-bg); }
.status-icon--danger { color: var(--danger); background: var(--danger-bg); }
h1 { margin: 0; font-size: clamp(1.35rem, 4.4vw, 1.6rem); line-height: 1.3; letter-spacing: -.01em; }
.message { max-width: 30ch; margin: 0; color: var(--muted); font-size: .95rem; line-height: 1.65; }
.account-chip code { display: inline-block; border-radius: 6px; background: var(--sunken); padding: 4px 10px; font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace; font-size: .85rem; }
.callback-url { width: 100%; display: grid; gap: .5rem; text-align: left; }
.callback-url code { display: block; overflow: hidden; border: 1px solid var(--border-subtle); border-radius: 7px; background: var(--sunken); color: var(--muted); padding: .55rem .65rem; font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace; font-size: .8rem; text-overflow: ellipsis; white-space: nowrap; }
.copy-button { justify-self: start; min-height: 2.5rem; padding: 0 1rem; border: 1px solid var(--border-control); border-radius: 7px; background: var(--surface); color: var(--text); font: inherit; font-weight: 600; cursor: pointer; }
.copy-button:hover { border-color: var(--faint); }
.actions { margin-top: .35rem; display: grid; justify-items: center; gap: .6rem; }
.close-button { min-width: 9rem; min-height: 2.75rem; padding: .6rem 1.1rem; border: 0; border-radius: 7px; color: var(--action-ink); background: var(--action); font: inherit; font-weight: 650; cursor: pointer; transition: background-color .18s ease; }
.close-button:hover { background: var(--action-hover); }
.close-button:focus-visible { outline: 3px solid var(--action); outline-offset: 3px; }
.close-button:disabled { cursor: default; background: var(--faint); }
.auto-close-note, .close-hint { margin: 0; color: var(--faint); font-size: .8rem; line-height: 1.5; }
[hidden] { display: none; }
@media (max-width: 26rem) { .shell { padding: 1rem; } .card { border-radius: 9px; } }
@media (prefers-reduced-motion: reduce) { .close-button { transition: none; } }`

func writeOAuthResult(writer http.ResponseWriter, outcome oauthCallbackOutcome) {
	presentation := presentOAuthCallbackOutcome(outcome)
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Content-Language", "zh-CN")
	writer.WriteHeader(http.StatusOK)
	autoCloseAttr := "0"
	autoCloseNote := ""
	if presentation.AutoClose {
		autoCloseAttr = "1"
		autoCloseNote = `<p class="auto-close-note">2 秒后自动关闭</p>`
	}
	accountChip := ""
	if outcome.Kind == oauthOutcomeSuccess && outcome.AccountMask != "" {
		accountChip = `<div class="account-chip"><code>` + html.EscapeString(outcome.AccountMask) + `</code></div>`
	}
	callbackURLBlock := ""
	if outcome.Kind == oauthOutcomeExchangeFailed && outcome.CallbackURL != "" {
		escapedURL := html.EscapeString(outcome.CallbackURL)
		callbackURLBlock = `<div class="callback-url">
        <code>` + escapedURL + `</code>
        <button id="copy-callback-url" type="button" class="copy-button" data-url="` + escapedURL + `">复制网址</button>
      </div>`
	}
	title := html.EscapeString(presentation.Title)
	page := `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>` + title + `</title>
  <style>` + oauthResultStyle + `</style>
</head>
<body data-autoclose="` + autoCloseAttr + `">
  <main class="shell">
    <section class="card" aria-labelledby="result-title" aria-describedby="result-message">
      <span class="brand">` + oauthBrandMarkSVG + `GPT-LOAD</span>
      <div class="status-icon status-icon--` + presentation.Tone + `"><svg viewBox="0 0 24 24" aria-hidden="true">` + presentation.Icon + `</svg></div>
      <h1 id="result-title">` + title + `</h1>
      <p id="result-message" class="message">` + html.EscapeString(presentation.Message) + `</p>
      ` + accountChip + callbackURLBlock + `
      <div class="actions">
        <button id="close-and-return" class="close-button" type="button">关闭</button>
        ` + autoCloseNote + `
        <p id="close-hint" class="close-hint" role="status" aria-live="polite" hidden>浏览器未允许自动关闭，请手动关闭此页面。</p>
      </div>
    </section>
  </main>
  <script>` + oauthResultScript + `</script>
</body>
</html>`
	_, _ = io.WriteString(writer, page)
}
