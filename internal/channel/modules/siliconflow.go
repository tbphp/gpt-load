package modules

func siliconFlowModule() Module {
	return fixedCompatibleModule(
		SiliconFlow,
		"SiliconFlow",
		"SF",
		"siliconcloud",
		[]string{"silicon flow"},
		"https://api.siliconflow.cn/v1",
		"siliconflow",
	)
}
