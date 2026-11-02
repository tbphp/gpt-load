// Package muse 隔离 Muse Code 订阅与 CPA Meta 桥接。
package muse

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/embedded"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

type Credential cpaembedded.MuseCredential

func ParseCredentialJSON(raw []byte) (Credential, error) {
	value, err := cpaembedded.ParseMuseCredentialJSON(raw)
	return Credential(value), err
}

func (c Credential) SecretValues() []string { return cpaembedded.MuseCredential(c).SecretValues() }

type museDriver struct{}

func Implementations() subscriptionruntime.Implementations {
	driver := &museDriver{}
	return subscriptionruntime.Implementations{
		Drivers:          []subscriptionruntime.Driver{driver},
		ModelDiscoveries: []subscriptionruntime.ModelDiscovery{museModelDiscovery{driver}},
	}
}

func (*museDriver) ID() spec.SubscriptionDriverID { return modules.MuseSubscriptionDriver }

func (*museDriver) Parse(raw []byte) (subscriptionruntime.Credential, error) {
	value, err := cpaembedded.ParseMuseCredentialJSON(raw)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return runtimeCredential(value)
}

func (*museDriver) ImportCredential(ctx context.Context, raw []byte) (subscriptionruntime.Credential, error) {
	options, err := museOptions(ctx)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	value, err := cpaembedded.ImportMuseCredential(ctx, raw, options)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return runtimeCredential(value)
}

func (*museDriver) Refresh(ctx context.Context, current subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
	value, err := cpaembedded.ParseMuseCredentialJSON(current.Canonical())
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	options, err := museOptions(ctx)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	updated, err := cpaembedded.RefreshMuseCredentialOnce(ctx, value, options)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return runtimeCredential(updated)
}

func (*museDriver) ClassifyRefreshFailure(err error) subscriptionruntime.RefreshFailureDecision {
	if errors.Is(err, cpaembedded.ErrMuseCredentialIdentityChanged) {
		return subscriptionruntime.RefreshFailureDecision{Kind: subscriptionruntime.RefreshFailureIdentityChanged}
	}
	decision := subscriptionruntime.RefreshFailureDecision{Kind: subscriptionruntime.RefreshFailureOutcomeUnknown}
	var upstream *cpaembedded.MuseHTTPError
	if errors.As(err, &upstream) {
		decision.StatusCode, decision.OAuthCode, decision.RetryAfter = upstream.StatusCode, upstream.Code, upstream.RetryAfter
		if subscriptionruntime.TokenEndpointFailureRetryable(upstream.StatusCode, upstream.Code) {
			decision.Kind = subscriptionruntime.RefreshFailureRetryable
		} else if upstream.StatusCode == 401 || upstream.StatusCode == 403 || upstream.Code == "invalid_grant" || upstream.Code == "invalid_token" || upstream.Code == "expired_token" || upstream.Code == "access_denied" {
			decision.Kind = subscriptionruntime.RefreshFailureReauthorizationRequired
		}
	}
	return decision
}

func (*museDriver) BeginDeviceAuthorization(ctx context.Context) (subscriptionruntime.DeviceAuthorization, error) {
	options, err := museOptions(ctx)
	if err != nil {
		return subscriptionruntime.DeviceAuthorization{}, err
	}
	value, err := cpaembedded.BeginMuseDeviceAuthorization(ctx, options)
	if err != nil {
		return subscriptionruntime.DeviceAuthorization{}, err
	}
	state, err := json.Marshal(value.State)
	return subscriptionruntime.DeviceAuthorization{VerificationURL: value.VerificationURL, UserCode: value.UserCode,
		DriverState: state, ExpiresAt: value.ExpiresAt, PollInterval: value.PollInterval}, err
}

func (*museDriver) PollDeviceAuthorization(ctx context.Context, raw []byte) (subscriptionruntime.DeviceAuthorizationPoll, error) {
	var state cpaembedded.MuseDeviceState
	if json.Unmarshal(raw, &state) != nil {
		return subscriptionruntime.DeviceAuthorizationPoll{}, errors.New("invalid Muse Code device state")
	}
	options, err := museOptions(ctx)
	if err != nil {
		return subscriptionruntime.DeviceAuthorizationPoll{}, err
	}
	value, err := cpaembedded.PollMuseDeviceAuthorizationOnce(ctx, state, options)
	if err != nil {
		return subscriptionruntime.DeviceAuthorizationPoll{}, err
	}
	result := subscriptionruntime.DeviceAuthorizationPoll{Status: subscriptionruntime.DeviceAuthorizationStatus(value.Status), PollInterval: value.PollInterval}
	if result.Status == subscriptionruntime.DeviceAuthorizationAuthorized {
		result.Credential, err = runtimeCredential(value.Credential)
	} else if result.Status == subscriptionruntime.DeviceAuthorizationPending {
		result.DriverState, err = json.Marshal(value.State)
	}
	return result, err
}

type museModelDiscovery struct{ *museDriver }

func (museModelDiscovery) ID() spec.UtilityID { return modules.MuseModelDiscovery }

func (museModelDiscovery) DiscoverModels(ctx context.Context, credential subscriptionruntime.Credential, target subscriptionruntime.Target) ([]string, error) {
	value, err := cpaembedded.ParseMuseCredentialJSON(credential.Canonical())
	if err != nil {
		return nil, err
	}
	baseURL, err := target.BaseURL()
	if err != nil {
		return nil, err
	}
	options, err := museOptions(ctx)
	if err != nil {
		return nil, err
	}
	models, err := cpaembedded.ListMuseModels(ctx, value, baseURL, options)
	var upstream *cpaembedded.MuseHTTPError
	if errors.As(err, &upstream) {
		return nil, &subscriptionruntime.UpstreamHTTPError{StatusCode: upstream.StatusCode}
	}
	return models, err
}

func runtimeCredential(value cpaembedded.MuseCredential) (subscriptionruntime.Credential, error) {
	raw, err := cpaembedded.MarshalMuseCredential(value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	defer clear(raw)
	account := subscriptionruntime.Account{Email: value.Email, PlanType: value.PlanType}
	if refreshed, err := time.Parse(time.RFC3339, value.LastRefresh); err == nil {
		account.LastRefresh, account.LastRefreshKnown = refreshed, true
	}
	// DCA 到期只影响后续兑换，不能把仍有效的请求密钥判成过期。
	return subscriptionruntime.NewCredential(raw, strings.ToLower(value.Email), account, time.Time{}, false, value.SecretValues()), nil
}

func museOptions(ctx context.Context) (cpaembedded.MuseOptions, error) {
	client, err := subscriptionruntime.HTTPClient(ctx)
	return cpaembedded.MuseOptions{HTTPClient: client}, err
}

var _ subscriptionruntime.DeviceAuthorizationDriver = (*museDriver)(nil)
var _ subscriptionruntime.CredentialFileImporter = (*museDriver)(nil)
