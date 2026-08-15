package modules

// All returns every built-in channel module in stable product order.
func All() []Module {
	return []Module{
		openAIModule(),
		codexModule(),
		anthropicModule(),
		geminiModule(),
		azureOpenAIModule(),
		awsBedrockModule(),
		googleVertexModule(),
		deepSeekModule(),
		moonshotAIModule(),
		siliconFlowModule(),
		zhipuAIModule(),
		alibabaModule(),
		volcengineModule(),
		openRouterModule(),
		groqModule(),
		xAIModule(),
		openAICompatibleModule(),
	}
}
