// Package devin 将 Devin 私有认证及 CPA 能力隔离在订阅渠道内。
package devin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/embedded"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

type Credential cpaembedded.DevinCredential

func ParseCredentialJSON(raw []byte) (Credential, error) {
	value, err := cpaembedded.ParseDevinCredentialJSON(raw)
	return Credential(value), err
}
func (c Credential) SecretValues() []string { return cpaembedded.DevinCredential(c).SecretValues() }

type devinDriver struct{}

func Implementations() subscriptionruntime.Implementations {
	driver := &devinDriver{}
	return subscriptionruntime.Implementations{Drivers: []subscriptionruntime.Driver{driver}, ModelDiscoveries: []subscriptionruntime.ModelDiscovery{devinModelDiscovery{}}, QuotaObservations: []subscriptionruntime.QuotaObservation{devinQuotaObservation{}}}
}

func (*devinDriver) ID() spec.SubscriptionDriverID { return modules.DevinSubscriptionDriver }
func (*devinDriver) Parse(raw []byte) (subscriptionruntime.Credential, error) {
	value, err := cpaembedded.ParseDevinCredentialJSON(raw)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return runtimeCredential(value)
}
func (*devinDriver) ImportCredential(ctx context.Context, raw []byte) (subscriptionruntime.Credential, error) {
	options, err := devinOptions(ctx)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	value, err := cpaembedded.ImportDevinCredential(ctx, raw, options)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return runtimeCredential(value)
}
func (*devinDriver) Refresh(ctx context.Context, current subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
	value, err := cpaembedded.ParseDevinCredentialJSON(current.Canonical())
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	options, err := devinOptions(ctx)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	value, err = cpaembedded.RefreshDevinCredentialOnce(ctx, value, options)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return runtimeCredential(value)
}
func (*devinDriver) MatchesRefreshIdentity(current, updated subscriptionruntime.Credential) bool {
	before, err := ParseCredentialJSON(current.Canonical())
	if err != nil {
		return false
	}
	after, err := ParseCredentialJSON(updated.Canonical())
	return err == nil && before.UserID == after.UserID && (before.OrgID == "" || before.OrgID == after.OrgID)
}
func (*devinDriver) ClassifyRefreshFailure(err error) subscriptionruntime.RefreshFailureDecision {
	if errors.Is(err, cpaembedded.ErrDevinCredentialIdentityChanged) {
		return subscriptionruntime.RefreshFailureDecision{Kind: subscriptionruntime.RefreshFailureIdentityChanged}
	}
	// 刷新只读账号状态，不轮换令牌；临时失败保留现有凭据并由运行时退避重试。
	decision := subscriptionruntime.RefreshFailureDecision{Kind: subscriptionruntime.RefreshFailureRetryable}
	var upstream *cpaembedded.DevinHTTPError
	if errors.As(err, &upstream) {
		decision.StatusCode, decision.RetryAfter = upstream.StatusCode, upstream.RetryAfter
		if upstream.StatusCode == http.StatusUnauthorized || upstream.StatusCode == http.StatusForbidden {
			decision.Kind = subscriptionruntime.RefreshFailureReauthorizationRequired
		} else if subscriptionruntime.TokenEndpointFailureRetryable(upstream.StatusCode, "") {
			decision.Kind = subscriptionruntime.RefreshFailureRetryable
		}
	}
	return decision
}

type authorizationState struct {
	Verifier string `json:"verifier"`
}

func (*devinDriver) BeginAuthorization() (subscriptionruntime.Authorization, error) {
	value, err := cpaembedded.BeginDevinBrowserAuthorization()
	if err != nil {
		return subscriptionruntime.Authorization{}, err
	}
	state, err := json.Marshal(authorizationState{Verifier: value.CodeVerifier})
	return subscriptionruntime.Authorization{URL: value.AuthorizationURL, State: value.State, DriverState: state, ExpiresAt: value.ExpiresAt}, err
}
func (*devinDriver) LocalCallback() (subscriptionruntime.LocalCallbackSpec, bool) {
	return subscriptionruntime.LocalCallbackSpec{RedirectURI: cpaembedded.DevinRedirectURI}, true
}
func (*devinDriver) CompleteAuthorization(ctx context.Context, completion subscriptionruntime.AuthorizationCompletion) (subscriptionruntime.Credential, error) {
	var state authorizationState
	if json.Unmarshal(completion.DriverState, &state) != nil || state.Verifier == "" {
		return subscriptionruntime.Credential{}, errors.New("invalid Devin authorization state")
	}
	options, err := devinOptions(ctx)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	value, err := cpaembedded.CompleteDevinBrowserAuthorization(ctx, cpaembedded.BrowserAuthorizationCompletion{ExpectedState: completion.ExpectedState, ReturnedState: completion.ReturnedState, Code: completion.Code, CodeVerifier: state.Verifier}, options)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return runtimeCredential(value)
}
func (*devinDriver) AuthorizationFailureDefinitive(err error) bool {
	var upstream *cpaembedded.DevinHTTPError
	return errors.As(err, &upstream) && (upstream.StatusCode == 400 || upstream.StatusCode == 401 || upstream.StatusCode == 403)
}

