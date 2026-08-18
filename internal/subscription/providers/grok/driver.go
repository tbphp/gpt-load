package grok

import (
	"context"
	"errors"
	"strings"
	"time"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

type grokDriver struct{}

func newGrokDriver() *grokDriver { return &grokDriver{} }

func Implementations() subscriptionruntime.Implementations {
	driver := newGrokDriver()
	return subscriptionruntime.Implementations{
		Drivers:          []subscriptionruntime.Driver{driver},
		ModelDiscoveries: []subscriptionruntime.ModelDiscovery{grokModelDiscovery{driver}},
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

func (*grokDriver) ClassifyRefreshFailure(err error) subscriptionruntime.RefreshFailure {
	if errors.Is(err, ErrCredentialIdentityChanged) {
		return subscriptionruntime.RefreshFailureIdentityChanged
	}
	var tokenErr *TokenEndpointError
	if errors.As(err, &tokenErr) && IsDefinitiveRefreshRejection(tokenErr.Code) {
		return subscriptionruntime.RefreshFailureReauthorizationRequired
	}
	return subscriptionruntime.RefreshFailureOutcomeUnknown
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
	return models, nil
}

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
