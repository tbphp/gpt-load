package codex

import (
	"context"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"

	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func (err *TokenEndpointError) HTTPStatusCode() int { return err.StatusCode }
func (err *UpstreamHTTPError) HTTPStatusCode() int  { return err.StatusCode }

func (*codexDriver) ImportCredential(ctx context.Context, raw []byte) (subscriptionruntime.Credential, error) {
	options, err := codexOptions(ctx)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	imported, err := cpaembedded.ImportCodexCredential(ctx, raw, options)
	if err != nil {
		return subscriptionruntime.Credential{}, normalizeAuthorizationError(err)
	}
	value := credentialFromBridge(imported)
	canonical, err := MarshalCredential(value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return codexRuntimeCredential(value, canonical), nil
}

var _ subscriptionruntime.CredentialFileImporter = (*codexDriver)(nil)
