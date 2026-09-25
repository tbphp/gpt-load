package mirasim

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/translator/builtin"
)

type ExecuteRequest struct {
	Model           string
	Payload         []byte
	Format          string
	Headers         http.Header
	OriginalRequest []byte
	BaseURL         string
}

type ExecuteResponse struct {
	StatusCode          int
	Payload             []byte
	Headers             http.Header
	UpstreamRequestPath string
}

type Executor struct{}

func NewExecutor() *Executor { return &Executor{} }

func (e *Executor) Execute(ctx context.Context, credential Storage, request ExecuteRequest) (ExecuteResponse, error) {
	if e == nil {
		return ExecuteResponse{}, fmt.Errorf("Mirasim executor is unavailable")
	}
	body, route, err := buildProviderRequest(request, false)
	if err != nil {
		return ExecuteResponse{}, err
	}
	credential = applyRelayOverride(credential, request.BaseURL)
	response, err := NewClient(credential).Do(ctx, http.MethodPost, route.Path, nil, requestHeaders(request.Headers, route.Format), body)
	if err != nil {
		return ExecuteResponse{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ExecuteResponse{}, NewStatusError(response.StatusCode, response.Body, response.Header)
	}
	upstream := response.Body
	if route.Format == sdktranslator.FormatCodex {
		upstream, err = codexNonStreamPayload(response.Body)
		if err != nil {
			return ExecuteResponse{}, err
		}
	}
	payload, err := translateNonStream(ctx, route.Format, responseFormat(request.Format), normalizeModel(request.Model), request.OriginalRequest, body, upstream)
	if err != nil {
		return ExecuteResponse{}, err
	}
	headers := cloneHeader(response.Header)
	headers.Set("Content-Type", "application/json")
	return ExecuteResponse{StatusCode: response.StatusCode, Payload: payload, Headers: headers, UpstreamRequestPath: route.Path}, nil
}

func (e *Executor) ExecuteStream(ctx context.Context, credential Storage, request ExecuteRequest) (ExecuteResponse, io.ReadCloser, error) {
	if e == nil {
		return ExecuteResponse{}, nil, fmt.Errorf("Mirasim executor is unavailable")
	}
	body, route, err := buildProviderRequest(request, true)
	if err != nil {
		return ExecuteResponse{}, nil, err
	}
	credential = applyRelayOverride(credential, request.BaseURL)
	response, stream, err := NewClient(credential).DoStream(ctx, http.MethodPost, route.Path, nil, requestHeaders(request.Headers, route.Format), body)
	if err != nil {
		return ExecuteResponse{}, nil, err
	}
	headers := cloneHeader(response.Header)
	headers.Set("Content-Type", "text/event-stream")
	output := responseFormat(request.Format)
	if route.Format == output || output == "" {
		return ExecuteResponse{StatusCode: response.StatusCode, Headers: headers, UpstreamRequestPath: route.Path}, stream, nil
	}
	translated := translateStream(ctx, route.Format, output, normalizeModel(request.Model), request.OriginalRequest, body, stream)
	return ExecuteResponse{StatusCode: response.StatusCode, Headers: headers, UpstreamRequestPath: route.Path}, translated, nil
}

func (e *Executor) CountTokens(ctx context.Context, credential Storage, request ExecuteRequest) (ExecuteResponse, error) {
	source := sdktranslator.FromString(strings.TrimSpace(request.Format))
	if source == "" {
		source = sdktranslator.FormatClaude
	}
	model := normalizeModel(request.Model)
	body, err := translateRequest(source, sdktranslator.FormatClaude, model, request.Payload, false)
	if err != nil {
		return ExecuteResponse{}, err
	}
	body, err = normalizeBody(body, model, false, sdktranslator.FormatClaude)
	if err != nil {
		return ExecuteResponse{}, err
	}
	credential = applyRelayOverride(credential, request.BaseURL)
	response, err := NewClient(credential).Do(ctx, http.MethodPost, "/v1/messages/count_tokens", nil, requestHeaders(request.Headers, sdktranslator.FormatClaude), body)
	if err != nil {
		return ExecuteResponse{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ExecuteResponse{}, NewStatusError(response.StatusCode, response.Body, response.Header)
	}
	payload := append([]byte(nil), response.Body...)
	output := responseFormat(request.Format)
	if output != "" && output != sdktranslator.FormatClaude {
		var countPayload struct {
			InputTokens int64 `json:"input_tokens"`
			TotalTokens int64 `json:"total_tokens"`
		}
		if json.Unmarshal(response.Body, &countPayload) == nil {
			count := countPayload.InputTokens
			if count == 0 {
				count = countPayload.TotalTokens
			}
			payload = builtin.Registry().TranslateTokenCount(ctx, sdktranslator.FormatClaude, output, count, response.Body)
		}
	}
	headers := cloneHeader(response.Header)
	headers.Set("Content-Type", "application/json")
	return ExecuteResponse{StatusCode: response.StatusCode, Payload: payload, Headers: headers, UpstreamRequestPath: "/v1/messages/count_tokens"}, nil
}

type providerRoute struct {
	Format sdktranslator.Format
	Path   string
}

func buildProviderRequest(request ExecuteRequest, stream bool) ([]byte, providerRoute, error) {
	model := normalizeModel(request.Model)
	source := sdktranslator.FromString(strings.TrimSpace(request.Format))
	wire := selectWireFormat(model)
	body, err := translateRequest(source, wire, model, request.Payload, stream)
	if err != nil {
		return nil, providerRoute{}, err
	}
	body, err = normalizeBody(body, model, stream, wire)
	if err != nil {
		return nil, providerRoute{}, err
	}
	path := "/v1/responses"
	if wire == sdktranslator.FormatClaude {
		path = "/v1/messages"
	}
	return body, providerRoute{Format: wire, Path: path}, nil
}

func selectWireFormat(model string) sdktranslator.Format {
	switch {
	case strings.HasPrefix(strings.ToLower(model), "gpt-"):
		return sdktranslator.FormatCodex
	case strings.HasPrefix(strings.ToLower(model), "claude-"):
		return sdktranslator.FormatClaude
	default:
		return sdktranslator.FormatCodex
	}
}

func responseFormat(value string) sdktranslator.Format {
	return sdktranslator.FromString(strings.TrimSpace(value))
}

func translateRequest(from, to sdktranslator.Format, model string, body []byte, stream bool) ([]byte, error) {
	if from == "" {
		return nil, fmt.Errorf("Mirasim executor request format is missing")
	}
	if from == to {
		return append([]byte(nil), body...), nil
	}
	registry := builtin.Registry()
	if !registry.HasRequestTransformer(from, to) {
		return nil, fmt.Errorf("Mirasim executor cannot translate request %s -> %s", from, to)
	}
	return registry.TranslateRequest(from, to, model, body, stream), nil
}

func translateNonStream(ctx context.Context, from, to sdktranslator.Format, model string, originalRequest, translatedRequest, body []byte) ([]byte, error) {
	if to == "" || from == to {
		return append([]byte(nil), body...), nil
	}
	registry := builtin.Registry()
	if !registry.HasNonStreamResponseTransformer(to, from) {
		return nil, fmt.Errorf("Mirasim executor cannot translate response %s -> %s", from, to)
	}
	var state any
	return registry.TranslateNonStream(ctx, from, to, model, originalRequest, translatedRequest, body, &state), nil
}

func translateStream(ctx context.Context, from, to sdktranslator.Format, model string, originalRequest, translatedRequest []byte, input io.ReadCloser) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		defer input.Close()
		defer writer.Close()
		registry := builtin.Registry()
		if !registry.HasStreamResponseTransformer(to, from) {
			writer.CloseWithError(fmt.Errorf("Mirasim executor cannot translate stream %s -> %s", from, to))
			return
		}
		var state any
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 0, 64*1024), maxCodexEventBytes)
		for scanner.Scan() {
			line := bytes.TrimSuffix(scanner.Bytes(), []byte("\r"))
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			frames := registry.TranslateStream(ctx, from, to, model, originalRequest, translatedRequest, append([]byte(nil), line...), &state)
			for _, frame := range frames {
				if len(frame) == 0 {
					continue
				}
				if _, err := writer.Write(append(append([]byte(nil), frame...), '\n')); err != nil {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			writer.CloseWithError(err)
		}
	}()
	return reader
}

