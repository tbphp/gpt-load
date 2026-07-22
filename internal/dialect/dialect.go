package dialect

import (
	"context"
	"net/http"

	"gpt-load/internal/health"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

type ParsedRequest struct {
	Method   string
	Path     string
	RawQuery string
	Header   http.Header
	Body     []byte
}

type Dialect interface {
	Protocol() protocol.Protocol
	ExtractModel(req *ParsedRequest) (model string, stream bool, err error)
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
