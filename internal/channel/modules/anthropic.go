package modules

func anthropicModule() Module {
	return Module{Definition: Definition{
		ID:          Anthropic,
		Name:        "Anthropic",
		Mark:        "AN",
		Icon:        "anthropic",
		SearchTerms: []string{"claude"},
		Description: "Anthropic official API",
		Connection:  apiKeyConnection(),
		Params:      optionalBaseURLFields(),
		Credentials: apiKeyFields(),
		Provider: ProviderBinding{
			ProviderKind:      ProviderAnthropic,
			CatalogProviderID: "anthropic",
			EndpointPolicy:    EndpointSDKDefault,
		},
		Routes: anthropicOfficialRoutes(),
	}}
}
