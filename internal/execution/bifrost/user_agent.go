package bifrost

import (
	"net/http"
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/platform/version"
)

// withUserAgent 仅为普通渠道补齐出站身份；订阅渠道由各自适配器处理。
func withUserAgent(spec execution.AttemptSpec) execution.AttemptSpec {
	// 系统或分组规则已先应用，保留其配置值或客户端原始身份。
	if strings.TrimSpace(spec.Header.Get("User-Agent")) != "" {
		return spec
	}
	spec.Header = spec.Header.Clone()
	if spec.Header == nil {
		spec.Header = make(http.Header)
	}
	// 没有有效 UA 时使用项目身份，包括规则明确删除或留空的情况。
	spec.Header.Set("User-Agent", "GPT-Load/"+version.Version)
	return spec
}
