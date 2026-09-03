// Package kiro owns GPT-Load's Kiro subscription contract and isolates the
// embedded CLIProxyAPI Kiro bridge from the rest of the application.
package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"

	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

const Provider = cpaembedded.ProviderKiro

var (
	ErrCredentialIdentityChanged        = errors.New("refreshed kiro credential identity changed")
	ErrModelDiscoveryUnavailable        = errors.New("Kiro model discovery is unavailable")
	ErrAccountObservationUnavailable    = errors.New("Kiro account observation is unavailable")
	ErrAccountObservationPayloadInvalid = errors.New("Kiro account observation payload is invalid")
)

type Credential struct {
	Type          string `json:"type"`
	AuthKind      string `json:"auth_kind,omitempty"`
	AccessToken   string `json:"access_token,omitempty"`
	RefreshToken  string `json:"refresh_token,omitempty"`
	TokenType     string `json:"token_type,omitempty"`
	ExpiresIn     int    `json:"expires_in,omitempty"`
	Expire        string `json:"expired,omitempty"`
	LastRefresh   string `json:"last_refresh,omitempty"`
	AccountID     string `json:"account_id,omitempty"`
	Email         string `json:"email,omitempty"`
	Region        string `json:"region,omitempty"`
	ProfileARN    string `json:"profile_arn,omitempty"`
	TokenEndpoint string `json:"token_endpoint,omitempty"`
}

type DeviceAuthorization struct {
	VerificationURL string
	UserCode        string
	State           DeviceAuthorizationState
	ExpiresAt       time.Time
	PollInterval    time.Duration
}

