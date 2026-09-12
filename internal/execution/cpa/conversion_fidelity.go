package cpa

import (
	"strconv"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func prepareConvertedInstructions(spec execution.AttemptSpec, providerKind channel.ProviderKind) (execution.AttemptSpec, *execution.ErrorEvidence) {
	if spec.RouteMode != execution.RouteConverted ||
		(spec.Operation != execution.OperationChatCompletion && spec.Operation != execution.OperationResponsesCreate &&
			spec.Operation != execution.OperationCountTokens) {
		return spec, nil
	}
	if (providerKind == channel.ProviderCodex || providerKind == channel.ProviderGrok) && spec.ClientProtocol == protocol.Anthropic {
		// 两个渠道共用 Codex 转换器；先映射角色，避免 CPA 将 system 降为 user 提醒并移动位置。
		for index, message := range gjson.GetBytes(spec.Body, "messages").Array() {
			if message.Get("role").String() != "system" {
				continue
			}
			body, err := sjson.SetBytes(spec.Body, "messages."+strconv.Itoa(index)+".role", "developer")
			if err != nil {
				return spec, notSentEvidence(execution.ErrorKindInvalidRequest,
					"cannot preserve subscription system instructions", "invalid_subscription_instructions")
			}
			spec.Body = body
		}
		return spec, nil
	}
	if spec.Operation == execution.OperationCountTokens ||
		dialect.CountMidConversationSystemMessages(spec.ClientProtocol, spec.Body) == 0 {
		return spec, nil
	}
	lossy := false
	switch providerKind {
	case channel.ProviderClaude, channel.ProviderAntigravity:
		lossy = true
	}
	if !lossy {
		return spec, nil
	}
	return spec, notSentEvidence(execution.ErrorKindConversionUnsupported,
		"conversion cannot preserve mid-conversation system instructions", execution.ErrorCodeCriticalSemanticLoss)
}
