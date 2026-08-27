package bifrost

import (
	"fmt"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// ValidateRouteCapability reports whether the Bifrost-backed implementation
// can execute one fixed route shape. Channel modules choose a subset of this
// implementation bound; this method does not make product routing decisions.
func (manager *RuntimeManager) ValidateRouteCapability(
	providerKind channel.ProviderKind,
	route channel.RouteDescriptor,
) error {
	if manager == nil {
		return fmt.Errorf("runtime manager is unavailable")
	}
	if _, sdkBacked := sdkProviderSpecFor(providerKind); !sdkBacked && providerKind != channel.ProviderOpenAICompatible {
		return fmt.Errorf("provider is not implemented by Bifrost")
	}
	if route.RouteMode == execution.RouteConverted {
		if convertedRouteImplemented(route.ClientProtocol, route.Operation) {
			return nil
		}
		return fmt.Errorf("converted route is not implemented")
	}
	if route.RouteMode != execution.RouteNative || !nativeRouteImplemented(providerKind, route.ClientProtocol, route.Operation) {
		return fmt.Errorf("native route is not implemented")
	}
	return nil
}

func convertedRouteImplemented(clientProtocol protocol.Protocol, operation execution.Operation) bool {
	switch operation {
	case execution.OperationListModels:
		return clientProtocol != protocol.OpenAIResponses && clientProtocol.Valid()
	case execution.OperationProbe:
		return clientProtocol.Valid()
	case execution.OperationChatCompletion:
		return clientProtocol == protocol.OpenAICompletions ||
			clientProtocol == protocol.Anthropic ||
			clientProtocol == protocol.Gemini
	case execution.OperationResponsesCreate:
		return clientProtocol == protocol.OpenAIResponses
	case execution.OperationResponsesInputTokens:
		return clientProtocol == protocol.OpenAIResponses
	case execution.OperationCountTokens:
		return clientProtocol == protocol.Anthropic || clientProtocol == protocol.Gemini
	default:
		return false
	}
}

func nativeRouteImplemented(
	providerKind channel.ProviderKind,
	clientProtocol protocol.Protocol,
	operation execution.Operation,
) bool {
	switch providerKind {
	case channel.ProviderOpenAI:
		if clientProtocol == protocol.OpenAIImages {
			return operation == execution.OperationImagesGenerate || operation == execution.OperationImagesEdit
		}
		return nativeOpenAIProtocolOperation(clientProtocol, operation, true)
	case channel.ProviderAnthropic:
		return clientProtocol == protocol.Anthropic && standardProtocolOperation(clientProtocol, operation)
	case channel.ProviderGemini:
		return clientProtocol == protocol.Gemini && standardProtocolOperation(clientProtocol, operation)
	case channel.ProviderOpenAICompatible:
		if clientProtocol == protocol.OpenAIImages {
			return operation == execution.OperationImagesGenerate || operation == execution.OperationImagesEdit
		}
		return clientProtocol == protocol.OpenAICompletions && standardProtocolOperation(clientProtocol, operation)
	case channel.ProviderDeepSeek:
		switch clientProtocol {
		case protocol.OpenAICompletions, protocol.OpenAIResponses:
			return standardProtocolOperation(clientProtocol, operation)
		case protocol.Anthropic:
			return operation == execution.OperationChatCompletion || operation == execution.OperationProbe
		default:
			return false
		}
	case channel.ProviderOpenRouter, channel.ProviderXAI:
		return nativeOpenAIProtocolOperation(clientProtocol, operation, false)
	case channel.ProviderGroq:
		return clientProtocol == protocol.OpenAICompletions && standardProtocolOperation(clientProtocol, operation)
	case channel.ProviderGoogleVertex:
		return clientProtocol == protocol.Gemini &&
			(operation == execution.OperationChatCompletion || operation == execution.OperationProbe)
	default:
		return false
	}
}

func nativeOpenAIProtocolOperation(
	clientProtocol protocol.Protocol,
	operation execution.Operation,
	responsesLifecycle bool,
) bool {
	if standardProtocolOperation(clientProtocol, operation) {
		return clientProtocol == protocol.OpenAICompletions || clientProtocol == protocol.OpenAIResponses
	}
	return responsesLifecycle && clientProtocol == protocol.OpenAIResponses && nativeResponsesLifecycleOperation(operation)
}

func nativeResponsesLifecycleOperation(operation execution.Operation) bool {
	switch operation {
	case execution.OperationResponsesRetrieve,
		execution.OperationResponsesDelete,
		execution.OperationResponsesCancel,
		execution.OperationResponsesInputItems,
		execution.OperationResponsesCompact,
		execution.OperationResponsesInputTokens,
		execution.OperationResponsesPassthrough:
		return true
	default:
		return false
	}
}

func standardProtocolOperation(clientProtocol protocol.Protocol, operation execution.Operation) bool {
	switch clientProtocol {
	case protocol.OpenAICompletions, protocol.Anthropic, protocol.Gemini:
		return operation == execution.OperationChatCompletion ||
			((clientProtocol == protocol.Anthropic || clientProtocol == protocol.Gemini) && operation == execution.OperationCountTokens) ||
			operation == execution.OperationListModels ||
			operation == execution.OperationProbe
	case protocol.OpenAIResponses:
		return operation == execution.OperationResponsesCreate ||
			operation == execution.OperationProbe
	default:
		return false
	}
}
