package dialect

import (
	"context"
	"net/http"

	"gpt-load/internal/health"
	"gpt-load/internal/protocol"
	"gpt-load/internal/reasoning"
	"gpt-load/internal/state"
	"gpt-load/internal/usage"
)

type ParsedRequest struct {
	Method   string
	Path     string
	RawQuery string
	Header   http.Header
	Body     []byte
}

type RequestMetadata struct {
	Model            *string
	Stream           bool
	ObserveUsage     bool
	UsageDiagnostics usage.Diagnostics
	Reasoning        reasoning.Config
}

type Dialect interface {
	Protocol() protocol.Protocol
	InspectRequest(req *ParsedRequest) (RequestMetadata, error)
	BuildUpstreamURL(base string, req *ParsedRequest) (string, error)
	InjectCredential(headers http.Header, apiKey string)
	ListModels(
		ctx context.Context,
		baseURL, apiKey string,
		rules state.HeaderRules,
	) ([]string, error)
	Probe(
		ctx context.Context,
		baseURL, apiKey string,
		rules state.HeaderRules,
		validationModel string,
	) error
	ClassifyStatus(status int, body []byte) health.FailureCategory
}

type ModelRewriter interface {
	RewriteRequestModel(req *ParsedRequest, upstreamModel string) (*ParsedRequest, error)
	RewriteResponseModel(body []byte, externalModel string) ([]byte, error)
}

// ResponseModelInspector optionally exposes provider-declared model identities
// from one non-streaming response object or SSE data payload. Inspection is
// observational: malformed or absent fields return no models and never change
// proxy behavior.
type ResponseModelInspector interface {
	InspectResponseModels(payload []byte) []string
}

type CredentialHeaderNamer interface {
	CredentialHeaderNames() []string
}
