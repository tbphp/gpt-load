package claude

import (
	"context"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
)

// claudeAPIOptions 只为凭据准备完成后的业务调用设置代理目标。
func claudeAPIOptions(ctx context.Context, apiRoot string) (cpaembedded.ClaudeOptions, error) {
	options, err := claudeOptions(ctx)
	if err != nil {
		return cpaembedded.ClaudeOptions{}, err
	}
	endpoints, err := cpaembedded.ResolveClaudeAPIEndpoints(apiRoot)
	if err != nil {
		return cpaembedded.ClaudeOptions{}, err
	}
	options.ProfileURL = endpoints.ProfileURL
	options.RolesURL = endpoints.RolesURL
	options.BootstrapURL = endpoints.BootstrapURL
	options.UsageURL = endpoints.UsageURL
	return options, nil
}
