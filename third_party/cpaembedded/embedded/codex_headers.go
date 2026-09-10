package embedded

import (
	"net/http"
	"strings"
)

// codexClientVersion 必须与固定 CPA 依赖的默认及模型 UA 版本一致，由出站请求测试校验。
const codexClientVersion = "0.153.3"

// codexHeadersRoundTripper 保留 CPA 的 UA，并固定版本及 HTTP 会话头。
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
	request.Header.Set("Version", codexClientVersion)
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
	// 仅 Codex 忽略客户端及分组规则提供的版本身份，UA 由 CPA 生成。
	for name := range cloned {
		if strings.EqualFold(name, "User-Agent") || strings.EqualFold(name, "Version") {
			delete(cloned, name)
		}
	}
	cloned.Set("Version", codexClientVersion)
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
