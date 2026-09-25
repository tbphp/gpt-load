// Package geminiembedding 实现 OpenAI Embeddings 客户端到 Gemini 原生 embedding 上游的单向格式适配。
// 转换目标 :batchEmbedContents 是 Gemini 渠道原生支持的 gemini-embeddings 操作。
package geminiembedding

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"strconv"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/usage"
)

// ErrInvalidResponse 表示上游响应无法转换为与输入一一对应的向量，错误不包含上游正文。
var ErrInvalidResponse = errors.New("Gemini embeddings response could not be converted")

// unsupportedRequestError 沿用执行器的转换失败分类，允许尝试其他上游候选。
type unsupportedRequestError string

func (err unsupportedRequestError) Error() string { return string(err) }

func (unsupportedRequestError) ConversionCode() string {
	return execution.ErrorCodeTargetConversionNotSupported
}

// Conversion 记录把 Gemini 响应转回 OpenAI 格式所需的请求信息。
type Conversion struct {
	InputCount    int
	Base64        bool
	UpstreamModel string
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiEmbedRequest struct {
	Model                string        `json:"model"`
	Content              geminiContent `json:"content"`
	OutputDimensionality *int64        `json:"outputDimensionality,omitempty"`
}

// ConvertRequest 将 OpenAI Embeddings 请求转换为 Gemini :batchEmbedContents 正文。
// 每条文本对应一条子请求；单次条数上限交给上游校验。
func ConvertRequest(payload []byte, upstreamModel string) ([]byte, Conversion, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return nil, Conversion{}, errors.New("Embeddings request must be a JSON object")
	}
	if upstreamModel == "" {
		return nil, Conversion{}, errors.New("Gemini embeddings conversion requires an upstream model")
	}
	texts, err := inputTexts(object["input"])
	if err != nil {
		return nil, Conversion{}, err
	}
	var dimensions *int64
	if raw := bytes.TrimSpace(object["dimensions"]); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		var value int64
		if err := json.Unmarshal(raw, &value); err != nil || value <= 0 {
			return nil, Conversion{}, errors.New("Embeddings dimensions must be a positive integer")
		}
		dimensions = &value
	}
	conversion := Conversion{InputCount: len(texts), UpstreamModel: upstreamModel}
	if raw := bytes.TrimSpace(object["encoding_format"]); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		var format string
		if err := json.Unmarshal(raw, &format); err != nil || (format != "float" && format != "base64") {
			return nil, Conversion{}, errors.New("Embeddings encoding_format must be float or base64")
		}
		conversion.Base64 = format == "base64"
	}
	requests := make([]geminiEmbedRequest, len(texts))
	for index, text := range texts {
		requests[index] = geminiEmbedRequest{
			Model:                "models/" + upstreamModel,
			Content:              geminiContent{Parts: []geminiPart{{Text: text}}},
			OutputDimensionality: dimensions,
		}
	}
	body, err := json.Marshal(struct {
		Requests []geminiEmbedRequest `json:"requests"`
	}{Requests: requests})
	if err != nil {
		return nil, Conversion{}, err
	}
	return body, conversion, nil
}

func inputTexts(raw json.RawMessage) ([]string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, errors.New("Embeddings input is required")
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, errors.New("decode Embeddings text input")
		}
		return []string{text}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, errors.New("decode Embeddings input")
	}
	if len(items) == 0 {
		return nil, errors.New("Embeddings input must not be empty")
	}
	texts := make([]string, len(items))
	for index, item := range items {
		if item = bytes.TrimSpace(item); len(item) == 0 || item[0] != '"' {
			return nil, unsupportedRequestError("Gemini embeddings conversion only supports text input")
		}
		if err := json.Unmarshal(item, &texts[index]); err != nil {
			return nil, errors.New("decode Embeddings text input")
		}
	}
	return texts, nil
}

type openAIEmbedding struct {
	Object    string `json:"object"`
	Index     int    `json:"index"`
	Embedding any    `json:"embedding"`
}

