package embedded

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/modelcatalog"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
)

// CodexClientVersion 与 GPT-Load 的 Codex 模型目录版本一致，由测试校验。
const CodexClientVersion = "0.159.2"

// 沿用 CPA 的客户端标识格式，版本统一由 GPT-Load 的已验证版本集固定。
const codexUserAgent = "codex-tui/" + CodexClientVersion + " (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; " + CodexClientVersion + ")"

func init() {
	// CPA 在 WebSocket 握手的最后应用模型专属头；同步其中的旧版本标识。
	var overrides []*registry.ModelInfo
	if err := json.Unmarshal(modelcatalog.JSON(), &overrides); err != nil {
		panic(err)
	}
	for _, model := range overrides {
		if model == nil || model.Config == nil {
			continue
		}
		for name := range model.Config.OverrideHeader {
			switch {
			case strings.EqualFold(name, "User-Agent"):
				model.Config.OverrideHeader[name] = codexUserAgent
			case strings.EqualFold(name, "Version"):
				model.Config.OverrideHeader[name] = CodexClientVersion
			}
		}
	}
	if len(overrides) > 0 {
		registry.GetGlobalRegistry().RegisterClient("gptload-codex-identity", ProviderCodex, overrides)
	}
}

// codexHeadersRoundTripper 固定客户端版本标识及 HTTP 会话头。
type codexHeadersRoundTripper struct {
	base       http.RoundTripper
	source     http.Header
	configured []string
}

func (transport codexHeadersRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	for _, name := range transport.configured {
		name = http.CanonicalHeaderKey(name)
		switch name {
		case "Originator":
			value, present := codexHeaderValue(transport.source, name)
			if present {
				request.Header.Set(name, value)
			} else {
				request.Header.Del(name)
			}
		}
	}
	request.Header.Set("Version", CodexClientVersion)
	request.Header.Set("User-Agent", codexUserAgent)
	normalizeCodexSessionHeader(request.Header)
	// CPA 的直接图片路径不读取 opts.Headers，补回调用者显式提供的会话。
	if request.Header.Get("Session-Id") == "" {
		if session := transport.source.Get("Session-Id"); session != "" {
			request.Header.Set("Session-Id", session)
		}
	}
	return transport.base.RoundTrip(request)
}

func normalizedCodexHeaders(headers http.Header) http.Header {
	cloned := headers.Clone()
	if cloned == nil {
		cloned = make(http.Header)
	}
	// 仅 Codex 忽略客户端及分组规则提供的版本身份。
	for name := range cloned {
		if strings.EqualFold(name, "User-Agent") || strings.EqualFold(name, "Version") {
			delete(cloned, name)
		}
	}
	cloned.Set("Version", CodexClientVersion)
	cloned.Set("User-Agent", codexUserAgent)
	normalizeCodexSessionHeader(cloned)
	return cloned
}

// normalizeCodexSessionHeader 兼容下划线拼法；同时存在时以连字符字段为准。
func normalizeCodexSessionHeader(headers http.Header) {
	session, _ := codexHeaderValue(headers, "Session-Id")
	session = strings.TrimSpace(session)
	if session == "" {
		session, _ = codexHeaderValue(headers, "Session_id")
		session = strings.TrimSpace(session)
	}
	for name := range headers {
		if strings.EqualFold(name, "Session-Id") || strings.EqualFold(name, "Session_id") {
			delete(headers, name)
		}
	}
	if session != "" {
		headers.Set("Session-Id", session)
	}
}

func codexHeaderValue(headers http.Header, name string) (string, bool) {
	values, present := headers[http.CanonicalHeaderKey(name)]
	if !present {
		for key, candidate := range headers {
			if strings.EqualFold(key, name) {
				values, present = candidate, true
				break
			}
		}
	}
	if len(values) > 0 {
		return values[0], present
	}
	return "", present
}
