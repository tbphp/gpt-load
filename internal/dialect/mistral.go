package dialect

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// Mistral implements Mistral-native HTTP routes that are not OpenAI chat or
// embeddings. Paths follow the current model pages on docs.mistral.ai, plus
// the file and job subpaths those features need.
type Mistral struct{}

var (
	_ Dialect       = (*Mistral)(nil)
	_ ModelRewriter = (*Mistral)(nil)
)

// NewMistral returns the dialect for Mistral-native HTTP routes.
func NewMistral() *Mistral { return &Mistral{} }

// Protocol returns the mistral protocol identifier.
func (*Mistral) Protocol() protocol.Protocol { return protocol.Mistral }

// InspectRequest classifies a Mistral-native path and reads its model when the
// operation requires one. Files, batch, agents, conversations, and voices do
// not require a model.
func (d *Mistral) InspectRequest(request *ParsedRequest) (RequestMetadata, error) {
	if request == nil {
		return RequestMetadata{}, fmt.Errorf("parsed request is required")
	}
	operation, err := classifyMistralRequest(request.Method, request.Path)
	if err != nil {
		return RequestMetadata{}, err
	}
	model, stream, err := inspectMistralBody(request, operation)
	if err != nil {
		return RequestMetadata{}, fmt.Errorf("decode %s request: %w", d.Protocol(), err)
	}
	if stream && !mistralAllowsStream(operation) {
		return RequestMetadata{}, fmt.Errorf("%s does not support streaming", operation)
	}
	if mistralModelRequired(operation) && model == "" {
		return RequestMetadata{}, fmt.Errorf("%s model is required", operation)
	}
	metadata := RequestMetadata{
		Stream:           stream,
		Operation:        operation,
		RouteRequirement: execution.RouteRequirementNative,
	}
	if model != "" {
		metadata.Model = &model
	}
	return metadata, nil
}

// RewriteRequestModel replaces the client model with the upstream model id.
// Requests without a model are cloned unchanged.
func (d *Mistral) RewriteRequestModel(request *ParsedRequest, model string) (*ParsedRequest, error) {
	if err := validateModelRewriteTarget(model, false); err != nil {
		return nil, err
	}
	metadata, err := d.InspectRequest(request)
	if err != nil {
		return nil, err
	}
	if metadata.Model == nil {
		return cloneParsedRequest(request)
	}
	mediaType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if strings.EqualFold(mediaType, "multipart/form-data") {
		body, contentType, err := rewriteMultipartModel(request.Body, request.Header.Get("Content-Type"), model)
		if err != nil {
			return nil, fmt.Errorf("rewrite %s multipart request: %w", d.Protocol(), err)
		}
		clone, err := cloneParsedRequest(request)
		if err != nil {
			return nil, err
		}
		clone.Body = body
		if clone.Header == nil {
			clone.Header = make(http.Header)
		}
		clone.Header.Set("Content-Type", contentType)
		return clone, nil
	}
	return rewriteJSONRequestModel(request, model, string(d.Protocol()))
}

// RewriteResponseModel writes the client-facing model back into a JSON object
// that already has a model field. Non-object bodies are copied unchanged.
func (*Mistral) RewriteResponseModel(body []byte, model string) ([]byte, error) {
	if err := validateModelRewriteTarget(model, false); err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return append([]byte(nil), body...), nil
	}
	rewritten, err := rewriteOptionalJSONField(body, "model", model)
	if err != nil {
		return append([]byte(nil), body...), nil
	}
	return rewritten, nil
}

// classifyMistralRequest maps one method and path to a Mistral-native
// operation. Realtime transcription is the GET that upgrades
// /v1/audio/transcriptions/realtime.
func classifyMistralRequest(method, path string) (execution.Operation, error) {
	switch {
	case method == http.MethodPost && path == "/v1/ocr":
		return execution.OperationMistralOCR, nil
	case method == http.MethodPost && path == "/v1/fim/completions":
		return execution.OperationMistralFIM, nil
	case method == http.MethodPost && path == "/v1/audio/transcriptions":
		return execution.OperationMistralAudioTranscription, nil
	case method == http.MethodGet && path == "/v1/audio/transcriptions/realtime":
		return execution.OperationMistralRealtimeTranscription, nil
	case method == http.MethodPost && path == "/v1/audio/speech":
		return execution.OperationMistralAudioSpeech, nil
	case method == http.MethodPost && path == "/v1/moderations":
		return execution.OperationMistralModeration, nil
	case method == http.MethodPost && path == "/v1/chat/moderations":
		return execution.OperationMistralChatModeration, nil
	case method == http.MethodPost && path == "/v1/classifications":
		return execution.OperationMistralClassification, nil
	case mistralVoiceMethod(method) && (mistralSubtree(path, "/mistral/v1/audio/voices") || mistralSubtree(path, "/mistral/v2/audio/voices")):
		return execution.OperationMistralVoices, nil
	default:
		return "", fmt.Errorf("unsupported mistral request %s %s", method, path)
	}
}

