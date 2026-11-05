package gateway

import (
	"gpt-load/internal/channel"
	"gpt-load/internal/codexrecovery"
	"gpt-load/internal/dialect"
	"gpt-load/internal/protocol"
)

// codexResponsesRequest adjusts only this candidate's outgoing copy. Never put
// it in the prepared-request cache or continuation-recovery state: other
// providers must receive their own unmodified reasoning history.
func codexResponsesRequest(request *dialect.ParsedRequest, clientProtocol protocol.Protocol, provider channel.ProviderKind) *dialect.ParsedRequest {
	if clientProtocol != protocol.OpenAIResponses || provider != channel.ProviderCodex {
		return request
	}
	if body, changed := codexrecovery.StripReasoningStatus(request.Body); changed {
		return withRequestBody(request, body)
	}
	return request
}
