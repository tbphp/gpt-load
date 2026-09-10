package provideradapter

import (
	"context"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// OpenWebsocket 仅转交已声明的原生能力，不从 HTTP 能力推断支持。
func (registry *Registry) OpenWebsocket(ctx context.Context, spec execution.AttemptSpec) (execution.WebsocketSession, execution.WebsocketResult) {
	adapter, evidence := registry.resolve(spec)
	if evidence != nil {
		return nil, execution.WebsocketResult{DispatchState: execution.DispatchNotSent, Error: evidence}
	}
	target, err := registry.channels.ResolveExecutionTarget(channel.ID(spec.ChannelID), spec.TargetConfig)
	opener, ok := adapter.(execution.WebsocketOpener)
	if err != nil || !ok || !target.ResponsesWebsocket.Native || spec.RouteMode != execution.RouteNative || spec.ClientProtocol != protocol.OpenAIResponses || spec.Operation != execution.OperationResponsesCreate {
		return nil, execution.WebsocketResult{DispatchState: execution.DispatchNotSent, Error: localAdapterEvidence(execution.ErrorKindInvalidRequest, "websocket_not_supported", "native websocket is not supported")}
	}
	return opener.OpenWebsocket(ctx, spec)
}
