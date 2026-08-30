package grok

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

type grokDriver struct{}

func newGrokDriver() *grokDriver { return &grokDriver{} }

func Implementations() subscriptionruntime.Implementations {
	driver := newGrokDriver()
	return subscriptionruntime.Implementations{
		Drivers:           []subscriptionruntime.Driver{driver},
		ModelDiscoveries:  []subscriptionruntime.ModelDiscovery{grokModelDiscovery{driver}},
		QuotaObservations: []subscriptionruntime.QuotaObservation{grokQuotaObservation{driver}},
	}
}

func (*grokDriver) ID() spec.SubscriptionDriverID { return modules.GrokSubscriptionDriver }

func (*grokDriver) Parse(raw []byte) (subscriptionruntime.Credential, error) {
	value, err := ParseCredentialJSON(raw)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	canonical, err := MarshalCredential(value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return grokRuntimeCredential(value, canonical), nil
}

func (*grokDriver) Refresh(ctx context.Context, current subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
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
	return grokRuntimeCredential(refreshed, canonical), nil
}

func (*grokDriver) ClassifyRefreshFailure(err error) subscriptionruntime.RefreshFailureDecision {
	if errors.Is(err, ErrCredentialIdentityChanged) {
		return subscriptionruntime.RefreshFailureDecision{Kind: subscriptionruntime.RefreshFailureIdentityChanged}
	}
	var tokenErr *TokenEndpointError
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

func (*grokDriver) BeginDeviceAuthorization(ctx context.Context) (subscriptionruntime.DeviceAuthorization, error) {
	value, err := BeginDeviceAuthorization(ctx)
	if err != nil {
		return subscriptionruntime.DeviceAuthorization{}, err
	}
	state, err := marshalDeviceState(value.State)
	if err != nil {
		return subscriptionruntime.DeviceAuthorization{}, err
	}
	return subscriptionruntime.DeviceAuthorization{
		VerificationURL: value.VerificationURL, UserCode: value.UserCode, DriverState: state,
		ExpiresAt: value.ExpiresAt, PollInterval: value.PollInterval,
	}, nil
}

func (*grokDriver) PollDeviceAuthorization(ctx context.Context, raw []byte) (subscriptionruntime.DeviceAuthorizationPoll, error) {
	state, err := unmarshalDeviceState(raw)
	if err != nil {
		return subscriptionruntime.DeviceAuthorizationPoll{}, err
	}
	value, err := PollDeviceAuthorizationOnce(ctx, state)
	if err != nil {
		return subscriptionruntime.DeviceAuthorizationPoll{}, err
	}
	result := subscriptionruntime.DeviceAuthorizationPoll{PollInterval: value.PollInterval}
	switch value.Status {
	case DeviceAuthorizationPending:
		result.Status = subscriptionruntime.DeviceAuthorizationPending
		result.DriverState, err = marshalDeviceState(value.State)
	case DeviceAuthorizationAuthorized:
		result.Status = subscriptionruntime.DeviceAuthorizationAuthorized
		canonical, marshalErr := MarshalCredential(value.Credential)
		if marshalErr != nil {
			return subscriptionruntime.DeviceAuthorizationPoll{}, marshalErr
		}
		result.Credential = grokRuntimeCredential(value.Credential, canonical)
	case DeviceAuthorizationDenied:
		result.Status = subscriptionruntime.DeviceAuthorizationDenied
	case DeviceAuthorizationExpired:
		result.Status = subscriptionruntime.DeviceAuthorizationExpired
	default:
		return subscriptionruntime.DeviceAuthorizationPoll{}, errors.New("unknown Grok device authorization status")
	}
	return result, err
}

func (*grokDriver) ImportCredential(ctx context.Context, raw []byte) (subscriptionruntime.Credential, error) {
	value, err := ImportCredential(ctx, raw)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	canonical, err := MarshalCredential(value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return grokRuntimeCredential(value, canonical), nil
}

type grokModelDiscovery struct{ *grokDriver }

func (grokModelDiscovery) ID() spec.UtilityID { return modules.GrokModelDiscovery }

func (*grokDriver) DiscoverModels(ctx context.Context, credential subscriptionruntime.Credential) ([]string, error) {
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
	return cpaembedded.MergeModelCatalog(cpaembedded.ProviderGrok, models), nil
}

func (*grokDriver) Observe(ctx context.Context, credential subscriptionruntime.Credential) (subscriptionruntime.Observation, error) {
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
		if errors.Is(err, ErrAccountObservationPayloadInvalid) {
			return subscriptionruntime.Observation{}, subscriptionruntime.ErrObservationPayloadInvalid
		}
		return subscriptionruntime.Observation{}, err
	}
	normalized, err := NormalizeObservation(value.Email, observed)
	if err != nil {
		return subscriptionruntime.Observation{}, fmt.Errorf("%w: %v", subscriptionruntime.ErrObservationPayloadInvalid, err)
	}
	observedScopes := grokObservedQuotaScopes(observed)
	quotaObserved := len(observedScopes) > 0
	return subscriptionruntime.Observation{
		Payload: normalized, Header: observed.Header.Clone(), Partial: len(observed.IncompleteSources) > 0,
		AccountObserved: observed.AccountObserved, QuotaObserved: quotaObserved,
		ObservedQuotaScopes: observedScopes,
	}, nil
}

func grokObservedQuotaScopes(observed AccountObservation) []string {
	scopes := make([]string, 0, 3)
	if observed.AccountQuotaObserved {
		scopes = append(scopes, quotaScopeAccount)
	}
	if observed.SurfaceQuotaObserved {
		scopes = append(scopes, quotaScopeSurface)
	}
	if observed.CreditQuotaObserved {
		scopes = append(scopes, quotaScopeCredits)
	}
	return scopes
}

type grokQuotaObservation struct{ *grokDriver }

func (grokQuotaObservation) ID() spec.UtilityID { return modules.GrokQuotaObservation }

func grokRuntimeCredential(value Credential, canonical []byte) subscriptionruntime.Credential {
	expiresAt, expires := CredentialExpiresAt(value)
	account := subscriptionruntime.Account{
		Email: strings.TrimSpace(value.Email), ExpiresAt: expiresAt, ExpiresAtKnown: expires,
	}
	if refreshed, err := time.Parse(time.RFC3339, strings.TrimSpace(value.LastRefresh)); err == nil {
		account.LastRefresh, account.LastRefreshKnown = refreshed, true
	}
	return subscriptionruntime.NewCredential(
		canonical, strings.TrimSpace(value.AccountID), account, expiresAt, expires, value.SecretValues(),
	)
}

var _ subscriptionruntime.DeviceAuthorizationDriver = (*grokDriver)(nil)
var _ subscriptionruntime.CredentialFileImporter = (*grokDriver)(nil)
