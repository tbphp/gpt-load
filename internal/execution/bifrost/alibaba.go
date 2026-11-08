package bifrost

import (
	"strings"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// openAICompatibleBase 报告上游是否以 OpenAI 兼容前缀执行 Chat 与转换路由。
// 百炼只额外原生支持 Responses 与 Anthropic，其余行为与 OpenAI 兼容渠道一致。
func openAICompatibleBase(providerKind channel.ProviderKind) bool {
	return providerKind == channel.ProviderOpenAICompatible || providerKind == channel.ProviderAlibaba
}

// alibabaAnthropicBaseURL 由 OpenAI 兼容前缀推导同域的 Anthropic 根地址。
// 官方与工作空间域名的前缀都以 /compatible-mode/v1 结尾，Anthropic 端点位于 /apps/anthropic；
// 其他自定义前缀去掉末尾的 /v1 后同样拼接，保证推导总能得到确定地址。
func alibabaAnthropicBaseURL(baseURL string) string {
	root := strings.TrimRight(baseURL, "/")
	if trimmed, ok := strings.CutSuffix(root, "/compatible-mode/v1"); ok {
		root = trimmed
	} else {
		root = strings.TrimSuffix(root, "/v1")
	}
	return root + "/apps/anthropic"
}

// prepareAlibabaProtocolProbe 让 Responses 与 Anthropic 的凭据测试直接请求各自的原生端点。
func prepareAlibabaProtocolProbe(
	spec execution.AttemptSpec,
	provider schemas.ModelProvider,
	directKey schemas.Key,
	secrets []string,
	baseURL string,
) (preparedAttempt, *execution.AttemptResult) {
	path, body, failure := protocolProbeRequest(spec)
	if failure != nil {
		return preparedAttempt{}, failure
	}
	upstreamURL := ""
	if spec.ClientProtocol == protocol.OpenAIResponses {
		// OpenAI 兼容前缀已包含 /v1；Anthropic 使用 provider 的推导地址。
		upstreamURL, path = baseURL, strings.TrimPrefix(path, "/v1")
	}
	return protocolProbeAttempt(spec, provider, directKey, secrets, upstreamURL, path, body), nil
}