// mistralModelRequired reports whether the operation must name a model.
func mistralModelRequired(operation execution.Operation) bool {
	switch operation {
	case execution.OperationMistralOCR,
		execution.OperationMistralFIM,
		execution.OperationMistralAudioTranscription,
		execution.OperationMistralAudioSpeech,
		execution.OperationMistralModeration,
		execution.OperationMistralChatModeration,
		execution.OperationMistralClassification,
		execution.OperationMistralRealtimeTranscription:
		return true
	default:
		return false
	}
}

// mistralAllowsStream reports whether the operation may set stream=true.
// The remaining Mistral operations are unary.
func mistralAllowsStream(operation execution.Operation) bool {
	return false
}

// mistralVoiceMethod reports whether the method is valid for a voice path.
func mistralVoiceMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodHead:
		return true
	default:
		return false
	}
}

// mistralSubtree reports whether path is root or a safe child of root.
func mistralSubtree(path, root string) bool {
	if path == root {
		return true
	}
	prefix := root + "/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" || strings.Contains(rest, "//") {
		return false
	}
	for _, segment := range strings.Split(rest, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// inspectMistralBody reads the model and stream flag for one operation.
// Realtime transcription takes the model from the query, not the body.
func inspectMistralBody(request *ParsedRequest, operation execution.Operation) (string, bool, error) {
	if operation == execution.OperationMistralRealtimeTranscription {
		return inspectMistralRealtimeModel(request)
	}
	if len(request.Body) == 0 {
		return "", false, nil
	}
	contentType := ""
	if request.Header != nil {
		contentType = request.Header.Get("Content-Type")
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if contentType != "" && err != nil {
		return "", false, fmt.Errorf("decode content type: %w", err)
	}
	mediaType = strings.ToLower(mediaType)
	if mediaType == "multipart/form-data" {
		model, found, err := readMultipartModel(request.Body, contentType)
		if err != nil {
			return "", false, err
		}
		if !found {
			return "", false, nil
		}
		return model, false, nil
	}
	trimmed := bytes.TrimSpace(request.Body)
	jsonBody := mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") ||
		(mediaType == "" && len(trimmed) > 0 && trimmed[0] == '{')
	if !jsonBody {
		if mistralModelRequired(operation) {
			return "", false, fmt.Errorf("unsupported content type %q", contentType)
		}
		return "", false, nil
	}
	metadata, err := inspectJSONRequestFields(request.Body, false, false)
	if err != nil {
		return "", false, err
	}
	model := ""
	if metadata.Model != nil {
		model = *metadata.Model
	}
	return model, metadata.Stream, nil
}

// inspectMistralRealtimeModel reads the required model query parameter.
func inspectMistralRealtimeModel(request *ParsedRequest) (string, bool, error) {
	if request == nil {
		return "", false, fmt.Errorf("parsed request is required")
	}
	values, err := url.ParseQuery(request.RawQuery)
	if err != nil {
		return "", false, fmt.Errorf("decode query: %w", err)
	}
	model := values.Get("model")
	if model == "" || strings.TrimSpace(model) != model {
		return "", false, fmt.Errorf("model query parameter is required")
	}
	return model, false, nil
}

// readMultipartModel finds the model field in a multipart body.
func readMultipartModel(body []byte, contentType string) (string, bool, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", false, fmt.Errorf("decode multipart content type: %w", err)
	}
	boundary := params["boundary"]
	if boundary == "" {
		return "", false, fmt.Errorf("multipart boundary is required")
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	found := false
	model := ""
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false, fmt.Errorf("read multipart body: %w", err)
		}
		name := part.FormName()
		filename := part.FileName()
		if name == "model" && filename == "" {
			if found {
				_ = part.Close()
				return "", false, fmt.Errorf("model field must be unique")
			}
			raw, err := io.ReadAll(io.LimitReader(part, 256))
			extra := make([]byte, 1)
			n, _ := part.Read(extra)
			_ = part.Close()
			if err != nil {
				return "", false, fmt.Errorf("read model field: %w", err)
			}
			if n > 0 {
				return "", false, fmt.Errorf("model field is too long")
			}
			value := string(raw)
			if value == "" || strings.TrimSpace(value) != value {
				return "", false, fmt.Errorf("model must be non-empty without boundary whitespace")
			}
			model = value
			found = true
			continue
		}
		_, _ = io.Copy(io.Discard, part)
		_ = part.Close()
	}
	return model, found, nil
}

// rewriteMultipartModel replaces the model field and returns the new body
// and Content-Type.
func rewriteMultipartModel(body []byte, contentType, model string) ([]byte, string, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, "", fmt.Errorf("decode multipart content type: %w", err)
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, "", fmt.Errorf("multipart boundary is required")
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var encoded bytes.Buffer
	writer := multipart.NewWriter(&encoded)
	replaced := false
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("read multipart body: %w", err)
		}
		if part.FormName() == "model" && part.FileName() == "" {
			if replaced {
				_ = part.Close()
				return nil, "", fmt.Errorf("model field must be unique")
			}
			field, err := writer.CreateFormField("model")
			if err != nil {
				_ = part.Close()
				return nil, "", err
			}
			if _, err := io.WriteString(field, model); err != nil {
				_ = part.Close()
				return nil, "", err
			}
			_, _ = io.Copy(io.Discard, part)
			_ = part.Close()
			replaced = true
			continue
		}
		destination, err := writer.CreatePart(part.Header)
		if err != nil {
			_ = part.Close()
			return nil, "", err
		}
		if _, err := io.Copy(destination, part); err != nil {
			_ = part.Close()
			return nil, "", err
		}
		_ = part.Close()
	}
	if !replaced {
		return nil, "", fmt.Errorf("model field is required")
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return encoded.Bytes(), writer.FormDataContentType(), nil
}

