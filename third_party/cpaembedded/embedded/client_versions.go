package embedded

import "github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/modelcatalog"

// 客户端兼容版本集中维护；升级 CPA 时核对各渠道请求标识与协议测试。
// Antigravity 控制面与执行面共用固定版本及 CPA 的 UA 生成器，
// 避免嵌入模式未启动版本更新器时使用 SDK 中过期的回退版本。
const (
	// Codex 的执行身份与模型模板共用同一份客户端契约。
	CodexClientVersion       = modelcatalog.ClientVersion
	ClaudeClientVersion      = "2.1.304"
	GrokClientVersion        = "1.0.62"
	AntigravityClientVersion = "2.24.0"

	claudeUserAgent          = "claude-cli/" + ClaudeClientVersion + " (external, cli)"
	claudeBootstrapUserAgent = "claude-code/" + ClaudeClientVersion
	antigravityUserAgent     = "antigravity/hub/" + AntigravityClientVersion + " darwin/arm64"
)
