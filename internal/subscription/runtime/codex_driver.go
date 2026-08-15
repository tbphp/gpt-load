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
	"gpt-load/internal/codex"
)

type codexDriver struct{}

func newCodexDriver() *codexDriver { return &codexDriver{} }

func (*codexDriver) ID() spec.SubscriptionDriverID { return modules.CodexSubscriptionDriver }

func (*codexDriver) Parse(raw []byte) (Credential, error) {
	value, err := codex.ParseCredentialJSON(raw)
	if err != nil {
		return Credential{}, err
	}
	canonical, err := codex.MarshalCredential(value)
	if err != nil {
		return Credential{}, err
	}
	return codexRuntimeCredential(value, canonical), nil
}

func (*codexDriver) Refresh(ctx context.Context, current Credential) (Credential, error) {
	value, err := codex.ParseCredentialJSON(current.canonical)
	if err != nil {
		return Credential{}, err
	}
	refreshed, err := codex.RefreshCredentialOnce(ctx, value)
	if err != nil {
		return Credential{}, err
	}
	canonical, err := codex.MarshalCredential(refreshed)
	if err != nil {
		return Credential{}, err
	}
	return codexRuntimeCredential(refreshed, canonical), nil
}

func (*codexDriver) ClassifyRefreshFailure(err error) RefreshFailure {
	var tokenErr *codex.TokenEndpointError
	if errors.Is(err, codex.ErrCredentialIdentityChanged) {
		return RefreshFailureIdentityChanged
	}
	if errors.As(err, &tokenErr) && codex.IsDefinitiveRefreshRejection(tokenErr.Code) {
		return RefreshFailureReauthorizationRequired
	}
	return RefreshFailureOutcomeUnknown
}

type codexAuthorizationState struct {
	Verifier string `json:"verifier"`
}

func (*codexDriver) BeginAuthorization() (Authorization, error) {
	value, err := codex.BeginBrowserAuthorization()
	if err != nil {
		return Authorization{}, err
	}
	state, err := json.Marshal(codexAuthorizationState{Verifier: value.CodeVerifier})
	if err != nil {
		return Authorization{}, err
	}
	return Authorization{
		URL: value.AuthorizationURL, State: value.State, DriverState: state,
		ExpiresAt: value.ExpiresAt, LocalCallback: true,
	}, nil
}

func (*codexDriver) RequiresLocalCallback() bool { return true }

func (*codexDriver) CompleteAuthorization(ctx context.Context, completion AuthorizationCompletion) (Credential, error) {
	var state codexAuthorizationState
	if json.Unmarshal(completion.DriverState, &state) != nil || strings.TrimSpace(state.Verifier) == "" {
		return Credential{}, codex.ErrInvalidState
	}
	value, err := codex.CompleteBrowserAuthorization(ctx, codex.BrowserAuthorizationCompletion{
		ExpectedState: completion.ExpectedState, ReturnedState: completion.ReturnedState,
		Code: completion.Code, CodeVerifier: state.Verifier,
	})
	if err != nil {
		return Credential{}, err
	}
	canonical, err := codex.MarshalCredential(value)
	if err != nil {
		return Credential{}, err
	}
	return codexRuntimeCredential(value, canonical), nil
}

func (*codexDriver) AuthorizationFailureDefinitive(err error) bool {
	var tokenErr *codex.TokenEndpointError
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

func (*codexDriver) DiscoverModels(ctx context.Context, credential Credential) ([]string, error) {
	value, err := codex.ParseCredentialJSON(credential.canonical)
	if err != nil {
		return nil, err
	}
	models, err := codex.ListModels(ctx, value)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(models))
	for _, model := range models {
		result = append(result, model.ID)
	}
	return result, nil
}

