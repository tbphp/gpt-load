package catalog

// automaticPriceProviderPriority is the fixed provider order for automatic
// model price matching. Providers outside this list are checked by ID after it.
var automaticPriceProviderPriority = []string{
	"openai",
	"anthropic",
	"google",
	"deepseek",
	"moonshotai",
	"moonshotai-cn",
	"zhipuai",
	"zai",
	"alibaba",
	"alibaba-cn",
	"xai",
	"minimax",
	"minimax-cn",
	"mistral",
	"cohere",
	"stepfun-ai",
	"stepfun",
	"perplexity",
	"upstage",
	"xiaomi",
	"meta",
	"llama",
	"groq",
	"cerebras",
	"fireworks-ai",
	"togetherai",
	"deepinfra",
	"siliconflow",
	"siliconflow-cn",
	"nebius",
	"baseten",
	"openrouter",
}

// AutomaticPriceProviderPriority returns the fixed provider order used for
// automatic model price matching.
func AutomaticPriceProviderPriority() []string {
	return append([]string(nil), automaticPriceProviderPriority...)
}
