// Package geminiimage 实现 OpenAI Images 客户端到 Gemini 生图上游的单向格式适配。
package geminiimage

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/usage"
)

// ErrInvalidResponse 表示上游响应无法转换为有效的单张图片，错误不包含上游正文。
var ErrInvalidResponse = errors.New("Gemini image response could not be converted")

// unsupportedRequestError 沿用执行器的转换失败分类，允许尝试其他上游候选。
type unsupportedRequestError string

func (err unsupportedRequestError) Error() string { return string(err) }

func (unsupportedRequestError) ConversionCode() string {
	return execution.ErrorCodeTargetConversionNotSupported
}

// ValidateRequest 校验单图、非流式合同，避免静默丢弃 Images 参数。
func ValidateRequest(payload []byte) error {
	_, err := generationPrompt(payload)
	return err
}

func generationPrompt(payload []byte) (string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return "", errors.New("Gemini image conversion requires a JSON object")
	}
	var prompt string
	if err := json.Unmarshal(object["prompt"], &prompt); err != nil || strings.TrimSpace(prompt) == "" {
		return "", errors.New("Gemini image conversion requires a non-empty prompt")
	}
	var conversionErr error
	for name, raw := range object {
		switch name {
		case "model", "prompt":
		case "n":
			var count int
			if json.Unmarshal(raw, &count) != nil || count < 1 {
				return "", errors.New("Images n must be a positive integer")
			}
			if count != 1 {
				conversionErr = unsupportedRequestError("Gemini image conversion only supports n=1")
			}
		case "stream":
			var stream *bool
			if json.Unmarshal(raw, &stream) != nil || stream == nil {
				return "", errors.New("Images stream must be a boolean")
			}
			if *stream {
				conversionErr = unsupportedRequestError("Gemini image conversion does not support streaming")
			}
		case "size", "quality", "response_format":
			want := "auto"
			if name == "response_format" {
				want = "b64_json"
			}
			var value *string
			if json.Unmarshal(raw, &value) != nil || value == nil {
				return "", fmt.Errorf("Images %s must be a string", name)
			}
			if *value != want {
				conversionErr = unsupportedRequestError(fmt.Sprintf("Gemini image conversion only supports %s=%s", name, want))
			}
		default:
			conversionErr = unsupportedRequestError("Gemini image conversion received an unsupported field")
		}
	}
	return prompt, conversionErr
}

// ConvertRequest 将已清理控制字段的 Images 请求转换为 Gemini generateContent 正文。
func ConvertRequest(payload []byte) ([]byte, error) {
	prompt, err := generationPrompt(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"contents": []any{map[string]any{
			"role": "user", "parts": []any{map[string]string{"text": prompt}},
		}},
		"generationConfig": map[string]any{"responseModalities": []string{"TEXT", "IMAGE"}},
	})
}

type imageData struct {
	MIMEType      string `json:"mimeType"`
	SnakeMIMEType string `json:"mime_type"`
	Data          string `json:"data"`
}

// ConvertResponse 返回 Images 正文及保留原 Gemini 计价语义的用量证据。
func ConvertResponse(payload []byte) ([]byte, *execution.UsageEvidence, error) {
	var root struct {
		ModelVersion  string          `json:"modelVersion"`
		UsageMetadata json.RawMessage `json:"usageMetadata"`
		Candidates    []struct {
			Content struct {
				Parts []struct {
					Thought         bool       `json:"thought"`
					InlineData      *imageData `json:"inlineData"`
					SnakeInlineData *imageData `json:"inline_data"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, nil, ErrInvalidResponse
	}
	image := ""
	for _, candidate := range root.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.Thought {
				continue
			}
			data := part.InlineData
			if data == nil {
				data = part.SnakeInlineData
			}
			if data == nil {
				continue
			}
			mimeType := data.MIMEType
			if mimeType == "" {
				mimeType = data.SnakeMIMEType
			}
			switch mimeType {
			case "image/png", "image/jpeg", "image/webp":
			default:
				return nil, nil, ErrInvalidResponse
			}
			if image != "" || data.Data == "" {
				return nil, nil, ErrInvalidResponse
			}
			// 流式校验 Base64，避免额外分配整张解码图片。
			decoded, err := io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(data.Data)))
			if err != nil || decoded == 0 {
				return nil, nil, ErrInvalidResponse
			}
			image = data.Data
		}
	}
	if image == "" {
		return nil, nil, ErrInvalidResponse
	}
	normalized, err := dialect.NewGemini().ExtractUsage(payload)
	if err != nil {
		// 用量算术失败不丢弃已生成的图片，但不能据此产生正常报价。
		normalized = usage.Result{State: usage.StateMissing}
		normalized.Diagnostics.Add(usage.DiagnosticInvalidNumber)
	}
	evidence := &execution.UsageEvidence{Normalized: normalized, Raw: bytes.Clone(root.UsageMetadata)}
	response := map[string]any{
		"created": time.Now().Unix(), "data": []map[string]string{{"b64_json": image}},
	}
	if model := strings.TrimSpace(root.ModelVersion); model != "" {
		response["model"] = model
	}
	// 对外只映射可信计数；内部保留原始 Gemini 的完整状态和缓存计价语义。
	if normalized.State != usage.StateMissing && normalized.Diagnostics == (usage.Diagnostics{}) {
		var metadata map[string]json.RawMessage
		if err := json.Unmarshal(root.UsageMetadata, &metadata); err != nil {
			return nil, nil, ErrInvalidResponse
		}
		wireUsage := map[string]any{}
		if raw, ok := metadata["promptTokenCount"]; ok {
			wireUsage["input_tokens"] = raw
		}
		if _, candidates := metadata["candidatesTokenCount"]; candidates || metadata["thoughtsTokenCount"] != nil {
			wireUsage["output_tokens"] = normalized.Tokens.Output
		}
		if raw, ok := metadata["totalTokenCount"]; ok {
			wireUsage["total_tokens"] = raw
		}
		if _, ok := metadata["cachedContentTokenCount"]; ok {
			wireUsage["input_tokens_details"] = map[string]int64{"cached_tokens": normalized.Tokens.CacheRead}
		}
		response["usage"] = wireUsage
	}
	body, err := json.Marshal(response)
	if err != nil {
		return nil, nil, ErrInvalidResponse
	}
	return body, evidence, nil
}
