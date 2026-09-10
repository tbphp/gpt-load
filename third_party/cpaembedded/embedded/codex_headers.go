package embedded

import (
	"net/http"
	"strings"
)

// codexHeadersRoundTripper 在 CPA 默认值与模型覆盖之后应用显式身份规则。
type codexHeadersRoundTripper struct {
	base       http.RoundTripper
	source     http.Header
	configured []string
}

func (transport codexHeadersRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	versionConfigured := false
	for _, name := range transport.configured {
		name = http.CanonicalHeaderKey(name)
		switch name {
		case "User-Agent", "Originator", "Version":
			value := codexHeaderValue(transport.source, name)
			if value != "" || name == "User-Agent" {
				// 空 UA 显式禁止 net/http 重新补入 Go 默认 UA。
				request.Header.Set(name, value)
			} else {
				request.Header.Del(name)
			}
			if name == "Version" {
				versionConfigured = true
			}
		}
	}
	if !versionConfigured {
		if version := codexUserAgentVersion(request.Header.Get("User-Agent")); version != "" {
			request.Header.Set("Version", version)
		} else {
			request.Header.Del("Version")
		}
	}
	normalizeCodexSessionHeader(request.Header)
	// CPA 的直接图片路径不读取 opts.Headers，补回调用者显式提供的会话。
	if request.Header.Get("Session-Id") == "" {
		if session := transport.source.Get("Session-Id"); session != "" {
			request.Header.Set("Session-Id", session)
		}
	}
	return transport.base.RoundTrip(request)
}

func codexUserAgentVersion(userAgent string) string {
	fields := strings.Fields(userAgent)
	if len(fields) == 0 {
		return ""
	}
	product, version, _ := strings.Cut(fields[0], "/")
	if product == "codex-tui" || product == "codex_cli_rs" {
		return version
	}
	return ""
}

func normalizedCodexHeaders(headers http.Header) http.Header {
	cloned := headers.Clone()
	normalizeCodexSessionHeader(cloned)
	return cloned
}

// normalizeCodexSessionHeader 兼容下划线拼法；同时存在时以连字符字段为准。
func normalizeCodexSessionHeader(headers http.Header) {
	session := strings.TrimSpace(codexHeaderValue(headers, "Session-Id"))
	if session == "" {
		session = strings.TrimSpace(codexHeaderValue(headers, "Session_id"))
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

func codexHeaderValue(headers http.Header, name string) string {
	if value := headers.Get(name); value != "" {
		return value
	}
	for key, values := range headers {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}