type openAIUsage struct {
	PromptTokens int64 `json:"prompt_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

// ConvertResponse 返回 OpenAI Embeddings 正文及原 Gemini 用量证据。
// usage 字段始终输出；上游未返回可信用量时对外为 0，内部证据仍记为缺失、不计价。
func ConvertResponse(payload []byte, conversion Conversion) ([]byte, *execution.UsageEvidence, error) {
	var root struct {
		Embeddings []struct {
			Values json.RawMessage `json:"values"`
		} `json:"embeddings"`
		UsageMetadata json.RawMessage `json:"usageMetadata"`
	}
	if err := json.Unmarshal(payload, &root); err != nil || len(root.Embeddings) != conversion.InputCount {
		return nil, nil, ErrInvalidResponse
	}
	data := make([]openAIEmbedding, len(root.Embeddings))
	for index, embedding := range root.Embeddings {
		var value any = embedding.Values
		if conversion.Base64 {
			encoded, err := encodeBase64Vector(embedding.Values)
			if err != nil {
				return nil, nil, ErrInvalidResponse
			}
			value = encoded
		} else if count, err := scanNumbers(embedding.Values, nil); err != nil || count == 0 {
			return nil, nil, ErrInvalidResponse
		}
		data[index] = openAIEmbedding{Object: "embedding", Index: index, Embedding: value}
	}
	normalized, err := dialect.NewGeminiEmbeddings().ExtractUsage(payload)
	if err != nil {
		normalized = usage.Result{State: usage.StateMissing}
		normalized.Diagnostics.Add(usage.DiagnosticInvalidNumber)
	}
	wireUsage := openAIUsage{}
	if normalized.State == usage.StateComplete && normalized.Diagnostics == (usage.Diagnostics{}) {
		wireUsage.PromptTokens = normalized.Tokens.UncachedInput + normalized.Tokens.CacheRead
		wireUsage.TotalTokens = wireUsage.PromptTokens
	}
	body, err := json.Marshal(struct {
		Object string            `json:"object"`
		Data   []openAIEmbedding `json:"data"`
		Model  string            `json:"model"`
		Usage  openAIUsage       `json:"usage"`
	}{Object: "list", Data: data, Model: conversion.UpstreamModel, Usage: wireUsage})
	if err != nil {
		return nil, nil, ErrInvalidResponse
	}
	return body, &execution.UsageEvidence{Normalized: normalized, Raw: bytes.Clone(root.UsageMetadata)}, nil
}

// encodeBase64Vector 按 OpenAI 约定把向量编码为 float32 小端序的 Base64。
func encodeBase64Vector(raw json.RawMessage) (string, error) {
	var buffer []byte
	count, err := scanNumbers(raw, func(number []byte) error {
		value, err := strconv.ParseFloat(string(number), 32)
		if err != nil || math.IsInf(value, 0) {
			return ErrInvalidResponse
		}
		buffer = binary.LittleEndian.AppendUint32(buffer, math.Float32bits(float32(value)))
		return nil
	})
	if err != nil || count == 0 {
		return "", ErrInvalidResponse
	}
	return base64.StdEncoding.EncodeToString(buffer), nil
}

// scanNumbers 逐个访问一维 JSON 数字数组的元素，不为每个元素分配对象。
// raw 来自 json.Unmarshal，已保证是合法 JSON，这里只需确认它是数字数组。
func scanNumbers(raw json.RawMessage, visit func([]byte) error) (int, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '[' || raw[len(raw)-1] != ']' {
		return 0, ErrInvalidResponse
	}
	elements := bytes.TrimSpace(raw[1 : len(raw)-1])
	if len(elements) == 0 {
		return 0, nil
	}
	count := 0
	for len(elements) > 0 {
		end := bytes.IndexByte(elements, ',')
		element := elements
		if end >= 0 {
			element = elements[:end]
			elements = elements[end+1:]
		} else {
			elements = nil
		}
		element = bytes.TrimSpace(element)
		if len(element) == 0 || (element[0] != '-' && (element[0] < '0' || element[0] > '9')) {
			return 0, ErrInvalidResponse
		}
		if visit != nil {
			if err := visit(element); err != nil {
				return 0, err
			}
		}
		count++
	}
	return count, nil
}
