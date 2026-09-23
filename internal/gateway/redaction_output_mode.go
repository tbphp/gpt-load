package gateway

import (
	"mime"
	"strings"

	"github.com/tidwall/gjson"

	"gpt-load/internal/protocol"
)

// requestDeclaresJSONOutput 仅识别显式格式声明；Responses 返回对象沿用 text.format。
func requestDeclaresJSONOutput(clientProtocol protocol.Protocol, body []byte) bool {
	declared := false
	switch clientProtocol {
	case protocol.OpenAICompletions:
		declared = redactionFormatType(gjson.GetBytes(body, "response_format.type"), true)
	case protocol.OpenAIResponses:
		declared = redactionFormatType(gjson.GetBytes(body, "text.format.type"), true)
	case protocol.Anthropic:
		declared = redactionFormatType(gjson.GetBytes(body, "output_config.format.type"), false)
	case protocol.Gemini:
		if mimeType := gjson.GetBytes(body, "generationConfig.responseMimeType"); mimeType.Type == gjson.String {
			mediaType, _, err := mime.ParseMediaType(mimeType.Str)
			if err == nil && strings.EqualFold(mediaType, "application/json") {
				declared = true
			}
		}
		if !declared {
			declared = gjson.GetBytes(body, "generationConfig.responseSchema").IsObject()
		}
	default:
		return false
	}
	// Most requests have no JSON output declaration, so only validate the full
	// wire body when a candidate is present. This avoids decoding large messages.
	return declared && gjson.ValidBytes(body)
}

func redactionFormatType(value gjson.Result, allowJSONObject bool) bool {
	return value.Type == gjson.String &&
		(value.Str == "json_schema" || (allowJSONObject && value.Str == "json_object"))
}
