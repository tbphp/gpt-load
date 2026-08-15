package modules

func geminiModule() Module {
	return Module{Definition: Definition{
		ID:          Gemini,
		Name:        "Google Gemini",
		Mark:        "GE",
		Icon:        "gemini",
		SearchTerms: []string{"google"},
		Description: "Google Gemini official API",
		Connection:  apiKeyConnection(),
		Params:      optionalBaseURLFields(),
		Credentials: apiKeyFields(),
		Provider: ProviderBinding{
			ProviderKind:      ProviderGemini,
			CatalogProviderID: "google",
			EndpointPolicy:    EndpointSDKDefault,
		},
		Routes: geminiOfficialRoutes(),
	}}
}
