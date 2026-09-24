package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

const (
	geminiEmbedContentSuffix       = ":embedContent"
	geminiBatchEmbedContentsSuffix = ":batchEmbedContents"
)

// GeminiEmbeddings 实现 Gemini 原生 embedding 协议（:embedContent 与 :batchEmbedContents）。
// 请求和响应原样透传，只改写模型名。
type GeminiEmbeddings struct{}

var (
	_ Dialect        = (*GeminiEmbeddings)(nil)
	_ ModelRewriter  = (*GeminiEmbeddings)(nil)
	_ UsageExtractor = (*GeminiEmbeddings)(nil)
)

func NewGeminiEmbeddings() *GeminiEmbeddings {
	return &GeminiEmbeddings{}
}

func (*GeminiEmbeddings) Protocol() protocol.Protocol {
	return protocol.GeminiEmbeddings
}

func (d *GeminiEmbeddings) InspectRequest(request *ParsedRequest) (RequestMetadata, error) {
	if request == nil {
		return RequestMetadata{}, fmt.Errorf("parsed request is required")
	}
	if request.Method != http.MethodPost {
		return RequestMetadata{}, fmt.Errorf("%s only supports POST", d.Protocol())
	}
	model, batch, err := parseGeminiEmbeddingsPath(request.Path)
	if err != nil {
		return RequestMetadata{}, err
	}
	if _, err := decodeGeminiEmbeddingsBody(request.Body, batch); err != nil {
		return RequestMetadata{}, fmt.Errorf("decode %s request: %w", d.Protocol(), err)
	}
	return RequestMetadata{
		Model:            &model,
		Operation:        execution.OperationEmbeddingsCreate,
		RouteRequirement: execution.RouteRequirementNative,
		ObserveUsage:     true,
	}, nil
}

// SanitizeRequestForAttempt 为选中的上游模型重建请求：路径和请求体里的模型名都指向上游模型，
// 同时剥离不能发往上游的控制字段。
func (d *GeminiEmbeddings) SanitizeRequestForAttempt(
	request *ParsedRequest,
	model string,
) (*ParsedRequest, error) {
	rewritten, err := d.rewriteRequest(request, model, true)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(rewritten.Header.Get("Content-Type")) == "" {
		rewritten.Header.Set("Content-Type", "application/json")
	}
	return rewritten, nil
}

func (d *GeminiEmbeddings) RewriteRequestModel(request *ParsedRequest, model string) (*ParsedRequest, error) {
	return d.rewriteRequest(request, model, false)
}

// RewriteResponseModel 原样返回响应：Gemini embedding 响应不携带模型名。
func (*GeminiEmbeddings) RewriteResponseModel(body []byte, model string) ([]byte, error) {
	if err := validateModelRewriteTarget(model, true); err != nil {
		return nil, err
	}
	return body, nil
}

func (d *GeminiEmbeddings) rewriteRequest(
	request *ParsedRequest,
	model string,
	stripControls bool,
) (*ParsedRequest, error) {
	if err := validateModelRewriteTarget(model, true); err != nil {
		return nil, err
	}
	if _, err := d.InspectRequest(request); err != nil {
		return nil, err
	}
	_, batch, err := parseGeminiEmbeddingsPath(request.Path)
	if err != nil {
		return nil, err
	}
	object, err := decodeGeminiEmbeddingsBody(request.Body, batch)
	if err != nil {
		return nil, fmt.Errorf("rewrite %s request: %w", d.Protocol(), err)
	}
	if stripControls {
		for field := range object {
			if openAIEmbeddingsControlField(field) {
				delete(object, field)
			}
		}
	}
	resourceName, err := json.Marshal("models/" + model)
	if err != nil {
		return nil, fmt.Errorf("encode %s model: %w", d.Protocol(), err)
	}
	if batch {
		if err := rewriteGeminiBatchRequestModels(object, resourceName); err != nil {
			return nil, fmt.Errorf("rewrite %s request: %w", d.Protocol(), err)
		}
	} else if _, exists := object["model"]; exists {
		object["model"] = resourceName
	}
	body, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode %s request: %w", d.Protocol(), err)
	}
	clone, err := cloneParsedRequest(request)
	if err != nil {
		return nil, err
	}
	suffix := geminiEmbedContentSuffix
	if batch {
		suffix = geminiBatchEmbedContentsSuffix
	}
	clone.Path = geminiGenerationPrefix + model + suffix
	clone.Body = body
	if clone.Header == nil {
		clone.Header = make(http.Header)
	}
	return clone, nil
}

