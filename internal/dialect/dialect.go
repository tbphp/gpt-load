package dialect

import (
	"net/http"

	"gpt-load/internal/execution"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/reasoning"
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
	Model                    *string
	Stream                   bool
	AffinityPrefix           []byte
	Operation                execution.Operation
	RouteRequirement         execution.RouteRequirement
	ResponsesStorePreference execution.ResponsesStorePreference
	ObserveUsage             bool
	PricingMode              pricing.Mode
	UsageDiagnostics         usage.Diagnostics
	Reasoning                reasoning.Config
}

type Dialect interface {
	Protocol() protocol.Protocol
	InspectRequest(req *ParsedRequest) (RequestMetadata, error)
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
