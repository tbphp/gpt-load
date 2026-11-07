package embedded

import (
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// prepareCodexExecutionRequest 固定 Codex 客户端头。Responses 出站只删
// input 里 reasoning 条目的输出侧 status，其余字段保持原字节。
func prepareCodexExecutionRequest(request ExecuteRequest) ExecuteRequest {
	request.Headers = normalizedCodexHeaders(request.Headers)
	if sdktranslator.FromString(request.Format) != sdktranslator.FormatOpenAIResponse {
		return request
	}
	request.Payload = stripCodexReasoningStatus(request.Payload)
	request.OriginalRequest = stripCodexReasoningStatus(request.OriginalRequest)
	return request
}

func stripCodexReasoningStatus(raw []byte) []byte {
	input := gjson.GetBytes(raw, "input")
	if !input.Exists() {
		return raw
	}
	result := raw
	if input.IsArray() {
		input.ForEach(func(index, item gjson.Result) bool {
			result = deleteCodexReasoningStatus(result, "input."+index.String()+".status", item)
			return true
		})
		return result
	}
	if input.IsObject() {
		return deleteCodexReasoningStatus(result, "input.status", input)
	}
	return result
}

func deleteCodexReasoningStatus(raw []byte, path string, item gjson.Result) []byte {
	if item.Get("type").String() != "reasoning" || !item.Get("status").Exists() {
		return raw
	}
	updated, err := sjson.DeleteBytes(raw, path)
	if err != nil {
		return raw
	}
	return updated
}
