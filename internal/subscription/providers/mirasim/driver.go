package mirasim

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
	providerobservation "gpt-load/internal/subscription/providers/observation"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

type driver struct{}

func Implementations() subscriptionruntime.Implementations {
	value := &driver{}
	return subscriptionruntime.Implementations{
		Drivers:           []subscriptionruntime.Driver{value},
		ModelDiscoveries:  []subscriptionruntime.ModelDiscovery{modelDiscovery{value}},
		QuotaObservations: []subscriptionruntime.QuotaObservation{quotaObservation{value}},
	}
}

func (*driver) ID() spec.SubscriptionDriverID { return modules.MirasimSubscriptionDriver }

func (*driver) Parse(raw []byte) (subscriptionruntime.Credential, error) {
	value, err := ParseCredentialJSON(raw)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return runtimeCredential(value)
}

func (*driver) Refresh(ctx context.Context, current subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
	value, err := ParseCredentialJSON(current.Canonical())
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	refreshed, err := NewClient(value).Refresh(ctx)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return runtimeCredential(refreshed)
}

func (*driver) ClassifyRefreshFailure(err error) subscriptionruntime.RefreshFailureDecision {
	if errors.Is(err, errIdentityChanged) {
		return subscriptionruntime.RefreshFailureDecision{Kind: subscriptionruntime.RefreshFailureIdentityChanged}
	}
	var refreshErr *RefreshError
	if errors.As(err, &refreshErr) {
		decision := subscriptionruntime.RefreshFailureDecision{
			Kind: subscriptionruntime.RefreshFailureOutcomeUnknown, StatusCode: refreshErr.StatusCode(),
			OAuthCode: refreshErr.code,
		}
		if retry := refreshErr.RetryAfter(); retry != nil {
			decision.RetryAfter = *retry
		}
		switch {
		case refreshErr.Retryable():
			decision.Kind = subscriptionruntime.RefreshFailureRetryable
		case refreshErr.StatusCode() == http.StatusUnauthorized || strings.EqualFold(refreshErr.code, "invalid_grant"):
			decision.Kind = subscriptionruntime.RefreshFailureReauthorizationRequired
		}
		return decision
	}
	return subscriptionruntime.RefreshFailureDecision{Kind: subscriptionruntime.RefreshFailureOutcomeUnknown}
}

func (*driver) MatchesRefreshIdentity(current, refreshed subscriptionruntime.Credential) bool {
	left, errLeft := ParseCredentialJSON(current.Canonical())
	right, errRight := ParseCredentialJSON(refreshed.Canonical())
	if errLeft != nil || errRight != nil {
		return false
	}
	return matchesRefreshIdentity(left, right)
}

func (*driver) BeginAuthorization() (subscriptionruntime.Authorization, error) {
	login, err := BeginBrowserLogin(context.Background(), defaultAdminURL)
	if err != nil {
		return subscriptionruntime.Authorization{}, err
	}
	return subscriptionruntime.Authorization{
		URL: login.URL, State: login.State, DriverState: login.DriverState, ExpiresAt: login.ExpiresAt,
	}, nil
}

func (*driver) LocalCallback() (subscriptionruntime.LocalCallbackSpec, bool) {
	return subscriptionruntime.LocalCallbackSpec{RedirectURI: CallbackRedirectURI}, true
}

func (*driver) CompleteAuthorization(_ context.Context, completion subscriptionruntime.AuthorizationCompletion) (subscriptionruntime.Credential, error) {
	if completion.ExpectedState == "" || completion.ExpectedState != completion.ReturnedState {
		return subscriptionruntime.Credential{}, errInvalidOAuthState
	}
	if strings.TrimSpace(completion.AccessToken) == "" || strings.TrimSpace(completion.RefreshToken) == "" {
		return subscriptionruntime.Credential{}, errMissingOAuthToken
	}
	var state oauthDriverState
	if json.Unmarshal(completion.DriverState, &state) != nil || strings.TrimSpace(state.DevicePrivateKey) == "" {
		return subscriptionruntime.Credential{}, errInvalidOAuthState
	}
	value, err := CompleteBrowserLogin(state, completion.AccessToken, completion.RefreshToken, time.Now())
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return runtimeCredential(value)
}