// MistralUpstreamTarget maps a client path onto the channel base URL.
// Model operations use the public /v1 path. Model-less operations use a
// /mistral prefix, which is removed before the path is appended.
// Paths under /v1 are appended to a base that already ends in /v1.
// /v2/audio/voices is a sibling of that prefix, not a child of it.
func MistralUpstreamTarget(baseURL, clientPath string) (string, string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return "", "", fmt.Errorf("mistral base URL is required")
	}
	clientPath = strings.TrimPrefix(clientPath, "/mistral")
	if strings.HasPrefix(clientPath, "/v2/") {
		if !mistralSafeAbsolutePath(clientPath) {
			return "", "", fmt.Errorf("mistral path %q is invalid", clientPath)
		}
		if !strings.HasSuffix(baseURL, "/v1") {
			return "", "", fmt.Errorf("mistral v2 path requires a base URL ending in /v1")
		}
		return strings.TrimSuffix(baseURL, "/v1"), clientPath, nil
	}
	path, err := MistralUpstreamPath(clientPath)
	if err != nil {
		return "", "", err
	}
	return baseURL, path, nil
}

// MistralUpstreamPath maps a client /v1 path onto a base URL that already
// ends in /v1, matching the other OpenAI-compatible presets.
func MistralUpstreamPath(clientPath string) (string, error) {
	if !strings.HasPrefix(clientPath, "/v1/") {
		return "", fmt.Errorf("mistral path %q is outside /v1", clientPath)
	}
	path := strings.TrimPrefix(clientPath, "/v1")
	if !mistralSafeAbsolutePath(clientPath) || path == "" {
		return "", fmt.Errorf("mistral path %q is invalid", clientPath)
	}
	return path, nil
}

// mistralSafeAbsolutePath reports whether path is an absolute path without
// dot segments or repeated slashes.
func mistralSafeAbsolutePath(path string) bool {
	return path != "" && !strings.Contains(path, "//") && !strings.Contains(path, "/./") && !strings.Contains(path, "/../") && !strings.HasSuffix(path, "/.") && !strings.HasSuffix(path, "/..")
}
