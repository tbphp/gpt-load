package modules

func openRouterModule() Module {
	return nativeOpenAIModule(
		OpenRouter,
		"OpenRouter",
		"OR",
		"openrouter",
		[]string{"router"},
		"openrouter",
		ProviderOpenRouter,
		true,
	)
}