type DeviceAuthorizationState struct {
	DeviceCode          string `json:"device_code"`
	TokenEndpoint       string `json:"token_endpoint"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	LoginProvider       string `json:"login_provider,omitempty"`
}

type DeviceAuthorizationStatus string

const (
	DeviceAuthorizationPending    DeviceAuthorizationStatus = "pending"
	DeviceAuthorizationAuthorized DeviceAuthorizationStatus = "authorized"
	DeviceAuthorizationDenied     DeviceAuthorizationStatus = "denied"
	DeviceAuthorizationExpired    DeviceAuthorizationStatus = "expired"
)

type DeviceAuthorizationPoll struct {
	Status       DeviceAuthorizationStatus
	State        DeviceAuthorizationState
	Credential   Credential
	PollInterval time.Duration
}

type TokenEndpointError struct {
	StatusCode int
	Code       string
	RetryAfter time.Duration
}

func (err *TokenEndpointError) Error() string {
	if err == nil {
		return "Kiro token endpoint failed"
	}
	return fmt.Sprintf("Kiro token endpoint returned status %d", err.StatusCode)
}

func (err *TokenEndpointError) HTTPStatusCode() int {
	if err == nil {
		return 0
	}
	return err.StatusCode
}

type UpstreamHTTPError struct {
	Operation  string
	StatusCode int
}

func (err *UpstreamHTTPError) Error() string {
	if err == nil {
		return "Kiro upstream request failed"
	}
	return fmt.Sprintf("Kiro %s endpoint returned status %d", err.Operation, err.StatusCode)
}

func (err *UpstreamHTTPError) HTTPStatusCode() int {
	if err == nil {
		return 0
	}
	return err.StatusCode
}

func ParseCredentialJSON(raw []byte) (Credential, error) {
	value, err := cpaembedded.ParseKiroCredentialJSON(raw)
	if err != nil {
		return Credential{}, normalizeError(err)
	}
	return credentialFromBridge(value), nil
}

func MarshalCredential(value Credential) ([]byte, error) {
	raw, err := cpaembedded.MarshalKiroCredential(credentialToBridge(value))
	if err != nil {
		return nil, normalizeError(err)
	}
	return raw, nil
}

func CredentialExpiresAt(value Credential) (time.Time, bool) {
	return cpaembedded.KiroCredentialExpiresAt(credentialToBridge(value))
}

func (value Credential) SecretValues() []string { return credentialToBridge(value).SecretValues() }

func BeginDeviceAuthorization(ctx context.Context) (DeviceAuthorization, error) {
	options, err := kiroOptions(ctx)
	if err != nil {
		return DeviceAuthorization{}, err
	}
	value, err := cpaembedded.BeginKiroDeviceAuthorization(ctx, options, "BuilderId")
	if err != nil {
		return DeviceAuthorization{}, normalizeError(err)
	}
	return DeviceAuthorization{
		VerificationURL: value.VerificationURL, UserCode: value.UserCode,
		State: stateFromBridge(value.State), ExpiresAt: value.ExpiresAt, PollInterval: value.PollInterval,
	}, nil
}

func PollDeviceAuthorizationOnce(ctx context.Context, state DeviceAuthorizationState) (DeviceAuthorizationPoll, error) {
	options, err := kiroOptions(ctx)
	if err != nil {
		return DeviceAuthorizationPoll{}, err
	}
	value, err := cpaembedded.PollKiroDeviceAuthorizationOnce(ctx, stateToBridge(state), options)
	if err != nil {
		return DeviceAuthorizationPoll{}, normalizeError(err)
	}
	return DeviceAuthorizationPoll{
		Status: DeviceAuthorizationStatus(value.Status), State: stateFromBridge(value.State),
		Credential: credentialFromBridge(value.Credential), PollInterval: value.PollInterval,
	}, nil
}

func RefreshCredentialOnce(ctx context.Context, current Credential) (Credential, error) {
	options, err := kiroOptions(ctx)
	if err != nil {
		return Credential{}, err
	}
	value, err := cpaembedded.RefreshKiroCredentialOnce(ctx, credentialToBridge(current), options)
	if err != nil {
		return Credential{}, normalizeError(err)
	}
	return credentialFromBridge(value), nil
}

func ImportCredential(ctx context.Context, raw []byte) (Credential, error) {
	options, err := kiroOptions(ctx)
	if err != nil {
		return Credential{}, err
	}
	value, err := cpaembedded.ImportKiroCredential(ctx, raw, options)
	if err != nil {
		return Credential{}, normalizeError(err)
	}
	return credentialFromBridge(value), nil
}

// DiscoverLocalCredential looks for an already-signed-in Kiro account on the
// local machine (the AWS SSO token cache that the Kiro desktop app writes) and
// returns it without running any interactive authorization. The boolean reports
// whether a usable token was found; a missing account is not an error.
func DiscoverLocalCredential(context.Context) (Credential, bool, error) {
	value, err := cpaembedded.DiscoverKiroCredential()
	if err != nil {
		return Credential{}, false, nil
	}
	return credentialFromBridge(value), true, nil
}

func ListModels(ctx context.Context, credential Credential) ([]string, error) {
	options, err := kiroOptions(ctx)
	if err != nil {
		return nil, err
	}
	values, err := cpaembedded.DiscoverKiroModels(ctx, credentialToBridge(credential), options)
	if err != nil {
		return nil, normalizeError(err)
	}
	return append([]string(nil), values...), nil
}

func ObserveAccount(ctx context.Context, credential Credential) (AccountObservation, error) {
	options, err := kiroOptions(ctx)
	if err != nil {
		return AccountObservation{}, err
	}
	value, err := cpaembedded.ObserveKiroAccount(ctx, credentialToBridge(credential), options)
	if err != nil {
		return AccountObservation{}, normalizeError(err)
	}
	meters := make([]UsageMeter, 0, len(value.Usage.Meters))
	for _, meter := range value.Usage.Meters {
		meters = append(meters, UsageMeter{
			DisplayName: meter.DisplayName, Unit: meter.Unit,
			CurrentUsage: meter.CurrentUsage, UsageLimit: meter.UsageLimit,
			UsageLimitExplicit: meter.UsageLimitExplicit, PercentageUsed: meter.PercentageUsed,
			ResetDate: meter.ResetDate,
		})
	}
	return AccountObservation{
		Usage:   UsageObservation{Meters: meters},
		ModelID: value.ModelID, Header: value.Header.Clone(),
		AccountObserved: value.AccountObserved, AccountQuotaObserved: value.AccountQuotaObserved,
		CreditQuotaObserved: value.CreditQuotaObserved, LoadedViaFreecode: value.LoadedViaFreecode,
		IncompleteSources: append([]string(nil), value.IncompleteSources...),
	}, nil
}

// CountTokensLocal produces a credential-less local token estimate for the
// Anthropic count-tokens trajectory. Kiro exposes no token-counting endpoint,
// so the value is a byte/whitespace heuristic in the Anthropic count_tokens
// response shape.
func CountTokensLocal(payload []byte) []byte {
	return cpaembedded.CountKiroTokensLocal(payload)
}

func kiroOptions(ctx context.Context) (cpaembedded.KiroOptions, error) {
	client, err := subscriptionruntime.HTTPClient(ctx)
	if err != nil {
		return cpaembedded.KiroOptions{}, err
	}
	return cpaembedded.KiroOptions{HTTPClient: client}, nil
}

func IsDefinitiveRefreshRejection(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "invalid_grant", "refresh_token_expired", "refresh_token_revoked", "access_denied":
		return true
	default:
		return false
	}
}

func credentialFromBridge(value cpaembedded.KiroCredential) Credential {
	return Credential{
		Type: value.Type, AuthKind: value.AuthKind, AccessToken: value.AccessToken,
		RefreshToken: value.RefreshToken, TokenType: value.TokenType, ExpiresIn: value.ExpiresIn,
		Expire: value.Expire, LastRefresh: value.LastRefresh, AccountID: value.AccountID,
		Email: value.Email, Region: value.Region, ProfileARN: value.ProfileARN,
		TokenEndpoint: value.TokenEndpoint,
	}
}

func credentialToBridge(value Credential) cpaembedded.KiroCredential {
	return cpaembedded.KiroCredential{
		Type: value.Type, AuthKind: value.AuthKind, AccessToken: value.AccessToken,
		RefreshToken: value.RefreshToken, TokenType: value.TokenType, ExpiresIn: value.ExpiresIn,
		Expire: value.Expire, LastRefresh: value.LastRefresh, AccountID: value.AccountID,
		Email: value.Email, Region: value.Region, ProfileARN: value.ProfileARN,
		TokenEndpoint: value.TokenEndpoint,
	}
}

func stateFromBridge(value cpaembedded.KiroDeviceState) DeviceAuthorizationState {
	return DeviceAuthorizationState{
		DeviceCode: value.DeviceCode, TokenEndpoint: value.TokenEndpoint,
		PollIntervalSeconds: value.PollIntervalSeconds, LoginProvider: value.LoginProvider,
	}
}

func stateToBridge(value DeviceAuthorizationState) cpaembedded.KiroDeviceState {
	return cpaembedded.KiroDeviceState{
		DeviceCode: value.DeviceCode, TokenEndpoint: value.TokenEndpoint,
		PollIntervalSeconds: value.PollIntervalSeconds, LoginProvider: value.LoginProvider,
	}
}

func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, cpaembedded.ErrKiroCredentialIdentityChanged) {
		return ErrCredentialIdentityChanged
	}
	var tokenErr *cpaembedded.KiroTokenEndpointError
	if errors.As(err, &tokenErr) {
		return &TokenEndpointError{
			StatusCode: tokenErr.StatusCode, Code: strings.TrimSpace(tokenErr.Code),
			RetryAfter: tokenErr.RetryAfter,
		}
	}
	var upstream *cpaembedded.KiroUpstreamHTTPError
	if errors.As(err, &upstream) {
		return &UpstreamHTTPError{Operation: strings.TrimSpace(upstream.Operation), StatusCode: upstream.StatusCode}
	}
	if errors.Is(err, cpaembedded.ErrKiroAccountObservationPayloadInvalid) {
		return ErrAccountObservationPayloadInvalid
	}
	if errors.Is(err, cpaembedded.ErrKiroAccountObservationUnavailable) {
		return ErrAccountObservationUnavailable
	}
	if errors.Is(err, cpaembedded.ErrKiroModelDiscoveryUnavailable) {
		return ErrModelDiscoveryUnavailable
	}
	return err
}

func marshalDeviceState(value DeviceAuthorizationState) ([]byte, error) { return json.Marshal(value) }

func unmarshalDeviceState(raw []byte) (DeviceAuthorizationState, error) {
	var value DeviceAuthorizationState
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return DeviceAuthorizationState{}, err
	}
	if strings.TrimSpace(value.DeviceCode) == "" || strings.TrimSpace(value.TokenEndpoint) == "" ||
		value.PollIntervalSeconds < 1 || value.PollIntervalSeconds > 60 {
		return DeviceAuthorizationState{}, errors.New("invalid Kiro device authorization state")
	}
	return value, nil
}
