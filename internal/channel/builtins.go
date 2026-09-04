package channel

import (
	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
)

// builtInModules lists every code-owned channel definition in stable product
// order. Channel modules contain only their own declaration and extensions.
func builtInModules() []spec.Module {
	return []spec.Module{
		modules.OpenAI(),
		modules.Codex(),
		modules.Claude(),
		modules.Antigravity(),
		modules.Grok(),
		modules.Anthropic(),
		modules.Gemini(),
		modules.AzureOpenAI(),
		modules.AWSBedrock(),
		modules.GoogleVertex(),
		modules.DeepSeek(),
		modules.MoonshotAI(),
		modules.SiliconFlow(),
		modules.ZhipuAI(),
		modules.Alibaba(),
		modules.Volcengine(),
		modules.OpenRouter(),
		modules.Groq(),
		modules.XAI(),
		modules.GPTLoad(),
		modules.NewAPI(),
		modules.CLIProxyAPI(),
		modules.Sub2API(),
		modules.OpenAICompatible(),
	}
}
