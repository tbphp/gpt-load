package chatgpt

import (
	"context"
	"errors"
	"strings"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/subscription/providers/codex"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

type chatgptDriver struct{}

func newChatGPTDriver() *chatgptDriver { return &chatgptDriver{} }

func Implementations() subscriptionruntime.Implementations {
	return subscriptionruntime.Implementations{
		Drivers: []subscriptionruntime.Driver{newChatGPTDriver()},
	}
}

func (*chatgptDriver) ID() spec.SubscriptionDriverID { return modules.ChatGPTSubscriptionDriver }

func (*chatgptDriver) Parse(raw []byte) (subscriptionruntime.Credential, error) {
	value, err := codex.ParseCredentialJSON(raw)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return runtimeCredential(value)
}

func (*chatgptDriver) Refresh(ctx context.Context, current subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
	value, err := codex.ParseCredentialJSON(current.Canonical())
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	refreshed, err := codex.RefreshCredentialOnce(ctx, value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return runtimeCredential(refreshed)
}

func (*chatgptDriver) ClassifyRefreshFailure(err error) subscriptionruntime.RefreshFailureDecision {
	var tokenErr *codex.TokenEndpointError
	if errors.Is(err, codex.ErrCredentialIdentityChanged) {
		return subscriptionruntime.RefreshFailureDecision{Kind: subscriptionruntime.RefreshFailureIdentityChanged}
	}
	if errors.As(err, &tokenErr) {
		decision := subscriptionruntime.RefreshFailureDecision{
			Kind: subscriptionruntime.RefreshFailureOutcomeUnknown, StatusCode: tokenErr.StatusCode,
			OAuthCode: strings.TrimSpace(tokenErr.Code), RetryAfter: tokenErr.RetryAfter,
		}
		if subscriptionruntime.TokenEndpointFailureRetryable(tokenErr.StatusCode, tokenErr.Code) {
			decision.Kind = subscriptionruntime.RefreshFailureRetryable
		} else if codex.IsDefinitiveRefreshRejection(tokenErr.Code) {
			decision.Kind = subscriptionruntime.RefreshFailureReauthorizationRequired
		}
		return decision
	}
	return subscriptionruntime.RefreshFailureDecision{Kind: subscriptionruntime.RefreshFailureOutcomeUnknown}
}

func (*chatgptDriver) MatchesRefreshIdentity(current, refreshed subscriptionruntime.Credential) bool {
	before, err := codex.ParseCredentialJSON(current.Canonical())
	if err != nil {
		return false
	}
	after, err := codex.ParseCredentialJSON(refreshed.Canonical())
	if err != nil || before.AccountID != after.AccountID {
		return false
	}
	beforeUser := strings.TrimPrefix(strings.TrimPrefix(codex.Identity(before), before.AccountID), "/")
	afterUser := strings.TrimPrefix(strings.TrimPrefix(codex.Identity(after), after.AccountID), "/")
	return beforeUser == "" || beforeUser == afterUser
}

func (*chatgptDriver) ImportCredential(ctx context.Context, raw []byte) (subscriptionruntime.Credential, error) {
	options, err := chatgptOptions(ctx)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	imported, err := cpaembedded.ImportCodexCredential(ctx, raw, options)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return runtimeCredential(codex.Credential{
		Type:         imported.Type,
		IDToken:      imported.IDToken,
		AccessToken:  imported.AccessToken,
		RefreshToken: imported.RefreshToken,
		AccountID:    imported.AccountID,
		Email:        imported.Email,
		Expire:       imported.Expire,
		LastRefresh:  imported.LastRefresh,
	})
}

func runtimeCredential(value codex.Credential) (subscriptionruntime.Credential, error) {
	canonical, err := codex.MarshalCredential(value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	expiresAt, expires := codex.CredentialExpiresAt(value)
	account := subscriptionruntime.Account{Email: strings.TrimSpace(value.Email), ExpiresAt: expiresAt, ExpiresAtKnown: expires}
	if refreshed, err := time.Parse(time.RFC3339, strings.TrimSpace(value.LastRefresh)); err == nil {
		account.LastRefresh, account.LastRefreshKnown = refreshed, true
	}
	return subscriptionruntime.NewCredential(canonical, codex.Identity(value), account, expiresAt, expires, value.SecretValues()), nil
}

func chatgptOptions(ctx context.Context) (cpaembedded.Options, error) {
	client, err := subscriptionruntime.HTTPClient(ctx)
	if err != nil {
		return cpaembedded.Options{}, err
	}
	return cpaembedded.Options{HTTPClient: client}, nil
}

var _ subscriptionruntime.Driver = (*chatgptDriver)(nil)
var _ subscriptionruntime.RefreshIdentityMatcher = (*chatgptDriver)(nil)
var _ subscriptionruntime.CredentialFileImporter = (*chatgptDriver)(nil)
