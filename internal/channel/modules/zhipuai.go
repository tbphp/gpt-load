package modules

func zhipuAIModule() Module {
	return fixedCompatibleModule(
		ZhipuAI,
		"Zhipu AI",
		"ZP",
		"zhipu",
		[]string{"glm", "bigmodel"},
		"https://open.bigmodel.cn/api/paas/v4",
		"zhipuai",
	)
}
