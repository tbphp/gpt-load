package grok

import (
	"context"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
)

// grokAPIOptions 只为凭据准备完成后的业务调用设置代理目标。
func grokAPIOptions(ctx context.Context, apiRoot string) (cpaembedded.GrokOptions, error) {
	options, err := grokOptions(ctx)
	if err != nil {
		return cpaembedded.GrokOptions{}, err
	}
	endpoints, err := cpaembedded.ResolveGrokAPIEndpoints(apiRoot)
	if err != nil {
		return cpaembedded.GrokOptions{}, err
	}
	options.ModelsURL = endpoints.ModelsURL
	options.BillingWeeklyURL = endpoints.BillingWeeklyURL
	options.BillingMonthlyURL = endpoints.BillingMonthlyURL
	return options, nil
}
