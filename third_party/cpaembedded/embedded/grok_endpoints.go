package embedded

import xaiauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/xai"

// GrokAPIEndpoints 集中声明凭据准备完成后的业务端点，不包含 OAuth 与身份核验。
type GrokAPIEndpoints struct {
	ExecutionBase     string
	ModelsURL         string
	BillingWeeklyURL  string
	BillingMonthlyURL string
}

// ResolveGrokAPIEndpoints 保留完整官方目标集合，或将原生路径映射到代理根地址。
func ResolveGrokAPIEndpoints(apiRoot string) (GrokAPIEndpoints, error) {
	endpoints := GrokAPIEndpoints{
		ExecutionBase:     xaiauth.CLIChatProxyBaseURL,
		ModelsURL:         grokModelsURL,
		BillingWeeklyURL:  grokBillingWeeklyURL,
		BillingMonthlyURL: grokBillingMonthlyURL,
	}
	for _, endpoint := range []*string{&endpoints.ExecutionBase, &endpoints.ModelsURL, &endpoints.BillingWeeklyURL, &endpoints.BillingMonthlyURL} {
		resolved, err := ResolveAPIEndpoint(apiRoot, *endpoint)
		if err != nil {
			return GrokAPIEndpoints{}, err
		}
		*endpoint = resolved
	}
	return endpoints, nil
}
