package embedded

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	codexchat "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/codex/openai/chat-completions"
	codexresponses "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/codex/openai/responses"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

// HTTP 与 WebSocket 共用此转换入口；响应继续使用 CPA 原始转换器。
func init() {
	sdktranslator.Register(sdktranslator.FormatOpenAIResponse, sdktranslator.FormatCodex,
		codexRequestWithServiceTier(codexresponses.ConvertOpenAIResponsesRequestToCodex),
		sdktranslator.ResponseTransform{
			Stream:    codexresponses.ConvertCodexResponseToOpenAIResponses,
			NonStream: codexresponses.ConvertCodexResponseToOpenAIResponsesNonStream,
		})
	sdktranslator.Register(sdktranslator.FormatOpenAI, sdktranslator.FormatCodex,
		codexRequestWithServiceTier(codexchat.ConvertOpenAIRequestToCodex),
		sdktranslator.ResponseTransform{
			Stream:    codexchat.ConvertCodexResponseToOpenAI,
			NonStream: codexchat.ConvertCodexResponseToOpenAINonStream,
		})
}

func codexRequestWithServiceTier(convert sdktranslator.RequestTransform) sdktranslator.RequestTransform {
	return func(model string, raw []byte, stream bool) []byte {
		tier := strings.ToLower(strings.TrimSpace(gjson.GetBytes(raw, "service_tier").String()))
		body := convert(model, raw, stream)
		switch tier {
		case "fast", "priority":
			// Codex 订阅端使用 priority，不能套用公开 API 的 fast 别名。
			tier = "priority"
		case "ultrafast":
			// 在 CPA 原始转换完成后恢复，避免被其字段过滤规则删除。
		default:
			return body
		}
		return helps.SetStringIfDifferent(body, "service_tier", tier)
	}
}
