package subscriptionruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/claude"
)

type claudeDriver struct{}

func newClaudeDriver() *claudeDriver { return &claudeDriver{} }

// ClaudeImplementations returns the concrete Claude subscription behavior
// assembled by the application composition root.
func ClaudeImplementations() Implementations {
	driver := newClaudeDriver()
	return Implementations{
		Drivers:           []Driver{driver},
		ModelDiscoveries:  []ModelDiscovery{driver.modelDiscovery()},
		QuotaObservations: []QuotaObservation{driver.quotaObservation()},
	}
}

func (*claudeDriver) ID() spec.SubscriptionDriverID { return modules.ClaudeSubscriptionDriver }

func (*claudeDriver) Parse(raw []byte) (Credential, error) {
	value, err := claude.ParseCredentialJSON(raw)
	if err != nil {
		return Credential{}, err
	}
	canonical, err := claude.MarshalCredential(value)
	if err != nil {
		return Credential{}, err
	}
	return claudeRuntimeCredential(value, canonical), nil
}

func (*claudeDriver) Refresh(ctx context.Context, current Credential) (Credential, error) {
	value, err := claude.ParseCredentialJSON(current.canonical)
	if err != nil {
		return Credential{}, err
	}
	refreshed, err := claude.RefreshCredentialOnce(ctx, value)
	if err != nil {
		return Credential{}, err
	}
	canonical, err := claude.MarshalCredential(refreshed)
	if err != nil {
		return Credential{}, err
	}
	return claudeRuntimeCredential(refreshed, canonical), nil
}

func (*claudeDriver) ClassifyRefreshFailure(err error) RefreshFailure {
	var tokenErr *claude.TokenEndpointError
	if errors.Is(err, claude.ErrCredentialIdentityChanged) ||
		errors.Is(err, claude.ErrOrganizationIdentityChanged) {
		return RefreshFailureIdentityChanged
	}
	if errors.As(err, &tokenErr) && claude.IsDefinitiveRefreshRejection(tokenErr.Code) {
		return RefreshFailureReauthorizationRequired
	}
	return RefreshFailureOutcomeUnknown
}

type claudeAuthorizationState struct {
	Verifier string `json:"verifier"`
}

func (*claudeDriver) BeginAuthorization() (Authorization, error) {
	value, err := claude.BeginBrowserAuthorization()
	if err != nil {
		return Authorization{}, err
	}
	driverState, err := json.Marshal(claudeAuthorizationState{Verifier: value.CodeVerifier})
	if err != nil {
		return Authorization{}, err
	}
	return Authorization{
		URL: value.AuthorizationURL, State: value.State, DriverState: driverState,
		ExpiresAt: value.ExpiresAt,
	}, nil
}

func (*claudeDriver) LocalCallback() (LocalCallbackSpec, bool) {
	return LocalCallbackSpec{RedirectURI: claude.RedirectURI}, true
}

func (*claudeDriver) CompleteAuthorization(ctx context.Context, completion AuthorizationCompletion) (Credential, error) {
	var state claudeAuthorizationState
	if json.Unmarshal(completion.DriverState, &state) != nil || strings.TrimSpace(state.Verifier) == "" {
		return Credential{}, claude.ErrInvalidState
	}
	value, err := claude.CompleteBrowserAuthorization(ctx, claude.BrowserAuthorizationCompletion{
		ExpectedState: completion.ExpectedState, ReturnedState: completion.ReturnedState,
		Code: completion.Code, CodeVerifier: state.Verifier,
	})
	if err != nil {
		return Credential{}, err
	}
	canonical, err := claude.MarshalCredential(value)
	if err != nil {
		return Credential{}, err
	}
	return claudeRuntimeCredential(value, canonical), nil
}

func (*claudeDriver) AuthorizationFailureDefinitive(err error) bool {
	var tokenErr *claude.TokenEndpointError
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

func (*claudeDriver) DiscoverModels(ctx context.Context, credential Credential) ([]string, error) {
	value, err := claude.ParseCredentialJSON(credential.canonical)
	if err != nil {
		return nil, err
	}
	models, err := claude.ListModels(ctx, value)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(models))
	for _, model := range models {
		result = append(result, model.ID)
	}
	return result, nil
}

func (*claudeDriver) Observe(ctx context.Context, credential Credential) (Observation, error) {
	value, err := claude.ParseCredentialJSON(credential.canonical)
	if err != nil {
		return Observation{}, err
	}
	observed, err := claude.ObserveAccount(ctx, value)
	if err != nil {
		var upstream *claude.UpstreamHTTPError
		if errors.As(err, &upstream) {
			return Observation{}, &UpstreamHTTPError{StatusCode: upstream.StatusCode}
		}
		return Observation{}, err
	}
	normalized, err := NormalizeClaudeObservation(observed)
	if err != nil {
		return Observation{}, fmt.Errorf("%w: %v", ErrObservationPayloadInvalid, err)
	}
	return Observation{
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

func (driver *claudeDriver) modelDiscovery() ModelDiscovery {
	return claudeModelDiscovery{driver}
}

func (driver *claudeDriver) quotaObservation() QuotaObservation {
	return claudeQuotaObservation{driver}
}

func (claudeModelDiscovery) ID() spec.UtilityID   { return modules.ClaudeModelDiscovery }
func (claudeQuotaObservation) ID() spec.UtilityID { return modules.ClaudeQuotaObservation }

func claudeRuntimeCredential(value claude.Credential, canonical []byte) Credential {
	expiresAt, expires := claude.CredentialExpiresAt(value)
	account := Account{Email: strings.TrimSpace(value.Email), ExpiresAt: expiresAt, ExpiresAtKnown: expires}
	if refreshed, err := time.Parse(time.RFC3339, strings.TrimSpace(value.LastRefresh)); err == nil {
		account.LastRefresh, account.LastRefreshKnown = refreshed, true
	}
	return newCredential(
		canonical,
		strings.TrimSpace(value.AccountUUID),
		account,
		expiresAt,
		expires,
		value.SecretValues(),
	)
}

var _ BrowserAuthorizationDriver = (*claudeDriver)(nil)
