package embedded

import "strings"

// targetContinuityScope 防止切换目标后复用原上游的会话或推理回放。
func targetContinuityScope(scope, baseURL string) string {
	if strings.TrimSpace(scope) == "" {
		return ""
	}
	return scope + "\x00target\x00" + baseURL
}
