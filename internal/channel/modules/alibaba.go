package modules

func alibabaModule() Module {
	return fixedCompatibleModule(
		Alibaba,
		"Alibaba Cloud Bailian",
		"BL",
		"alibabacloud",
		[]string{"dashscope", "qwen", "bailian"},
		"https://dashscope.aliyuncs.com/compatible-mode/v1",
		"alibaba",
	)
}
