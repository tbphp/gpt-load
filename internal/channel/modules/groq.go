package modules

func groqModule() Module {
	return nativeOpenAIModule(
		Groq,
		"Groq",
		"GQ",
		"groq",
		[]string{"groqcloud"},
		"groq",
		ProviderGroq,
		false,
	)
}
