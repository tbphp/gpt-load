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

	"gpt-load/internal/channel"
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
if (document.body.dataset.autoclose === "1") {
  window.setTimeout(closeThisWindow, 2000)
}`

type oauthCallbackParameters struct {
	State         string
	Code          string
	ProviderError string
}

// OAuthCallbackServer owns the restricted localhost callback requested by a
// compiled subscription driver. It is deliberately separate from the
// authenticated /api server.
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
	var pendingChannelIDs []string
	nowMS := server.service.now().UnixMilli()
	if err := server.service.db.WithContext(ctx).Model(&models.CredentialStage{}).
		Where("status = ? AND expires_at_ms > ?", models.CredentialStagePendingAuthorization, nowMS).
		Distinct("channel_id").Order("channel_id ASC").Pluck("channel_id", &pendingChannelIDs).Error; err == nil {
		for _, rawChannelID := range pendingChannelIDs {
			browser, ok := subscriptionsBrowser(server.service.subscriptions, channel.ID(rawChannelID))
			if !ok || !browser.RequiresLocalCallback() {
				continue
			}
			if startErr := server.EnsureStarted(); startErr != nil {
				logrus.WithField("event", "oauth.callback_start_failed").WithError(startErr).Warn("OAuth callback listener is unavailable")
			}
			break
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
			Kind: classifyOAuthCallbackFailure(err),
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
// AccountMask is only ever set alongside oauthOutcomeSuccess.
type oauthCallbackOutcome struct {
	Kind        oauthCallbackOutcomeKind
	AccountMask string
}

type oauthCallbackPresentation struct {
	Title     string
	Message   string
	Status    string
	Tone      string
	Icon      string
	AutoClose bool
}

func presentOAuthCallbackOutcome(outcome oauthCallbackOutcome) oauthCallbackPresentation {
	switch outcome.Kind {
	case oauthOutcomeSuccess:
		return oauthCallbackPresentation{
			Title:     "授权已完成",
			Message:   "授权信息已安全返回 GPT-Load。请返回 GPT-Load 添加账号，这个页面可以关闭了。",
			Status:    "已完成",
			Tone:      "success",
			Icon:      `<path d="m6.5 12.5 3.5 3.5 7.5-8"/>`,
			AutoClose: true,
		}
	case oauthOutcomeDenied:
		return oauthCallbackPresentation{
			Title:   "授权未完成",
			Message: "上游账号服务没有完成这次授权。回到 GPT-Load 重新开始一次即可。",
			Status:  "已取消",
			Tone:    "warning",
			Icon:    oauthWarningIconPath,
		}
	case oauthOutcomeExpired:
		return oauthCallbackPresentation{
			Title:   "授权会话已过期",
			Message: "这次授权花的时间超过了有效期。回到 GPT-Load 重新开始一次即可，之前的账号不受影响。",
			Status:  "已过期",
			Tone:    "warning",
			Icon:    oauthWarningIconPath,
		}
	case oauthOutcomeExchangeFailed:
		return oauthCallbackPresentation{
			Title:   "换取凭据时失败",
			Message: "GPT-Load 未能确认凭据交换结果。请回到 GPT-Load 查看状态，并重新发起授权；本页面的回调地址不能重复使用。",
			Status:  "需要处理",
			Tone:    "danger",
			Icon:    `<path d="M8 8l8 8M16 8l-8 8"/>`,
		}
	default:
		return oauthCallbackPresentation{
			Title:   "无法识别这次授权",
			Message: "这个授权请求的信息不完整或已失效。回到 GPT-Load 重新开始一次即可。",
			Status:  "无效请求",
			Tone:    "warning",
			Icon:    oauthWarningIconPath,
		}
	}
}

const oauthWarningIconPath = `<path d="M12 7.5v6"/><path d="M12 16.8v.1"/>`

const oauthBrandMarkSVG = `<svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><g transform="rotate(45 12 12)"><path d="M4 6.6H8V16H14.4V20H4Z"/><path d="M20 17.4H16V8H9.6V4H20Z"/></g></svg>`

const oauthResultStyle = `:root {
  color-scheme: light dark;
  --canvas: #f3f5f7; --surface: #ffffff; --sunken: #f7f8f9; --text: #17202a; --muted: #4f5d6a;
  --faint: #667481; --border-subtle: #dfe4e8; --border-control: #c7d0d8; --action: #165d7a;
  --action-hover: #104a63; --action-ink: #ffffff; --tone: #667481; --tone-bg: #eef1f3;
}
body[data-tone="success"] { --tone: #18724a; --tone-bg: #e8f5ee; }
body[data-tone="warning"] { --tone: #8a5d0b; --tone-bg: #fbf2dc; }
body[data-tone="danger"] { --tone: #b83232; --tone-bg: #fcebea; }
@media (prefers-color-scheme: dark) {
  :root {
    --canvas: #0c1015; --surface: #151a21; --sunken: #10151b; --text: #eef1f3; --muted: #abb4bd;
    --faint: #909ba6; --border-subtle: #2a323b; --border-control: #3d4853; --action: #78bddb;
    --action-hover: #91cce4; --action-ink: #0d1d25; --tone: #aab3bc; --tone-bg: #242b33;
  }
  body[data-tone="success"] { --tone: #62c38d; --tone-bg: #142c20; }
  body[data-tone="warning"] { --tone: #e0b45b; --tone-bg: #302610; }
  body[data-tone="danger"] { --tone: #ee7d7d; --tone-bg: #351918; }
}
* { box-sizing: border-box; }
body { margin: 0; min-height: 100vh; min-height: 100dvh; display: grid; place-items: center; background: var(--canvas); color: var(--text); font-family: Inter, ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif; font-size: 16px; }
button { font: inherit; }
.shell { width: min(100%, 40rem); padding: clamp(1rem, 4vw, 2rem); }
.card { position: relative; overflow: hidden; background: var(--surface); border: 1px solid var(--border-subtle); border-radius: 18px; }
.card::before { position: absolute; inset: 0 auto 0 0; width: 4px; background: var(--tone); content: ""; }
.card__header { display: flex; align-items: center; justify-content: space-between; gap: 1rem; min-height: 4rem; padding: 1rem clamp(1.25rem, 5vw, 2rem); border-bottom: 1px solid var(--border-subtle); }
.brand { display: inline-flex; align-items: center; gap: .5rem; color: var(--text); font-size: .78rem; font-weight: 750; letter-spacing: .075em; }
.brand svg { width: 18px; height: 18px; fill: var(--action); }
.result-badge { display: inline-flex; align-items: center; min-height: 1.75rem; padding: .25rem .65rem; border-radius: 999px; background: var(--tone-bg); color: var(--tone); font-size: .78rem; font-weight: 700; white-space: nowrap; }
.card__body { display: grid; gap: 1.5rem; padding: clamp(1.5rem, 6vw, 2.5rem) clamp(1.25rem, 5vw, 2rem); }
.result-summary { display: grid; grid-template-columns: 3.5rem minmax(0, 1fr); align-items: start; gap: 1.15rem; }
.status-icon { display: grid; place-items: center; width: 3.5rem; height: 3.5rem; border-radius: 14px; background: var(--tone-bg); color: var(--tone); }
.status-icon svg { width: 1.65rem; height: 1.65rem; fill: none; stroke: currentColor; stroke-width: 2.25; stroke-linecap: round; stroke-linejoin: round; }
.result-copy { min-width: 0; }
.eyebrow { margin: 0 0 .35rem; color: var(--tone); font-size: .76rem; font-weight: 750; letter-spacing: .08em; text-transform: uppercase; }
h1 { margin: 0; font-size: clamp(1.45rem, 5vw, 1.85rem); line-height: 1.25; letter-spacing: -.025em; }
.message { max-width: 52ch; margin: .65rem 0 0; color: var(--muted); font-size: .98rem; line-height: 1.7; }
.account-chip { display: flex; flex-wrap: wrap; align-items: center; gap: .55rem; padding: .8rem 1rem; border: 1px solid var(--border-subtle); border-radius: 10px; background: var(--sunken); }
.account-chip span { color: var(--faint); font-size: .8rem; font-weight: 650; }
.account-chip code { font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace; font-size: .86rem; overflow-wrap: anywhere; }
.close-button { min-height: 2.75rem; border-radius: 9px; font-weight: 700; cursor: pointer; transition: background-color .18s ease, color .18s ease; }
.close-button:focus-visible { outline: 3px solid var(--action); outline-offset: 3px; }
.actions { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: 1rem clamp(1.25rem, 5vw, 2rem); border-top: 1px solid var(--border-subtle); background: var(--sunken); }
.action-note { min-width: 0; }
.auto-close-note, .close-hint { margin: 0; color: var(--faint); font-size: .82rem; line-height: 1.55; }
.close-button { flex: 0 0 auto; min-width: 9.5rem; padding: .65rem 1.2rem; border: 0; color: var(--action-ink); background: var(--action); }
.close-button:hover { background: var(--action-hover); }
.close-button:disabled { cursor: default; background: var(--faint); color: var(--surface); }
[hidden] { display: none; }
@media (max-width: 32rem) {
  .shell { padding: .75rem; }
  .card { border-radius: 14px; }
  .card__header, .card__body, .actions { padding-left: 1.15rem; padding-right: 1.15rem; }
  .result-summary { grid-template-columns: 1fr; }
  .actions { align-items: stretch; flex-direction: column; }
  .close-button { width: 100%; }
}
@media (prefers-reduced-motion: reduce) { .close-button { transition: none; } }`

func writeOAuthResult(writer http.ResponseWriter, outcome oauthCallbackOutcome) {
	presentation := presentOAuthCallbackOutcome(outcome)
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Content-Language", "zh-CN")
	writer.WriteHeader(http.StatusOK)
	autoCloseAttr := "0"
	autoCloseNote := `<p class="auto-close-note">处理完成后可安全关闭此页面。</p>`
	if presentation.AutoClose {
		autoCloseAttr = "1"
		autoCloseNote = `<p class="auto-close-note">页面将在 2 秒后自动关闭。</p>`
	}
	accountChip := ""
	if outcome.Kind == oauthOutcomeSuccess && outcome.AccountMask != "" {
		accountChip = `<div class="account-chip"><span>已授权账号</span><code>` + html.EscapeString(outcome.AccountMask) + `</code></div>`
	}
	title := html.EscapeString(presentation.Title)
	status := html.EscapeString(presentation.Status)
	page := `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>` + title + `</title>
  <style>` + oauthResultStyle + `</style>
</head>
<body data-tone="` + presentation.Tone + `" data-autoclose="` + autoCloseAttr + `">
  <main class="shell">
    <section class="card" aria-labelledby="result-title" aria-describedby="result-message">
      <header class="card__header">
        <span class="brand">` + oauthBrandMarkSVG + `GPT-LOAD</span>
        <span class="result-badge">` + status + `</span>
      </header>
      <div class="card__body">
        <div class="result-summary">
          <div class="status-icon"><svg viewBox="0 0 24 24" aria-hidden="true">` + presentation.Icon + `</svg></div>
          <div class="result-copy">
            <p class="eyebrow">订阅账号授权</p>
            <h1 id="result-title">` + title + `</h1>
            <p id="result-message" class="message">` + html.EscapeString(presentation.Message) + `</p>
          </div>
        </div>
        ` + accountChip + `
      </div>
      <footer class="actions">
        <div class="action-note">
          ` + autoCloseNote + `
          <p id="close-hint" class="close-hint" role="status" aria-live="polite" hidden>浏览器未允许自动关闭，请手动关闭此页面。</p>
        </div>
        <button id="close-and-return" class="close-button" type="button">关闭并返回</button>
      </footer>
    </section>
  </main>
  <script>` + oauthResultScript + `</script>
</body>
</html>`
	_, _ = io.WriteString(writer, page)
}
