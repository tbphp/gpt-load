package modules

func openAIModule() Module {
	return Module{Definition: Definition{
		ID:          OpenAI,
		Name:        "OpenAI",
		Mark:        "OA",
		Icon:        "openai",
		SearchTerms: []string{"gpt"},
		Description: "OpenAI official API",
		Connection:  apiKeyConnection(),
		Params:      optionalBaseURLFields(),
		Credentials: apiKeyFields(),
		Provider: ProviderBinding{
			ProviderKind:      ProviderOpenAI,
			CatalogProviderID: "openai",
			EndpointPolicy:    EndpointSDKDefault,
		},
		Routes: openAIOfficialRoutes(),
	}}
}