func normalizeBody(body []byte, model string, stream bool, wire sdktranslator.Format) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode translated Mirasim request: %w", err)
	}
	payload["model"] = model
	if wire == sdktranslator.FormatClaude {
		payload["stream"] = stream
		if strings.HasPrefix(strings.ToLower(model), "claude-") {
			ensureClaudeBillingHeader(payload)
		}
	} else {
		payload["stream"] = true
	}
	updated, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode translated Mirasim request: %w", err)
	}
	return updated, nil
}

// claudeBillingHeaderText is the marker Mirasim's Claude Messages route requires
// as the first system block. Claude Code sends it itself. Other clients do not,
// and the relay then rejects the whole request as invalid before looking at the
// prompt. The version is not checked; the entrypoint field is.
const claudeBillingHeaderText = "x-anthropic-billing-header: cc_version=0.0.0; cc_entrypoint=sdk-cli;"

func ensureClaudeBillingHeader(payload map[string]any) {
	if claudeBillingHeaderPresent(payload["system"]) {
		return
	}
	block := map[string]any{"type": "text", "text": claudeBillingHeaderText}
	switch system := payload["system"].(type) {
	case nil:
		payload["system"] = []any{block}
	case string:
		blocks := []any{block}
		if strings.TrimSpace(system) != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": system})
		}
		payload["system"] = blocks
	case []any:
		payload["system"] = append([]any{block}, system...)
	default:
		payload["system"] = []any{block, system}
	}
}

func claudeBillingHeaderPresent(system any) bool {
	switch value := system.(type) {
	case string:
		return isClaudeBillingHeader(value)
	case []any:
		if len(value) == 0 {
			return false
		}
		return blockHasClaudeBillingHeader(value[0])
	default:
		return false
	}
}

func blockHasClaudeBillingHeader(block any) bool {
	switch value := block.(type) {
	case string:
		return isClaudeBillingHeader(value)
	case map[string]any:
		text, _ := value["text"].(string)
		return isClaudeBillingHeader(text)
	default:
		return false
	}
}

func isClaudeBillingHeader(text string) bool {
	return strings.Contains(text, "x-anthropic-billing-header:") && strings.Contains(text, "cc_entrypoint=")
}

func requestHeaders(source http.Header, wire sdktranslator.Format) http.Header {
	headers := cloneHeader(source)
	if wire == sdktranslator.FormatClaude && headers.Get("Anthropic-Version") == "" {
		headers.Set("Anthropic-Version", "2023-06-01")
	}
	if wire == sdktranslator.FormatCodex {
		headers.Set("Accept", "text/event-stream")
	}
	return headers
}

func normalizeModel(model string) string {
	model = strings.TrimSpace(model)
	if index := strings.IndexAny(model, "(["); index > 0 {
		model = strings.TrimSpace(model[:index])
	}
	return model
}

func applyRelayOverride(credential Storage, baseURL string) Storage {
	if baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/"); baseURL != "" {
		credential.RelayURL = baseURL
	}
	return credential
}
