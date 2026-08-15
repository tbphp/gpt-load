package modules

func fixedCompatibleModule(
	id ID,
	name string,
	mark string,
	icon string,
	searchTerms []string,
	fixedBaseURL string,
	catalogProviderID string,
) Module {
	return Module{Definition: Definition{
		ID:          id,
		Name:        name,
		Mark:        mark,
		Icon:        icon,
		SearchTerms: append([]string(nil), searchTerms...),
		Description: "Managed API preset",
		Connection:  apiKeyConnection(),
		Params:      optionalBaseURLFields(),
		Credentials: apiKeyFields(),
		Provider: ProviderBinding{
			ProviderKind:      ProviderOpenAICompatible,
			CatalogProviderID: catalogProviderID,
			EndpointPolicy:    EndpointFixedWithOverride,
			FixedBaseURL:      fixedBaseURL,
		},
		Routes: openAICompatibleRoutes(),
	}}
}

func nativeOpenAIModule(
	id ID,
	name string,
	mark string,
	icon string,
	searchTerms []string,
	catalogProviderID string,
	providerKind ProviderKind,
	nativeResponses bool,
) Module {
	return Module{Definition: Definition{
		ID:          id,
		Name:        name,
		Mark:        mark,
		Icon:        icon,
		SearchTerms: append([]string(nil), searchTerms...),
		Description: "Managed API preset",
		Connection:  apiKeyConnection(),
		Params:      optionalBaseURLFields(),
		Credentials: apiKeyFields(),
		Provider: ProviderBinding{
			ProviderKind:      providerKind,
			CatalogProviderID: catalogProviderID,
			EndpointPolicy:    EndpointSDKDefault,
		},
		Routes: nativeOpenAIRoutes(nativeResponses),
	}}
}
