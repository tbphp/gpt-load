package dialect

import (
	"bytes"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const audioSpeechPath = "/v1/audio/speech"

// AudioSpeech implements POST /v1/audio/speech. The gateway reads only the
// model; each channel keeps its native request and response format.
type AudioSpeech struct{}

var (
	_ Dialect       = (*AudioSpeech)(nil)
	_ ModelRewriter = (*AudioSpeech)(nil)
)

// NewAudioSpeech returns the dialect for speech synthesis requests.
func NewAudioSpeech() *AudioSpeech { return &AudioSpeech{} }

// Protocol returns the audio-speech protocol identifier.
func (*AudioSpeech) Protocol() protocol.Protocol { return protocol.AudioSpeech }

// InspectRequest reads the required model from a JSON speech request. Voice,
// format, and the other fields follow the selected channel and pass through.
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
	if metadata.Stream {
		return RequestMetadata{}, fmt.Errorf("audio speech does not support streaming")
	}
	metadata.Operation = execution.OperationAudioSpeech
	metadata.RouteRequirement = execution.RouteRequirementNative
	return metadata, nil
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
