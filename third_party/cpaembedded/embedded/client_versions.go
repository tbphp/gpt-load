package embedded

import "github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/modelcatalog"

// 客户端兼容版本集中维护；升级 CPA 时核对各渠道请求标识与协议测试。
// Antigravity 版本由 CPA 的 misc.AntigravityLatestVersion 维护，控制面与
// 执行面共用其 UA 生成器，并通过请求合同测试校验两条路径的一致性。
const (
	// Codex 的执行身份与模型模板共用同一份客户端契约。
	CodexClientVersion  = modelcatalog.ClientVersion
	ClaudeClientVersion = "2.1.296"
	GrokClientVersion   = "1.0.44"

	claudeUserAgent          = "claude-cli/" + ClaudeClientVersion + " (external, cli)"
	claudeBootstrapUserAgent = "claude-code/" + ClaudeClientVersion
)