type devinModelDiscovery struct{}

func (devinModelDiscovery) ID() spec.UtilityID { return modules.DevinModelDiscovery }
func (devinModelDiscovery) DiscoverModels(ctx context.Context, current subscriptionruntime.Credential, target subscriptionruntime.Target) ([]string, error) {
	if _, err := observeAccount(ctx, current, target); err != nil {
		return nil, err
	}
	return cpaembedded.ListDevinModels(), nil
}

type devinQuotaObservation struct{}

func (devinQuotaObservation) ID() spec.UtilityID { return modules.DevinQuotaObservation }
func (devinQuotaObservation) Observe(ctx context.Context, current subscriptionruntime.Credential, target subscriptionruntime.Target) (subscriptionruntime.Observation, error) {
	value, err := observeAccount(ctx, current, target)
	if err != nil {
		return subscriptionruntime.Observation{}, err
	}
	observedAt := time.Now()
	payload, err := normalizeObservation(value)
	if err != nil {
		return subscriptionruntime.Observation{}, subscriptionruntime.ErrObservationPayloadInvalid
	}
	scopes := []string{}
	if value.DailyRemaining != nil {
		scopes = append(scopes, "daily")
	}
	if value.WeeklyRemaining != nil {
		scopes = append(scopes, "weekly")
	}
	return subscriptionruntime.Observation{Payload: payload, AccountObserved: true, QuotaObserved: len(scopes) > 0, Partial: len(scopes) != 2, QuotaObservedAt: observedAt, ObservedQuotaScopes: scopes}, nil
}
func observeAccount(ctx context.Context, current subscriptionruntime.Credential, target subscriptionruntime.Target) (cpaembedded.DevinAccountObservation, error) {
	value, err := cpaembedded.ParseDevinCredentialJSON(current.Canonical())
	if err != nil {
		return cpaembedded.DevinAccountObservation{}, err
	}
	baseURL, err := target.BaseURL()
	if err != nil {
		return cpaembedded.DevinAccountObservation{}, err
	}
	options, err := devinOptions(ctx)
	if err != nil {
		return cpaembedded.DevinAccountObservation{}, err
	}
	observed, err := cpaembedded.ObserveDevinAccount(ctx, value, baseURL, options)
	var upstream *cpaembedded.DevinHTTPError
	if errors.As(err, &upstream) {
		return cpaembedded.DevinAccountObservation{}, &subscriptionruntime.UpstreamHTTPError{StatusCode: upstream.StatusCode}
	}
	return observed, err
}
func runtimeCredential(value cpaembedded.DevinCredential) (subscriptionruntime.Credential, error) {
	raw, err := cpaembedded.MarshalDevinCredential(value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	defer clear(raw)
	account := subscriptionruntime.Account{Email: value.Email, PlanType: value.Plan}
	if refreshed, err := time.Parse(time.RFC3339, value.LastRefresh); err == nil {
		account.LastRefresh, account.LastRefreshKnown = refreshed, true
	}
	return subscriptionruntime.NewCredential(raw, value.UserID+"\x00"+value.OrgID, account, time.Time{}, false, value.SecretValues()), nil
}
func devinOptions(ctx context.Context) (cpaembedded.DevinOptions, error) {
	client, err := subscriptionruntime.HTTPClient(ctx)
	return cpaembedded.DevinOptions{HTTPClient: client}, err
}

var _ subscriptionruntime.BrowserAuthorizationDriver = (*devinDriver)(nil)
var _ subscriptionruntime.CredentialFileImporter = (*devinDriver)(nil)
