package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const audioSpeechPath = "/v1/audio/speech"

// AudioSpeech 实现 POST /v1/audio/speech，读取模型与流式模式并保留渠道原生格式。
type AudioSpeech struct{}

var (
	_ Dialect               = (*AudioSpeech)(nil)
	_ ModelRewriter         = (*AudioSpeech)(nil)
	_ StreamEventClassifier = (*AudioSpeech)(nil)
)

// NewAudioSpeech returns the dialect for speech synthesis requests.
func NewAudioSpeech() *AudioSpeech { return &AudioSpeech{} }

// Protocol returns the audio-speech protocol identifier.
func (*AudioSpeech) Protocol() protocol.Protocol { return protocol.AudioSpeech }

// InspectRequest 读取模型与原生流式开关，其余语音参数按渠道格式透传。
func (d *AudioSpeech) InspectRequest(request *ParsedRequest) (RequestMetadata, error) {
	if request == nil || request.Method != http.MethodPost || request.Path != audioSpeechPath {
		return RequestMetadata{}, fmt.Errorf("audio speech requires POST %s", audioSpeechPath)
	}
	if value := strings.TrimSpace(request.Header.Get("Content-Type")); value != "" {
		mediaType, _, err := mime.ParseMediaType(value)
		mediaType = strings.ToLower(mediaType)
		if err != nil || mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
			return RequestMetadata{}, fmt.Errorf("audio speech requires a JSON body")
		}
	}
	metadata, err := inspectJSONRequestFields(request.Body, true, false)
	if err != nil {
		return RequestMetadata{}, fmt.Errorf("decode %s request: %w", d.Protocol(), err)
	}
	streamFormat, err := inspectAudioSpeechStreamFormat(request.Body)
	if err != nil {
		return RequestMetadata{}, err
	}
	metadata.Stream = metadata.Stream || streamFormat == "sse"
	metadata.Operation = execution.OperationAudioSpeech
	metadata.RouteRequirement = execution.RouteRequirementNative
	return metadata, nil
}

// JSON 与 model、stream 已由通用检查校验，这里只补充 OpenAI 的流式控制字段。
func inspectAudioSpeechStreamFormat(body []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if _, err := decoder.Token(); err != nil {
		return "", err
	}
	format := ""
	seen := false
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return "", err
		}
		field, ok := token.(string)
		if !ok {
			return "", fmt.Errorf("audio speech field name must be a string")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return "", err
		}
		if !strings.EqualFold(field, "stream_format") {
			continue
		}
		if field != "stream_format" || seen {
			return "", fmt.Errorf("stream_format field must be unique lowercase stream_format")
		}
		seen = true
		if err := json.Unmarshal(value, &format); err != nil || (format != "audio" && format != "sse") {
			return "", fmt.Errorf("stream_format must be audio or sse")
		}
	}
	return format, nil
}

func (*AudioSpeech) RequiresTerminalEvent() bool { return true }

func (*AudioSpeech) ClassifyStreamEvent(event StreamEvent) (StreamEventClassification, error) {
	var object struct {
		Type  string          `json:"type"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(event.Payload, &object); err != nil {
		return StreamEventClassification{}, fmt.Errorf("decode Audio Speech stream event")
	}
	if event.Name != "" && object.Type != "" && event.Name != object.Type {
		return StreamEventClassification{}, fmt.Errorf("Audio Speech stream event name conflicts with payload type")
	}
	if meaningfulJSONValue(object.Error) {
		return StreamEventClassification{Disposition: StreamEventFailed}, nil
	}
	eventType := event.Name
	if eventType == "" {
		eventType = object.Type
	}
	if eventType == "" {
		return StreamEventClassification{}, fmt.Errorf("audio speech stream event type is required")
	}
	switch eventType {
	case "speech.audio.done":
		return StreamEventClassification{Disposition: StreamEventCompleted}, nil
	case "error":
		return StreamEventClassification{Disposition: StreamEventFailed}, nil
	default:
		return StreamEventClassification{Disposition: StreamEventContinue}, nil
	}
}

// RewriteRequestModel replaces the client model with the upstream model id.
func (d *AudioSpeech) RewriteRequestModel(request *ParsedRequest, model string) (*ParsedRequest, error) {
	if _, err := d.InspectRequest(request); err != nil {
		return nil, err
	}
	return rewriteJSONRequestModel(request, model, string(d.Protocol()))
}

// RewriteResponseModel writes the client-facing model back into a JSON object
// that already has a model field. Audio bytes are copied unchanged.
func (*AudioSpeech) RewriteResponseModel(body []byte, model string) ([]byte, error) {
	if err := validateModelRewriteTarget(model, false); err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return bytes.Clone(body), nil
	}
	rewritten, err := rewriteOptionalJSONField(body, "model", model)
	if err != nil {
		return bytes.Clone(body), nil
	}
	return rewritten, nil
}
