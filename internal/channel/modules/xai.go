package modules

func xAIModule() Module {
	return nativeOpenAIModule(
		XAI,
		"xAI",
		"XA",
		"xai",
		[]string{"grok"},
		"xai",
		ProviderXAI,
		true,
	)
}
