package modules

func volcengineModule() Module {
	return fixedCompatibleModule(
		Volcengine,
		"Volcengine Ark",
		"VE",
		"volcengine",
		[]string{"doubao", "ark"},
		"https://ark.cn-beijing.volces.com/api/v3",
		"volcengine",
	)
}
