package embedded

import antigravityauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/antigravity"

// AntigravityAPIEndpoints 集中声明凭据准备完成后的业务端点，不包含 OAuth 与身份核验。
type AntigravityAPIEndpoints struct {
	ExecutionBase        string
	FetchModelsURL       string
	LoadCodeAssistURL    string
	RetrieveUserQuotaURL string
}

// ResolveAntigravityAPIEndpoints 保留完整官方目标集合，或将原生路径映射到代理根地址。
func ResolveAntigravityAPIEndpoints(apiRoot string) (AntigravityAPIEndpoints, error) {
	endpoints := AntigravityAPIEndpoints{
		ExecutionBase:        antigravityExecutionBase,
		FetchModelsURL:       antigravityExecutionBase + "/" + antigravityauth.APIVersion + ":fetchAvailableModels",
		LoadCodeAssistURL:    antigravityauth.APIEndpoint + "/" + antigravityauth.APIVersion + ":loadCodeAssist",
		RetrieveUserQuotaURL: antigravityauth.DailyAPIEndpoint + "/" + antigravityauth.APIVersion + ":retrieveUserQuotaSummary",
	}
	for _, endpoint := range []*string{&endpoints.ExecutionBase, &endpoints.FetchModelsURL, &endpoints.LoadCodeAssistURL, &endpoints.RetrieveUserQuotaURL} {
		resolved, err := ResolveAPIEndpoint(apiRoot, *endpoint)
		if err != nil {
			return AntigravityAPIEndpoints{}, err
		}
		*endpoint = resolved
	}
	return endpoints, nil
}
