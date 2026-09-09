package cpa

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

var errAntigravityImagesResponse = errors.New("Antigravity image response could not be converted")

// antigravityImagesPrompt 校验首期单图、非流式合同，避免静默丢弃 Images 参数。
func antigravityImagesPrompt(request providerRequest) (string, error) {
	if request.RequestPath != "/v1/images/generations" {
		return "", errors.New("Antigravity only supports image generations")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(request.Payload, &object); err != nil || object == nil {
		return "", errors.New("Antigravity Images request must be a JSON object")
	}
	var prompt string
	if err := json.Unmarshal(object["prompt"], &prompt); err != nil || strings.TrimSpace(prompt) == "" {
		return "", errors.New("Antigravity Images requires a non-empty prompt")
	}
	for name, raw := range object {
		switch name {
		case "model", "prompt":
		case "n":
			var count int
			if json.Unmarshal(raw, &count) != nil || count != 1 {
				return "", errors.New("Antigravity Images only supports n=1")
			}
		case "stream":
			if !bytes.Equal(bytes.TrimSpace(raw), []byte("false")) {
				return "", errors.New("Antigravity Images does not support streaming")
			}
		case "size", "quality", "response_format":
			want := "auto"
			if name == "response_format" {
				want = "b64_json"
			}
			var value string
			if json.Unmarshal(raw, &value) != nil || value != want {
				return "", fmt.Errorf("Antigravity Images only supports %s=%s", name, want)
			}
		default:
			return "", errors.New("Antigravity Images request contains an unsupported field")
		}
	}
	return prompt, nil
}

func antigravityImagesRequestPayload(request providerRequest) ([]byte, error) {
	prompt, err := antigravityImagesPrompt(request)
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

type antigravityImageData struct {
	MIMEType      string `json:"mimeType"`
	SnakeMIMEType string `json:"mime_type"`
	Data          string `json:"data"`
}

func convertAntigravityImagesResponse(payload []byte) ([]byte, *execution.UsageEvidence, error) {
	var root struct {
		ModelVersion  string          `json:"modelVersion"`
		UsageMetadata json.RawMessage `json:"usageMetadata"`
		Candidates    []struct {
			Content struct {
				Parts []struct {
					Thought         bool                  `json:"thought"`
					InlineData      *antigravityImageData `json:"inlineData"`
					SnakeInlineData *antigravityImageData `json:"inline_data"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, nil, errAntigravityImagesResponse
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
				return nil, nil, errAntigravityImagesResponse
			}
			if image != "" || data.Data == "" {
				return nil, nil, errAntigravityImagesResponse
			}
			// 流式校验 Base64，避免额外分配整张解码图片。
			decoded, err := io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(data.Data)))
			if err != nil || decoded == 0 {
				return nil, nil, errAntigravityImagesResponse
			}
			image = data.Data
		}
	}
	if image == "" {
		return nil, nil, errAntigravityImagesResponse
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
			return nil, nil, errAntigravityImagesResponse
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
		return nil, nil, errAntigravityImagesResponse
	}
	return body, evidence, nil
}
