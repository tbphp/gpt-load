package embedded

import (
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	codexresponses "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/codex/openai/responses"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

// HTTP 与 WebSocket 共用此转换入口；响应继续使用 CPA 原始转换器。
func init() {
	sdktranslator.Register(
		sdktranslator.FormatOpenAIResponse,
		sdktranslator.FormatCodex,
		codexResponsesRequestWithFastTier,
		sdktranslator.ResponseTransform{
			Stream:    codexresponses.ConvertCodexResponseToOpenAIResponses,
			NonStream: codexresponses.ConvertCodexResponseToOpenAIResponsesNonStream,
		},
	)
}

func codexResponsesRequestWithFastTier(model string, raw []byte, stream bool) []byte {
	tier := gjson.GetBytes(raw, "service_tier").String()
	body := codexresponses.ConvertOpenAIResponsesRequestToCodex(model, raw, stream)
	if tier == "priority" || tier == "fast" {
		// CPA 仅保留 priority；在原始转换完成后统一发送官方推荐的 fast。
		body = helps.SetStringIfDifferent(body, "service_tier", "fast")
	}
	return body
}
