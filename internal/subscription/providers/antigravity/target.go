package antigravity

import (
	"context"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
)

// antigravityAPIOptions 只为凭据准备完成后的业务调用设置代理目标。
func antigravityAPIOptions(ctx context.Context, apiRoot string) (cpaembedded.AntigravityOptions, error) {
	options, err := antigravityOptions(ctx)
	if err != nil {
		return cpaembedded.AntigravityOptions{}, err
	}
	endpoints, err := cpaembedded.ResolveAntigravityAPIEndpoints(apiRoot)
	if err != nil {
		return cpaembedded.AntigravityOptions{}, err
	}
	options.FetchModelsURL = endpoints.FetchModelsURL
	options.LoadCodeAssistURL = endpoints.LoadCodeAssistURL
	options.RetrieveUserQuotaURL = endpoints.RetrieveUserQuotaURL
	return options, nil
}
