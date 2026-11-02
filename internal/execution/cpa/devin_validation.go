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
	if mime := root.Get("generationConfig.responseMimeType").String(); mime != "" && mime != "text/plain" {
		return errors.New("Devin does not support constrained output formats")
	}
	if mode := root.Get("toolConfig.functionCallingConfig.mode").String(); mode != "" && mode != "AUTO" {
		return errors.New("Devin does not support forced or disabled tool choice")
	}
	if root.Get("tool_choice.disable_parallel_tool_use").Bool() {
		return errors.New("Devin cannot enforce serial tool calls")
	}
	for _, tool := range root.Get("tools").Array() {
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
