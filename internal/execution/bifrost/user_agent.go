package bifrost

import (
	"net/http"
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/platform/version"
)

// withUserAgent 仅对普通渠道统一出站身份；订阅渠道由各自适配器处理。
func withUserAgent(spec execution.AttemptSpec) execution.AttemptSpec {
	for _, name := range spec.ConfiguredHeaders {
		if strings.EqualFold(name, "User-Agent") && strings.TrimSpace(spec.Header.Get("User-Agent")) != "" {
			return spec
		}
	}
	spec.Header = spec.Header.Clone()
	if spec.Header == nil {
		spec.Header = make(http.Header)
	}
	// 未配置、删除或留空都使用系统身份，不继承客户端或 HTTP 库的默认值。
	spec.Header.Set("User-Agent", "GPT-Load/"+version.Version)
	return spec
}