// rewriteGeminiBatchRequestModels 给每条子请求写入同一个模型：Gemini 要求子请求的模型和路径一致。
func rewriteGeminiBatchRequestModels(object map[string]json.RawMessage, resourceName json.RawMessage) error {
	var requests []map[string]json.RawMessage
	if err := json.Unmarshal(object["requests"], &requests); err != nil {
		return fmt.Errorf("decode requests: %w", err)
	}
	for _, item := range requests {
		item["model"] = resourceName
	}
	encoded, err := json.Marshal(requests)
	if err != nil {
		return fmt.Errorf("encode requests: %w", err)
	}
	object["requests"] = encoded
	return nil
}

func parseGeminiEmbeddingsPath(path string) (model string, batch bool, err error) {
	modelAndMethod, ok := strings.CutPrefix(path, geminiGenerationPrefix)
	if !ok {
		return "", false, fmt.Errorf("invalid Gemini embeddings path")
	}
	switch {
	case strings.HasSuffix(modelAndMethod, geminiBatchEmbedContentsSuffix):
		model = strings.TrimSuffix(modelAndMethod, geminiBatchEmbedContentsSuffix)
		batch = true
	case strings.HasSuffix(modelAndMethod, geminiEmbedContentSuffix):
		model = strings.TrimSuffix(modelAndMethod, geminiEmbedContentSuffix)
	default:
		return "", false, fmt.Errorf("invalid Gemini embeddings method")
	}
	if model == "" || strings.Contains(model, "/") || strings.TrimSpace(model) != model {
		return "", false, fmt.Errorf("invalid Gemini model")
	}
	return model, batch, nil
}

// decodeGeminiEmbeddingsBody 只校验改写模型名所需的结构，其余字段交给上游校验。
func decodeGeminiEmbeddingsBody(body []byte, batch bool) (map[string]json.RawMessage, error) {
	object, err := decodeJSONObject(body)
	if err != nil {
		return nil, err
	}
	if !batch {
		if _, err := decodeJSONObject(object["content"]); err != nil {
			return nil, fmt.Errorf("content must be an object")
		}
		return object, nil
	}
	var requests []json.RawMessage
	if err := json.Unmarshal(object["requests"], &requests); err != nil || len(requests) == 0 {
		return nil, fmt.Errorf("requests must be a non-empty array")
	}
	for _, item := range requests {
		if _, err := decodeJSONObject(item); err != nil {
			return nil, fmt.Errorf("each request must be an object")
		}
	}
	return object, nil
}

func (*GeminiEmbeddings) ExtractUsage(body []byte) (usage.Result, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return usage.Result{}, fmt.Errorf("decode Gemini Embeddings usage response")
	}
	// 只取 usageMetadata，避免为向量字段复制整份响应。
	var envelope struct {
		UsageMetadata json.RawMessage `json:"usageMetadata"`
	}
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return usage.Result{}, fmt.Errorf("decode Gemini Embeddings usage response")
	}
	root := make(map[string]json.RawMessage, 1)
	if len(envelope.UsageMetadata) > 0 {
		root["usageMetadata"] = envelope.UsageMetadata
	}
	patch, found := geminiUsagePatch(root, true)
	var accumulator usage.Accumulator
	if found {
		if err := accumulator.ReplaceSnapshot(patch); err != nil {
			return usage.Result{}, fmt.Errorf("normalize Gemini Embeddings usage response")
		}
	} else if err := accumulator.MergePatch(patch); err != nil {
		return usage.Result{}, fmt.Errorf("normalize Gemini Embeddings usage response")
	}
	result, _ := accumulator.Finalize(true)
	return result, nil
}

func (*GeminiEmbeddings) NewUsageStreamExtractor() UsageStreamExtractor {
	return &openAIEmbeddingsUnsupportedStreamUsage{}
}
