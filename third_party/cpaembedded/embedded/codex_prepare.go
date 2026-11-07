package embedded

import (
	"bytes"

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
	if input.IsArray() {
		return stripCodexReasoningStatusArray(raw, input)
	}
	if input.IsObject() {
		return deleteCodexReasoningStatus(raw, "input.status", input)
	}
	return raw
}

func stripCodexReasoningStatusArray(raw []byte, input gjson.Result) []byte {
	changed := false
	parts := make([][]byte, 0, int(input.Get("#").Int()))
	input.ForEach(func(_, item gjson.Result) bool {
		itemRaw := []byte(item.Raw)
		updated := deleteCodexReasoningStatus(itemRaw, "status", item)
		if !changed && !bytes.Equal(updated, itemRaw) {
			changed = true
		}
		parts = append(parts, updated)
		return true
	})
	if !changed {
		return raw
	}
	joined := bytes.Join(parts, []byte{','})
	rebuilt := make([]byte, 0, len(joined)+2)
	rebuilt = append(rebuilt, '[')
	rebuilt = append(rebuilt, joined...)
	rebuilt = append(rebuilt, ']')
	updated, err := sjson.SetRawBytes(raw, "input", rebuilt)
	if err != nil {
		return raw
	}
	return updated
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
