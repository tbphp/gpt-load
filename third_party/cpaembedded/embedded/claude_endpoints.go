package embedded

import claudeauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/claude"

// ClaudeAPIEndpoints 集中声明凭据准备完成后的业务端点，不包含 OAuth 与身份核验。
type ClaudeAPIEndpoints struct {
	ExecutionBase string
	ProfileURL    string
	RolesURL      string
	BootstrapURL  string
	UsageURL      string
}

// ResolveClaudeAPIEndpoints 保留完整官方目标集合，或将原生路径映射到代理根地址。
func ResolveClaudeAPIEndpoints(apiRoot string) (ClaudeAPIEndpoints, error) {
	endpoints := ClaudeAPIEndpoints{
		ExecutionBase: "https://api.anthropic.com",
		ProfileURL:    claudeauth.ProfileURL,
		RolesURL:      claudeauth.RolesURL,
		BootstrapURL:  ClaudeBootstrapURL,
		UsageURL:      ClaudeUsageURL,
	}
	for _, endpoint := range []*string{&endpoints.ExecutionBase, &endpoints.ProfileURL, &endpoints.RolesURL, &endpoints.BootstrapURL, &endpoints.UsageURL} {
		resolved, err := ResolveAPIEndpoint(apiRoot, *endpoint)
		if err != nil {
			return ClaudeAPIEndpoints{}, err
		}
		*endpoint = resolved
	}
	return endpoints, nil
}
