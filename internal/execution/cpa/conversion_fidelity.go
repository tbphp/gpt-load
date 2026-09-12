package cpa

import (
	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func convertedInstructionFailure(spec execution.AttemptSpec, providerKind channel.ProviderKind) *execution.ErrorEvidence {
	if spec.RouteMode != execution.RouteConverted ||
		(spec.Operation != execution.OperationChatCompletion && spec.Operation != execution.OperationResponsesCreate) ||
		dialect.CountMidConversationSystemMessages(spec.ClientProtocol, spec.Body) == 0 {
		return nil
	}
	lossy := false
	switch providerKind {
	case channel.ProviderClaude, channel.ProviderAntigravity:
		lossy = true
	case channel.ProviderCodex, channel.ProviderGrok:
		// CPA 的 Claude 转换器把中途 system 包成 user；OpenAI 输入能原位映射为 developer。
		lossy = spec.ClientProtocol == protocol.Anthropic
	}
	if !lossy {
		return nil
	}
	return notSentEvidence(execution.ErrorKindConversionUnsupported,
		"conversion cannot preserve mid-conversation system instructions", execution.ErrorCodeCriticalSemanticLoss)
}
