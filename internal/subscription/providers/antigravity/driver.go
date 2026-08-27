package antigravity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

type antigravityDriver struct{}

func newAntigravityDriver() *antigravityDriver { return &antigravityDriver{} }

// Implementations returns the concrete Antigravity subscription
// behavior assembled by the application composition root.
func Implementations() subscriptionruntime.Implementations {
	driver := newAntigravityDriver()
	return subscriptionruntime.Implementations{
		Drivers:           []subscriptionruntime.Driver{driver},
		ModelDiscoveries:  []subscriptionruntime.ModelDiscovery{driver.modelDiscovery()},
		QuotaObservations: []subscriptionruntime.QuotaObservation{driver.quotaObservation()},
	}
}

func (*antigravityDriver) ID() spec.SubscriptionDriverID {
	return modules.AntigravitySubscriptionDriver
}

func (*antigravityDriver) Parse(raw []byte) (subscriptionruntime.Credential, error) {
	value, err := ParseCredentialJSON(raw)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	canonical, err := MarshalCredential(value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return antigravityRuntimeCredential(value, canonical), nil
}

func (*antigravityDriver) Refresh(ctx context.Context, current subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
	value, err := ParseCredentialJSON(current.Canonical())
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	refreshed, err := RefreshCredentialOnce(ctx, value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	canonical, err := MarshalCredential(refreshed)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return antigravityRuntimeCredential(refreshed, canonical), nil
}

func (*antigravityDriver) ClassifyRefreshFailure(err error) subscriptionruntime.RefreshFailure {
	if errors.Is(err, ErrCredentialIdentityChanged) {
		return subscriptionruntime.RefreshFailureIdentityChanged
	}
	var tokenErr *TokenEndpointError
	if errors.As(err, &tokenErr) && IsDefinitiveRefreshRejection(tokenErr.Code) {
		return subscriptionruntime.RefreshFailureReauthorizationRequired
	}
	return subscriptionruntime.RefreshFailureOutcomeUnknown
}

func (*antigravityDriver) BeginAuthorization() (subscriptionruntime.Authorization, error) {
	value, err := BeginBrowserAuthorization()
	if err != nil {
		return subscriptionruntime.Authorization{}, err
	}
	return subscriptionruntime.Authorization{URL: value.AuthorizationURL, State: value.State, ExpiresAt: value.ExpiresAt}, nil
}

func (*antigravityDriver) LocalCallback() (subscriptionruntime.LocalCallbackSpec, bool) {
	return subscriptionruntime.LocalCallbackSpec{RedirectURI: RedirectURI}, true
}

func (*antigravityDriver) CompleteAuthorization(ctx context.Context, completion subscriptionruntime.AuthorizationCompletion) (subscriptionruntime.Credential, error) {
	value, err := CompleteBrowserAuthorization(ctx, BrowserAuthorizationCompletion{
		ExpectedState: completion.ExpectedState, ReturnedState: completion.ReturnedState, Code: completion.Code,
	})
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	canonical, err := MarshalCredential(value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return antigravityRuntimeCredential(value, canonical), nil
}

func (*antigravityDriver) AuthorizationFailureDefinitive(err error) bool {
	var tokenErr *TokenEndpointError
	return errors.As(err, &tokenErr) && IsDefinitiveRefreshRejection(tokenErr.Code)
}

func (*antigravityDriver) ImportCredential(ctx context.Context, raw []byte) (subscriptionruntime.Credential, error) {
	value, err := ImportCredential(ctx, raw)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	canonical, err := MarshalCredential(value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return antigravityRuntimeCredential(value, canonical), nil
}

func (*antigravityDriver) DiscoverModels(ctx context.Context, credential subscriptionruntime.Credential) ([]string, error) {
	value, err := ParseCredentialJSON(credential.Canonical())
	if err != nil {
		return nil, err
	}
	models, err := ListModels(ctx, value)
	if err != nil {
		var upstream *UpstreamHTTPError
		if errors.As(err, &upstream) {
			return nil, &subscriptionruntime.UpstreamHTTPError{StatusCode: upstream.StatusCode}
		}
		return nil, err
	}
	result := make([]string, 0, len(models))
	for _, model := range models {
		result = append(result, model.ID)
	}
	return cpaembedded.MergeModelCatalog(cpaembedded.ProviderAntigravity, result), nil
}

func (*antigravityDriver) Observe(ctx context.Context, credential subscriptionruntime.Credential) (subscriptionruntime.Observation, error) {
	value, err := ParseCredentialJSON(credential.Canonical())
	if err != nil {
		return subscriptionruntime.Observation{}, err
	}
	observed, err := ObserveAccount(ctx, value)
	if err != nil {
		var upstream *UpstreamHTTPError
		if errors.As(err, &upstream) {
			return subscriptionruntime.Observation{}, &subscriptionruntime.UpstreamHTTPError{StatusCode: upstream.StatusCode}
		}
		return subscriptionruntime.Observation{}, err
	}
	accountObserved, quotaObserved, partial, err := antigravityObservationCompleteness(observed)
	if err != nil {
		return subscriptionruntime.Observation{}, err
	}
	normalized, err := NormalizeObservation(value.Email, observed)
	if err != nil {
		return subscriptionruntime.Observation{}, fmt.Errorf("%w: %v", subscriptionruntime.ErrObservationPayloadInvalid, err)
	}
	creditsObserved := observed.CreditsObserved || observed.GoogleOneAICredits != nil
	observedQuotaScopes := make([]string, 0, 2)
	if creditsObserved {
		observedQuotaScopes = append(observedQuotaScopes, quotaScopeCredits)
	}
	if quotaObserved {
		observedQuotaScopes = append(observedQuotaScopes, quotaScopeModel)
	}
	return subscriptionruntime.Observation{
		Payload: normalized, Partial: partial,
		AccountObserved: accountObserved, QuotaObserved: quotaObserved,
		ObservedQuotaScopes: observedQuotaScopes,
	}, nil
}

func antigravityObservationCompleteness(observed AccountObservation) (bool, bool, bool, error) {
	accountObserved := observed.AccountObserved || strings.TrimSpace(observed.PlanID) != ""
	quotaObserved := observed.QuotaObserved || len(observed.QuotaGroups) > 0
	if !accountObserved && !quotaObserved {
		return false, false, false, fmt.Errorf("%w: Antigravity account observation has no usable fields", subscriptionruntime.ErrObservationPayloadInvalid)
	}
	return accountObserved, quotaObserved,
		len(observed.IncompleteSources) > 0 || !accountObserved || !quotaObserved,
		nil
}

type antigravityModelDiscovery struct{ *antigravityDriver }
type antigravityQuotaObservation struct{ *antigravityDriver }

func (driver *antigravityDriver) modelDiscovery() subscriptionruntime.ModelDiscovery {
	return antigravityModelDiscovery{driver}
}

func (driver *antigravityDriver) quotaObservation() subscriptionruntime.QuotaObservation {
	return antigravityQuotaObservation{driver}
}

func (antigravityModelDiscovery) ID() spec.UtilityID   { return modules.AntigravityModelDiscovery }
func (antigravityQuotaObservation) ID() spec.UtilityID { return modules.AntigravityQuotaObservation }

func antigravityRuntimeCredential(value Credential, canonical []byte) subscriptionruntime.Credential {
	expiresAt, expires := CredentialExpiresAt(value)
	account := subscriptionruntime.Account{Email: strings.TrimSpace(value.Email), ExpiresAt: expiresAt, ExpiresAtKnown: expires}
	if refreshed, err := time.Parse(time.RFC3339, strings.TrimSpace(value.LastRefresh)); err == nil {
		account.LastRefresh, account.LastRefreshKnown = refreshed, true
	}
	return subscriptionruntime.NewCredential(canonical, strings.TrimSpace(value.AccountID), account, expiresAt, expires, value.SecretValues())
}

var _ subscriptionruntime.BrowserAuthorizationDriver = (*antigravityDriver)(nil)
var _ subscriptionruntime.CredentialFileImporter = (*antigravityDriver)(nil)
