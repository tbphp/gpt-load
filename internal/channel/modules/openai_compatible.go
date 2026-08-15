package modules

func openAICompatibleModule() Module {
	return Module{Definition: Definition{
		ID:          OpenAICompatible,
		Name:        "OpenAI Compatible",
		Mark:        "OC",
		Icon:        "compatible",
		SearchTerms: []string{"custom", "proxy", "gateway"},
		Description: "Custom OpenAI-compatible API",
		Connection:  apiKeyConnection(),
		Params:      requiredBaseURLFields(),
		Credentials: apiKeyFields(),
		Provider: ProviderBinding{
			ProviderKind:   ProviderOpenAICompatible,
			EndpointPolicy: EndpointRequiredBaseURL,
		},
		Routes: openAICompatibleRoutes(),
	}}
}
