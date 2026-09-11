package bifrost

import (
	"context"
	"net/http"
	"net/url"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/execution/wsnative"
	"gpt-load/internal/protocol"
)

// OpenWebsocket 复用目标、凭据和代理合同，原生 WS 不进入 HTTP SDK 的重试链。
func (manager *RuntimeManager) OpenWebsocket(ctx context.Context, spec execution.AttemptSpec) (execution.WebsocketSession, execution.WebsocketResult) {
	spec = withUserAgent(spec)
	reject := func() (execution.WebsocketSession, execution.WebsocketResult) {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "unsupported native websocket request")
		return nil, execution.WebsocketResult{DispatchState: execution.DispatchNotSent, Error: failure.Error}
	}
	if spec.Validate() != nil || spec.ClientProtocol != protocol.OpenAIResponses || spec.Operation != execution.OperationResponsesCreate || spec.RouteMode != execution.RouteNative {
		return reject()
	}
	config, failure := manager.configForAttempt(spec)
	if failure != nil {
		return nil, execution.WebsocketResult{DispatchState: execution.DispatchNotSent, Error: failure.Error}
	}
	target, err := manager.registry.ResolveExecutionTarget(channel.ID(spec.ChannelID), spec.TargetConfig)
	if err != nil || !target.ResponsesWebsocket.Native {
		return reject()
	}
	credential, err := manager.registry.ValidateCredential(channel.ID(spec.ChannelID), spec.Credential.Data())
	if err != nil {
		return reject()
	}
	apiKey, _ := credential.Value("api_key")
	if apiKey == "" {
		return reject()
	}
	base := config.targetBaseURL
	if base == "" {
		base, _, err = sdkDefaultBaseURL(target.ProviderKind)
	}
	if err != nil {
		return reject()
	}
	resource := "/responses"
	if target.ProviderKind == channel.ProviderMultiProtocolGateway {
		resource = "/v1/responses"
	}
	endpoint, err := resolveTypedTargetURL(base, resource, safeAttemptQuery(spec))
	if err != nil {
		return reject()
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return reject()
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return reject()
	}
	header := make(http.Header)
	for name, value := range safePassthroughHeaders(spec.Header) {
		header.Set(name, value)
	}
	header.Set("Authorization", "Bearer "+apiKey)
	return wsnative.Dial(ctx, u.String(), header, spec.Proxy)
}
