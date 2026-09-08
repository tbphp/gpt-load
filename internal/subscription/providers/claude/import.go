package claude

import (
	"context"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"

	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func (err *TokenEndpointError) HTTPStatusCode() int { return err.StatusCode }
func (err *UpstreamHTTPError) HTTPStatusCode() int  { return err.StatusCode }

func (*claudeDriver) ImportCredential(ctx context.Context, raw []byte) (subscriptionruntime.Credential, error) {
	options, err := claudeOptions(ctx)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	imported, err := cpaembedded.ImportClaudeCredential(ctx, raw, options)
	if err != nil {
		return subscriptionruntime.Credential{}, normalizeAuthorizationError(err)
	}
	value := credentialFromBridge(imported)
	canonical, err := MarshalCredential(value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return claudeRuntimeCredential(value, canonical), nil
}

var _ subscriptionruntime.CredentialFileImporter = (*claudeDriver)(nil)
