package bifrost

import (
	"strconv"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"gpt-load/internal/protocol"
)

// 仅修正 DeepSeek 原生协议的字段差异，不重建或重排消息历史。
func normalizeDeepSeekNativeRequest(body []byte, clientProtocol protocol.Protocol) ([]byte, error) {
	var err error
	body, err = normalizeDeepSeekDefaultThinking(body, clientProtocol)
	if err != nil {
		return nil, err
	}
	field := "messages"
	switch clientProtocol {
	case protocol.OpenAICompletions:
	case protocol.OpenAIResponses:
		field = "input"
	default:
		return body, nil
	}
	history := gjson.GetBytes(body, field)
	if !history.IsArray() {
		return body, nil
	}
	for index, message := range history.Array() {
		if !message.IsObject() {
			continue
		}
		if clientProtocol == protocol.OpenAIResponses {
			itemType := message.Get("type")
			if itemType.Exists() && itemType.String() != "message" {
				continue
			}
		}
		path := field + "." + strconv.Itoa(index)
		var err error
		if message.Get("role").String() == "developer" && deepSeekTextOnlyContent(message.Get("content"), clientProtocol) {
			// Completions 不接受 developer；Responses 将它视为 user，需保留指令语义。
			body, err = sjson.SetBytes(body, path+".role", "system")
			if err != nil {
				return nil, err
			}
		}
		if clientProtocol == protocol.OpenAICompletions && message.Get("role").String() == "assistant" && !message.Get("reasoning_content").Exists() {
			// 只复制已有的完整文本，不用摘要、密文或空占位补造思考内容。
			reasoning := message.Get("reasoning")
			if reasoning.Type == gjson.String && reasoning.Str != "" {
				body, err = sjson.SetRawBytes(body, path+".reasoning_content", []byte(reasoning.Raw))
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return body, nil
}

// 强制工具调用优先于上游默认思考；显式思考配置保持原样，由上游校验冲突。
func normalizeDeepSeekDefaultThinking(body []byte, clientProtocol protocol.Protocol) ([]byte, error) {
	choice := gjson.GetBytes(body, "tool_choice")
	forced := false
	switch clientProtocol {
	case protocol.OpenAICompletions:
		forced = choice.String() == "required" ||
			(choice.Get("type").String() == "function" && choice.Get("function.name").String() != "")
	case protocol.OpenAIResponses:
		forced = choice.String() == "required" ||
			(choice.Get("type").String() == "function" && choice.Get("name").String() != "")
	case protocol.Anthropic:
		forced = choice.Get("type").String() == "any" ||
			(choice.Get("type").String() == "tool" && choice.Get("name").String() != "")
	default:
		return body, nil
	}
	if !forced {
		return body, nil
	}
	if gjson.GetBytes(body, "thinking").Exists() || gjson.GetBytes(body, "reasoning_effort").Exists() {
		return body, nil
	}
	if clientProtocol == protocol.OpenAIResponses {
		reasoning := gjson.GetBytes(body, "reasoning")
		if reasoning.Exists() && (!reasoning.IsObject() || reasoning.Get("effort").Exists()) {
			return body, nil
		}
		return sjson.SetBytes(body, "reasoning.effort", "none")
	}
	if clientProtocol == protocol.Anthropic {
		config := gjson.GetBytes(body, "output_config")
		if config.Exists() && (!config.IsObject() || config.Get("effort").Exists()) {
			return body, nil
		}
	}
	return sjson.SetBytes(body, "thinking.type", "disabled")
}

func deepSeekTextOnlyContent(content gjson.Result, clientProtocol protocol.Protocol) bool {
	if content.Type == gjson.String {
		return true
	}
	if !content.IsArray() || len(content.Array()) == 0 {
		return false
	}
	for _, block := range content.Array() {
		kind := block.Get("type").String()
		if clientProtocol == protocol.OpenAICompletions && kind != "text" {
			return false
		}
		if clientProtocol == protocol.OpenAIResponses && kind != "input_text" && kind != "output_text" {
			return false
		}
		if block.Get("text").Type != gjson.String {
			return false
		}
	}
	return true
}
