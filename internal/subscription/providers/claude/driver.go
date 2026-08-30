package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

type claudeDriver struct{}

func newClaudeDriver() *claudeDriver { return &claudeDriver{} }

// Implementations returns the concrete Claude subscription behavior
// assembled by the application composition root.
func Implementations() subscriptionruntime.Implementations {
	driver := newClaudeDriver()
	return subscriptionruntime.Implementations{
		Drivers:           []subscriptionruntime.Driver{driver},
		ModelDiscoveries:  []subscriptionruntime.ModelDiscovery{driver.modelDiscovery()},
		QuotaObservations: []subscriptionruntime.QuotaObservation{driver.quotaObservation()},
	}
}

func (*claudeDriver) ID() spec.SubscriptionDriverID { return modules.ClaudeSubscriptionDriver }

func (*claudeDriver) Parse(raw []byte) (subscriptionruntime.Credential, error) {
	value, err := ParseCredentialJSON(raw)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	canonical, err := MarshalCredential(value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return claudeRuntimeCredential(value, canonical), nil
}

func (*claudeDriver) Refresh(ctx context.Context, current subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
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
	return claudeRuntimeCredential(refreshed, canonical), nil
}

func (*claudeDriver) ClassifyRefreshFailure(err error) subscriptionruntime.RefreshFailureDecision {
	var tokenErr *TokenEndpointError
	if errors.Is(err, ErrCredentialIdentityChanged) ||
		errors.Is(err, ErrOrganizationIdentityChanged) {
		return subscriptionruntime.RefreshFailureDecision{Kind: subscriptionruntime.RefreshFailureIdentityChanged}
	}
	if errors.As(err, &tokenErr) {
		decision := subscriptionruntime.RefreshFailureDecision{
			Kind: subscriptionruntime.RefreshFailureRetryable, StatusCode: tokenErr.StatusCode,
			OAuthCode: strings.TrimSpace(tokenErr.Code), RetryAfter: tokenErr.RetryAfter,
		}
		if IsDefinitiveRefreshRejection(tokenErr.Code) {
			decision.Kind = subscriptionruntime.RefreshFailureReauthorizationRequired
		}
		return decision
	}
	return subscriptionruntime.RefreshFailureDecision{Kind: subscriptionruntime.RefreshFailureOutcomeUnknown}
}

type claudeAuthorizationState struct {
	Verifier string `json:"verifier"`
}

func (*claudeDriver) BeginAuthorization() (subscriptionruntime.Authorization, error) {
	value, err := BeginBrowserAuthorization()
	if err != nil {
		return subscriptionruntime.Authorization{}, err
	}
	driverState, err := json.Marshal(claudeAuthorizationState{Verifier: value.CodeVerifier})
	if err != nil {
		return subscriptionruntime.Authorization{}, err
	}
	return subscriptionruntime.Authorization{
		URL: value.AuthorizationURL, State: value.State, DriverState: driverState,
		ExpiresAt: value.ExpiresAt,
	}, nil
}

func (*claudeDriver) LocalCallback() (subscriptionruntime.LocalCallbackSpec, bool) {
	return subscriptionruntime.LocalCallbackSpec{RedirectURI: RedirectURI}, true
}

func (*claudeDriver) CompleteAuthorization(ctx context.Context, completion subscriptionruntime.AuthorizationCompletion) (subscriptionruntime.Credential, error) {
	var state claudeAuthorizationState
	if json.Unmarshal(completion.DriverState, &state) != nil || strings.TrimSpace(state.Verifier) == "" {
		return subscriptionruntime.Credential{}, ErrInvalidState
	}
	value, err := CompleteBrowserAuthorization(ctx, BrowserAuthorizationCompletion{
		ExpectedState: completion.ExpectedState, ReturnedState: completion.ReturnedState,
		Code: completion.Code, CodeVerifier: state.Verifier,
	})
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	canonical, err := MarshalCredential(value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return claudeRuntimeCredential(value, canonical), nil
}

func (*claudeDriver) AuthorizationFailureDefinitive(err error) bool {
	var tokenErr *TokenEndpointError
	if !errors.As(err, &tokenErr) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(tokenErr.Code)) {
	case "invalid_grant", "access_denied":
		return true
	default:
		return false
	}
}

func (*claudeDriver) DiscoverModels(ctx context.Context, credential subscriptionruntime.Credential) ([]string, error) {
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
	return result, nil
}

func (*claudeDriver) Observe(ctx context.Context, credential subscriptionruntime.Credential) (subscriptionruntime.Observation, error) {
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
	normalized, err := NormalizeObservation(observed)
	if err != nil {
		return subscriptionruntime.Observation{}, fmt.Errorf("%w: %v", subscriptionruntime.ErrObservationPayloadInvalid, err)
	}
	return subscriptionruntime.Observation{
		Payload: normalized, Header: observed.Header.Clone(),
		Partial:         len(observed.IncompleteSources) > 0,
		AccountObserved: observed.AccountObserved,
		QuotaObserved:   observed.UsageObserved,
	}, nil
}

// Go cannot overload ID across the narrow capability interfaces, so wrappers
// expose each typed ID while sharing the implementation below.
type claudeModelDiscovery struct{ *claudeDriver }
type claudeQuotaObservation struct{ *claudeDriver }

func (driver *claudeDriver) modelDiscovery() subscriptionruntime.ModelDiscovery {
	return claudeModelDiscovery{driver}
}

func (driver *claudeDriver) quotaObservation() subscriptionruntime.QuotaObservation {
	return claudeQuotaObservation{driver}
}

func (claudeModelDiscovery) ID() spec.UtilityID   { return modules.ClaudeModelDiscovery }
func (claudeQuotaObservation) ID() spec.UtilityID { return modules.ClaudeQuotaObservation }

func claudeRuntimeCredential(value Credential, canonical []byte) subscriptionruntime.Credential {
	expiresAt, expires := CredentialExpiresAt(value)
	account := subscriptionruntime.Account{Email: strings.TrimSpace(value.Email), ExpiresAt: expiresAt, ExpiresAtKnown: expires}
	if refreshed, err := time.Parse(time.RFC3339, strings.TrimSpace(value.LastRefresh)); err == nil {
		account.LastRefresh, account.LastRefreshKnown = refreshed, true
	}
	return subscriptionruntime.NewCredential(
		canonical,
		strings.TrimSpace(value.AccountUUID),
		account,
		expiresAt,
		expires,
		value.SecretValues(),
	)
}

var _ subscriptionruntime.BrowserAuthorizationDriver = (*claudeDriver)(nil)
