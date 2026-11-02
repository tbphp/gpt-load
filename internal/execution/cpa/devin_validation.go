package cpa

import (
	"errors"
	"strings"

	"github.com/tidwall/gjson"

	"gpt-load/internal/dialect"
	"gpt-load/internal/protocol"
)

// 只校验 Connect-RPC 明确无法表达的语义，普通请求结构仍由方言层校验。
func (*devinProviderBridge) ValidateRequest(request providerRequest) error {
	root := gjson.ParseBytes(request.Payload)
	if !root.IsObject() {
		return errors.New("invalid Devin request")
	}
	// 这些续接字段会被 CPA 优先用于私有会话，不能覆盖 GPT-Load 的账号隔离标识。
	for _, field := range []string{"previous_response_id", "previous_interaction_id"} {
		if strings.TrimSpace(root.Get(field).String()) != "" {
			return errors.New("Devin does not support upstream response continuation")
		}
	}
	for _, path := range []string{
		"stop", "stop_sequences", "generationConfig.stopSequences", "generationConfig.stop_sequences",
		"generation_config.stopSequences", "generation_config.stop_sequences",
	} {
		value := root.Get(path)
		if value.Exists() && value.Type != gjson.Null &&
			!(value.Type == gjson.String && value.Str == "") && !(value.IsArray() && len(value.Array()) == 0) {
			return errors.New("Devin does not support stop sequences")
		}
	}
	proto := protocol.OpenAICompletions
	switch request.Format {
	case "claude":
		proto = protocol.Anthropic
	case "gemini":
		proto = protocol.Gemini
	case "openai-response":
		proto = protocol.OpenAIResponses
	}
	if dialect.CountMidConversationSystemMessages(proto, request.Payload) > 0 {
		return errors.New("Devin cannot preserve mid-conversation system instructions")
	}
	if choice := root.Get("tool_choice"); choice.Exists() {
		mode := choice.String()
		if choice.IsObject() {
			mode = choice.Get("type").String()
		}
		if mode != "auto" {
			return errors.New("Devin does not support forced or disabled tool choice")
		}
	}
	if parallel := root.Get("parallel_tool_calls"); parallel.Exists() && !parallel.Bool() {
		return errors.New("Devin cannot enforce serial tool calls")
	}
	for _, path := range []string{"response_format.type", "text.format.type", "output_config.format.type"} {
		if format := root.Get(path).String(); format != "" && format != "text" {
			return errors.New("Devin does not support constrained output formats")
		}
	}
	for _, path := range []string{
		"generationConfig.responseMimeType", "generationConfig.response_mime_type",
		"generation_config.responseMimeType", "generation_config.response_mime_type",
	} {
		if mime := root.Get(path).String(); mime != "" && mime != "text/plain" {
			return errors.New("Devin does not support constrained output formats")
		}
	}
	toolConfig, err := devinGeminiToolConfig(request.Payload)
	if err != nil {
		return err
	}
	if mode := toolConfig.Get("mode").String(); mode != "" && mode != "AUTO" {
		return errors.New("Devin does not support forced or disabled tool choice")
	}
	if root.Get("tool_choice.disable_parallel_tool_use").Bool() {
		return errors.New("Devin cannot enforce serial tool calls")
	}
	for _, tool := range root.Get("tools").Array() {
		if kind := tool.Get("type").String(); request.Format == "openai" && kind != "" && kind != "function" {
			return errors.New("Devin Chat Completions only supports function tools")
		}
		if request.Format == "gemini" {
			unsupported := false
			tool.ForEach(func(key, _ gjson.Result) bool {
				if key.String() != "functionDeclarations" {
					unsupported = true
				}
				return true
			})
			if unsupported {
				return errors.New("Devin only supports client function tools")
			}
		} else if devinUnsupportedTool(tool) {
			return errors.New("Devin does not support server-side tools")
		}
	}
	var invalid bool
	checkParts := func(parts gjson.Result) {
		if devinUnsupportedParts(parts) {
			invalid = true
		}
	}
	switch request.Format {
	case "gemini":
		for _, message := range root.Get("contents").Array() {
			checkParts(message.Get("parts"))
		}
	case "openai-response":
		for _, message := range root.Get("input").Array() {
			checkParts(message.Get("content"))
			checkParts(message.Get("output"))
		}
	default:
		for _, message := range root.Get("messages").Array() {
			checkParts(message.Get("content"))
		}
	}
	if invalid {
		return errors.New("Devin supports text and inline images only; remote images, audio, video and documents are unsupported")
	}
	return nil
}

// 校验和白名单转换共用同一份 Gemini 字段选择，避免一种拼写检查另一种拼写执行。
func devinGeminiToolConfig(body []byte) (gjson.Result, error) {
	var config gjson.Result
	for _, path := range []string{
		"toolConfig.functionCallingConfig", "toolConfig.function_calling_config",
		"tool_config.functionCallingConfig", "tool_config.function_calling_config",
	} {
		candidate := gjson.GetBytes(body, path)
		if !candidate.Exists() || candidate.Type == gjson.Null {
			continue
		}
		if !candidate.IsObject() || config.Exists() {
			return gjson.Result{}, errors.New("invalid or ambiguous Devin Gemini tool configuration")
		}
		config = candidate
	}
	if config.Get("allowedFunctionNames").Exists() && config.Get("allowed_function_names").Exists() {
		return gjson.Result{}, errors.New("ambiguous Devin Gemini function allowlist")
	}
	return config, nil
}

func devinUnsupportedParts(parts gjson.Result) bool {
	for _, part := range parts.Array() {
		switch part.Get("type").String() {
		case "input_audio", "audio", "video", "input_video", "file", "input_file", "document":
			return true
		case "image_url", "input_image":
			uri := part.Get("image_url").String()
			if part.Get("image_url").IsObject() {
				uri = part.Get("image_url.url").String()
			}
			if !strings.HasPrefix(strings.ToLower(uri), "data:image/") {
				return true
			}
		case "image":
			if part.Get("source.type").String() != "base64" {
				return true
			}
		case "tool_result":
			if devinUnsupportedParts(part.Get("content")) {
				return true
			}
		}
		if part.Get("fileData").Exists() || part.Get("file_data").Exists() {
			return true
		}
		for _, key := range []string{"inlineData", "inline_data"} {
			data := part.Get(key)
			if data.Exists() {
				mime := data.Get("mimeType").String()
				if mime == "" {
					mime = data.Get("mime_type").String()
				}
				if !strings.HasPrefix(strings.ToLower(mime), "image/") {
					return true
				}
			}
		}
	}
	return false
}

func devinUnsupportedTool(tool gjson.Result) bool {
	switch tool.Get("type").String() {
	case "", "function", "custom":
		return false
	case "namespace":
		for _, child := range tool.Get("tools").Array() {
			if devinUnsupportedTool(child) {
				return true
			}
		}
		return false
	default:
		return true
	}
}
