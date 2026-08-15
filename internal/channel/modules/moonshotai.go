package modules

func moonshotAIModule() Module {
	return fixedCompatibleModule(
		MoonshotAI,
		"Moonshot AI",
		"MS",
		"moonshot",
		[]string{"kimi", "moonshot"},
		"https://api.moonshot.cn/v1",
		"moonshotai",
	)
}
