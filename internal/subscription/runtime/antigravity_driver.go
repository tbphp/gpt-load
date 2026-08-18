package subscriptionruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gpt-load/internal/antigravity"
	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
)

type antigravityDriver struct{}

func newAntigravityDriver() *antigravityDriver { return &antigravityDriver{} }

// AntigravityImplementations returns the concrete Antigravity subscription
// behavior assembled by the application composition root.
func AntigravityImplementations() Implementations {
	driver := newAntigravityDriver()
	return Implementations{
		Drivers:           []Driver{driver},
		ModelDiscoveries:  []ModelDiscovery{driver.modelDiscovery()},
		QuotaObservations: []QuotaObservation{driver.quotaObservation()},
	}
}

func (*antigravityDriver) ID() spec.SubscriptionDriverID {
	return modules.AntigravitySubscriptionDriver
}

func (*antigravityDriver) Parse(raw []byte) (Credential, error) {
	value, err := antigravity.ParseCredentialJSON(raw)
	if err != nil {
		return Credential{}, err
	}
	canonical, err := antigravity.MarshalCredential(value)
	if err != nil {
		return Credential{}, err
	}
	return antigravityRuntimeCredential(value, canonical), nil
}

func (*antigravityDriver) Refresh(ctx context.Context, current Credential) (Credential, error) {
	value, err := antigravity.ParseCredentialJSON(current.canonical)
	if err != nil {
		return Credential{}, err
	}
	refreshed, err := antigravity.RefreshCredentialOnce(ctx, value)
	if err != nil {
		return Credential{}, err
	}
	canonical, err := antigravity.MarshalCredential(refreshed)
	if err != nil {
		return Credential{}, err
	}
	return antigravityRuntimeCredential(refreshed, canonical), nil
}

func (*antigravityDriver) ClassifyRefreshFailure(err error) RefreshFailure {
	if errors.Is(err, antigravity.ErrCredentialIdentityChanged) {
		return RefreshFailureIdentityChanged
	}
	var tokenErr *antigravity.TokenEndpointError
	if errors.As(err, &tokenErr) && antigravity.IsDefinitiveRefreshRejection(tokenErr.Code) {
		return RefreshFailureReauthorizationRequired
	}
	return RefreshFailureOutcomeUnknown
}

func (*antigravityDriver) BeginAuthorization() (Authorization, error) {
	value, err := antigravity.BeginBrowserAuthorization()
	if err != nil {
		return Authorization{}, err
	}
	return Authorization{URL: value.AuthorizationURL, State: value.State, ExpiresAt: value.ExpiresAt}, nil
}

func (*antigravityDriver) LocalCallback() (LocalCallbackSpec, bool) {
	return LocalCallbackSpec{RedirectURI: antigravity.RedirectURI}, true
}

func (*antigravityDriver) CompleteAuthorization(ctx context.Context, completion AuthorizationCompletion) (Credential, error) {
	value, err := antigravity.CompleteBrowserAuthorization(ctx, antigravity.BrowserAuthorizationCompletion{
		ExpectedState: completion.ExpectedState, ReturnedState: completion.ReturnedState, Code: completion.Code,
	})
	if err != nil {
		return Credential{}, err
	}
	canonical, err := antigravity.MarshalCredential(value)
	if err != nil {
		return Credential{}, err
	}
	return antigravityRuntimeCredential(value, canonical), nil
}

func (*antigravityDriver) AuthorizationFailureDefinitive(err error) bool {
	var tokenErr *antigravity.TokenEndpointError
	return errors.As(err, &tokenErr) && antigravity.IsDefinitiveRefreshRejection(tokenErr.Code)
}

func (*antigravityDriver) ImportCredential(ctx context.Context, raw []byte) (Credential, error) {
	value, err := antigravity.ImportCredential(ctx, raw)
	if err != nil {
		return Credential{}, err
	}
	canonical, err := antigravity.MarshalCredential(value)
	if err != nil {
		return Credential{}, err
	}
	return antigravityRuntimeCredential(value, canonical), nil
}

func (*antigravityDriver) DiscoverModels(ctx context.Context, credential Credential) ([]string, error) {
	value, err := antigravity.ParseCredentialJSON(credential.canonical)
	if err != nil {
		return nil, err
	}
	models, err := antigravity.ListModels(ctx, value)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(models))
	for _, model := range models {
		result = append(result, model.ID)
	}
	return result, nil
}

func (*antigravityDriver) Observe(ctx context.Context, credential Credential) (Observation, error) {
	value, err := antigravity.ParseCredentialJSON(credential.canonical)
	if err != nil {
		return Observation{}, err
	}
	observed, err := antigravity.ObserveAccount(ctx, value)
	if err != nil {
		var upstream *antigravity.UpstreamHTTPError
		if errors.As(err, &upstream) {
			return Observation{}, &UpstreamHTTPError{StatusCode: upstream.StatusCode}
		}
		return Observation{}, err
	}
	accountObserved, quotaObserved, partial, err := antigravityObservationCompleteness(observed)
	if err != nil {
		return Observation{}, err
	}
	normalized, err := NormalizeAntigravityObservation(value.Email, observed)
	if err != nil {
		return Observation{}, fmt.Errorf("%w: %v", ErrObservationPayloadInvalid, err)
	}
	return Observation{
		Payload: normalized, Partial: partial,
		AccountObserved: accountObserved, QuotaObserved: quotaObserved,
	}, nil
}

func antigravityObservationCompleteness(observed antigravity.AccountObservation) (bool, bool, bool, error) {
	accountObserved := strings.TrimSpace(observed.PlanID) != ""
	// A successfully observed plan with no Google One AI credit entry is an
	// authoritative empty quota result, not a partial observation.
	quotaObserved := accountObserved || observed.GoogleOneAICredits != nil
	if !accountObserved && !quotaObserved {
		return false, false, false, fmt.Errorf("%w: Antigravity account observation has no usable fields", ErrObservationPayloadInvalid)
	}
	return accountObserved, quotaObserved, !accountObserved || !quotaObserved, nil
}

type antigravityModelDiscovery struct{ *antigravityDriver }
type antigravityQuotaObservation struct{ *antigravityDriver }

func (driver *antigravityDriver) modelDiscovery() ModelDiscovery {
	return antigravityModelDiscovery{driver}
}

func (driver *antigravityDriver) quotaObservation() QuotaObservation {
	return antigravityQuotaObservation{driver}
}

func (antigravityModelDiscovery) ID() spec.UtilityID   { return modules.AntigravityModelDiscovery }
func (antigravityQuotaObservation) ID() spec.UtilityID { return modules.AntigravityQuotaObservation }

func antigravityRuntimeCredential(value antigravity.Credential, canonical []byte) Credential {
	expiresAt, expires := antigravity.CredentialExpiresAt(value)
	account := Account{Email: strings.TrimSpace(value.Email), ExpiresAt: expiresAt, ExpiresAtKnown: expires}
	if refreshed, err := time.Parse(time.RFC3339, strings.TrimSpace(value.LastRefresh)); err == nil {
		account.LastRefresh, account.LastRefreshKnown = refreshed, true
	}
	return newCredential(canonical, strings.TrimSpace(value.AccountID), account, expiresAt, expires, value.SecretValues())
}

var _ BrowserAuthorizationDriver = (*antigravityDriver)(nil)
var _ CredentialFileImporter = (*antigravityDriver)(nil)