func (*codexDriver) Observe(ctx context.Context, credential Credential) (Observation, error) {
	value, err := codex.ParseCredentialJSON(credential.canonical)
	if err != nil {
		return Observation{}, err
	}
	observed, err := codex.ObserveAccount(ctx, value)
	if err != nil {
		return Observation{}, err
	}
	var detailsPayload []byte
	details, detailErr := codex.ObserveResetCredits(ctx, value)
	if detailErr == nil {
		detailsPayload = details.Payload
	}
	normalized, err := NormalizeCodexQuota(observed.Payload, detailsPayload)
	if err != nil {
		return Observation{}, fmt.Errorf("%w: %v", ErrObservationPayloadInvalid, err)
	}
	return Observation{Payload: normalized, Header: observed.Header.Clone()}, nil
}

func (*codexDriver) Consume(ctx context.Context, credential Credential, requestID string) (ResetCreditResult, error) {
	value, err := codex.ParseCredentialJSON(credential.canonical)
	if err != nil {
		return ResetCreditResult{}, err
	}
	result, err := codex.ConsumeResetCredit(ctx, value, requestID)
	if err != nil {
		var upstream *codex.UpstreamHTTPError
		if errors.As(err, &upstream) {
			return ResetCreditResult{}, &UpstreamHTTPError{StatusCode: upstream.StatusCode}
		}
		return ResetCreditResult{}, err
	}
	return NormalizeCodexResetCreditResult(result.Payload)
}

type codexResetCreditPayload struct {
	Code         string `json:"code"`
	WindowsReset int    `json:"windows_reset"`
	Credit       *struct {
		RedeemedAt string `json:"redeemed_at"`
	} `json:"credit"`
}

// NormalizeCodexResetCreditResult converts the Codex mutation payload into the
// provider-neutral action result persisted by the control plane.
func NormalizeCodexResetCreditResult(raw []byte) (ResetCreditResult, error) {
	var payload codexResetCreditPayload
	if json.Unmarshal(raw, &payload) != nil || !strings.EqualFold(strings.TrimSpace(payload.Code), "reset") || payload.WindowsReset < 0 {
		return ResetCreditResult{}, errors.New("invalid subscription reset-credit response")
	}
	result := ResetCreditResult{Status: "succeeded", WindowsReset: payload.WindowsReset}
	if payload.Credit != nil && strings.TrimSpace(payload.Credit.RedeemedAt) != "" {
		if parsed, err := time.Parse(time.RFC3339, payload.Credit.RedeemedAt); err == nil {
			value := parsed.UnixMilli()
			result.RedeemedAtMS = &value
		}
	}
	return result, nil
}

// Go cannot overload ID across the narrow capability interfaces, so wrappers
// expose each typed ID while sharing the implementation below.
type codexModelDiscovery struct{ *codexDriver }
type codexQuotaObservation struct{ *codexDriver }
type codexResetCreditAction struct{ *codexDriver }

func (driver *codexDriver) modelDiscovery() ModelDiscovery     { return codexModelDiscovery{driver} }
func (driver *codexDriver) quotaObservation() QuotaObservation { return codexQuotaObservation{driver} }
func (driver *codexDriver) resetCreditAction() ResetCreditAction {
	return codexResetCreditAction{driver}
}

func (codexModelDiscovery) ID() spec.UtilityID   { return modules.CodexModelDiscovery }
func (codexQuotaObservation) ID() spec.UtilityID { return modules.CodexQuotaObservation }
func (codexResetCreditAction) ID() spec.ActionID { return modules.CodexResetCreditAction }

func codexRuntimeCredential(value codex.Credential, canonical []byte) Credential {
	expiresAt, expires := codex.CredentialExpiresAt(value)
	account := Account{Email: strings.TrimSpace(value.Email), ExpiresAt: expiresAt, ExpiresAtKnown: expires}
	if refreshed, err := time.Parse(time.RFC3339, strings.TrimSpace(value.LastRefresh)); err == nil {
		account.LastRefresh, account.LastRefreshKnown = refreshed, true
	}
	return newCredential(canonical, strings.TrimSpace(value.AccountID), account, expiresAt, expires, value.SecretValues())
}