func (*driver) AuthorizationFailureDefinitive(err error) bool {
	return errors.Is(err, errInvalidOAuthState) || errors.Is(err, errMissingOAuthToken)
}

var (
	errInvalidOAuthState = errors.New("mirasim oauth state mismatch")
	errMissingOAuthToken = errors.New("mirasim oauth callback is missing tokens")
)

type modelDiscovery struct{ *driver }

func (modelDiscovery) ID() spec.UtilityID { return modules.MirasimModelDiscovery }

func (*driver) DiscoverModels(ctx context.Context, credential subscriptionruntime.Credential, target subscriptionruntime.Target) ([]string, error) {
	value, err := credentialForTarget(credential, target)
	if err != nil {
		return nil, err
	}
	models, err := NewClient(value).ListModels(ctx)
	if err != nil {
		var upstream *StatusError
		if errors.As(err, &upstream) {
			return nil, &subscriptionruntime.UpstreamHTTPError{StatusCode: upstream.StatusCode()}
		}
		return nil, err
	}
	return models, nil
}

type quotaObservation struct{ *driver }

func (quotaObservation) ID() spec.UtilityID { return modules.MirasimQuotaObservation }

func (*driver) Observe(ctx context.Context, credential subscriptionruntime.Credential, target subscriptionruntime.Target) (subscriptionruntime.Observation, error) {
	value, err := credentialForTarget(credential, target)
	if err != nil {
		return subscriptionruntime.Observation{}, err
	}
	limits, err := NewClient(value).FetchLimits(ctx)
	if err != nil {
		var upstream *StatusError
		if errors.As(err, &upstream) {
			return subscriptionruntime.Observation{}, &subscriptionruntime.UpstreamHTTPError{StatusCode: upstream.StatusCode()}
		}
		return subscriptionruntime.Observation{}, err
	}
	if len(limits.Windows) == 0 {
		return subscriptionruntime.Observation{}, subscriptionruntime.ErrObservationPayloadInvalid
	}
	payload, err := NormalizeObservation(value.Email, value.Plan, limits)
	if err != nil {
		return subscriptionruntime.Observation{}, err
	}
	scopes := []string{}
	var decoded providerobservation.Snapshot
	if json.Unmarshal(payload, &decoded) == nil {
		seen := map[string]struct{}{}
		for _, window := range decoded.QuotaWindows {
			if _, ok := seen[window.Scope]; ok || window.Scope == "" {
				continue
			}
			seen[window.Scope] = struct{}{}
			scopes = append(scopes, window.Scope)
		}
	}
	return subscriptionruntime.Observation{
		Payload: payload, AccountObserved: true, QuotaObserved: true, ObservedQuotaScopes: scopes,
	}, nil
}

func credentialForTarget(credential subscriptionruntime.Credential, target subscriptionruntime.Target) (Storage, error) {
	value, err := ParseCredentialJSON(credential.Canonical())
	if err != nil {
		return Storage{}, err
	}
	var config struct {
		BaseURL string `json:"base_url"`
	}
	if len(target.Config) > 0 && string(target.Config) != "{}" {
		if err := json.Unmarshal(target.Config, &config); err != nil {
			return Storage{}, err
		}
	}
	if baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/"); baseURL != "" {
		value.RelayURL = baseURL
	}
	return value, nil
}

func runtimeCredential(value Storage) (subscriptionruntime.Credential, error) {
	canonical, err := MarshalCredential(value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	expiresAt, expires := value.AccessTokenExpiry(time.Now()), true
	if strings.TrimSpace(value.Expired) == "" {
		expires = false
	}
	account := subscriptionruntime.Account{
		Email: strings.TrimSpace(value.Email), ExpiresAt: expiresAt, ExpiresAtKnown: expires,
	}
	if refreshed, err := time.Parse(time.RFC3339, strings.TrimSpace(value.LastRefresh)); err == nil {
		account.LastRefresh, account.LastRefreshKnown = refreshed, true
	}
	return subscriptionruntime.NewCredential(
		canonical, value.Identity(), account, expiresAt, expires, value.SecretValues(),
	), nil
}

var (
	_ subscriptionruntime.BrowserAuthorizationDriver = (*driver)(nil)
	_ subscriptionruntime.RefreshIdentityMatcher     = (*driver)(nil)
)
