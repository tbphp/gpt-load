// Package grok owns GPT-Load's Grok subscription contract and isolates the
// embedded CLIProxyAPI xAI bridge from the rest of the application.
package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"

	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

const Provider = cpaembedded.ProviderGrok

var (
	ErrCredentialIdentityChanged        = errors.New("refreshed grok credential identity changed")
	ErrAccountObservationUnavailable    = errors.New("Grok account observation is unavailable")
	ErrAccountObservationPayloadInvalid = errors.New("Grok account observation payload is invalid")
)

type Credential struct {
	Type          string `json:"type"`
	AccessToken   string `json:"access_token"`
	RefreshToken  string `json:"refresh_token"`
	IDToken       string `json:"id_token,omitempty"`
	TokenType     string `json:"token_type,omitempty"`
	ExpiresIn     int    `json:"expires_in,omitempty"`
	Expire        string `json:"expired"`
	LastRefresh   string `json:"last_refresh,omitempty"`
	AccountID     string `json:"account_id"`
	Email         string `json:"email"`
	TokenEndpoint string `json:"token_endpoint"`
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
	UserInfoEndpoint    string `json:"userinfo_endpoint"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
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
}

func (err *TokenEndpointError) Error() string {
	if err == nil {
		return "Grok token endpoint failed"
	}
	return fmt.Sprintf("Grok token endpoint returned status %d", err.StatusCode)
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

type ProductUsage struct {
	Product      string
	UsagePercent *float64
}

type BillingObservation struct {
	PeriodType         string
	PeriodStart        string
	PeriodEnd          string
	UsagePercent       *float64
	ProductUsage       []ProductUsage
	MonthlyLimitCents  *float64
	UsedCents          *float64
	OnDemandCapCents   *float64
	OnDemandUsedCents  *float64
	BillingPeriodStart string
	BillingPeriodEnd   string
}

type AccountObservation struct {
	Billing              BillingObservation
	Tier                 *int
	Header               http.Header
	AccountObserved      bool
	AccountQuotaObserved bool
	SurfaceQuotaObserved bool
	CreditQuotaObserved  bool
	IncompleteSources    []string
}

func (err *UpstreamHTTPError) Error() string {
	if err == nil {
		return "Grok upstream request failed"
	}
	return fmt.Sprintf("Grok %s endpoint returned status %d", err.Operation, err.StatusCode)
}

func (err *UpstreamHTTPError) HTTPStatusCode() int {
	if err == nil {
		return 0
	}
	return err.StatusCode
}

func ParseCredentialJSON(raw []byte) (Credential, error) {
	value, err := cpaembedded.ParseGrokCredentialJSON(raw)
	if err != nil {
		return Credential{}, normalizeError(err)
	}
	return credentialFromBridge(value), nil
}

func MarshalCredential(value Credential) ([]byte, error) {
	raw, err := cpaembedded.MarshalGrokCredential(credentialToBridge(value))
	if err != nil {
		return nil, normalizeError(err)
	}
	return raw, nil
}

func CredentialExpiresAt(value Credential) (time.Time, bool) {
	return cpaembedded.GrokCredentialExpiresAt(credentialToBridge(value))
}

func (value Credential) SecretValues() []string { return credentialToBridge(value).SecretValues() }

func BeginDeviceAuthorization(ctx context.Context) (DeviceAuthorization, error) {
	options, err := grokOptions(ctx)
	if err != nil {
		return DeviceAuthorization{}, err
	}
	value, err := cpaembedded.BeginGrokDeviceAuthorization(ctx, options)
	if err != nil {
		return DeviceAuthorization{}, normalizeError(err)
	}
	return DeviceAuthorization{
		VerificationURL: value.VerificationURL, UserCode: value.UserCode,
		State: stateFromBridge(value.State), ExpiresAt: value.ExpiresAt, PollInterval: value.PollInterval,
	}, nil
}

func PollDeviceAuthorizationOnce(ctx context.Context, state DeviceAuthorizationState) (DeviceAuthorizationPoll, error) {
	options, err := grokOptions(ctx)
	if err != nil {
		return DeviceAuthorizationPoll{}, err
	}
	value, err := cpaembedded.PollGrokDeviceAuthorizationOnce(ctx, stateToBridge(state), options)
	if err != nil {
		return DeviceAuthorizationPoll{}, normalizeError(err)
	}
	return DeviceAuthorizationPoll{
		Status: DeviceAuthorizationStatus(value.Status), State: stateFromBridge(value.State),
		Credential: credentialFromBridge(value.Credential), PollInterval: value.PollInterval,
	}, nil
}

func RefreshCredentialOnce(ctx context.Context, current Credential) (Credential, error) {
	options, err := grokOptions(ctx)
	if err != nil {
		return Credential{}, err
	}
	value, err := cpaembedded.RefreshGrokCredentialOnce(ctx, credentialToBridge(current), options)
	if err != nil {
		return Credential{}, normalizeError(err)
	}
	return credentialFromBridge(value), nil
}

func ImportCredential(ctx context.Context, raw []byte) (Credential, error) {
	options, err := grokOptions(ctx)
	if err != nil {
		return Credential{}, err
	}
	value, err := cpaembedded.ImportGrokCredential(ctx, raw, options)
	if err != nil {
		return Credential{}, normalizeError(err)
	}
	return credentialFromBridge(value), nil
}

func ListModels(ctx context.Context, credential Credential) ([]string, error) {
	options, err := grokOptions(ctx)
	if err != nil {
		return nil, err
	}
	values, err := cpaembedded.DiscoverGrokModels(ctx, credentialToBridge(credential), options)
	if err != nil {
		return nil, normalizeError(err)
	}
	return append([]string(nil), values...), nil
}

func ObserveAccount(ctx context.Context, credential Credential) (AccountObservation, error) {
	options, err := grokOptions(ctx)
	if err != nil {
		return AccountObservation{}, err
	}
	value, err := cpaembedded.ObserveGrokAccount(ctx, credentialToBridge(credential), options)
	if err != nil {
		return AccountObservation{}, normalizeError(err)
	}
	products := make([]ProductUsage, 0, len(value.Billing.ProductUsage))
	for _, product := range value.Billing.ProductUsage {
		products = append(products, ProductUsage{Product: product.Product, UsagePercent: product.UsagePercent})
	}
	return AccountObservation{
		Billing: BillingObservation{
			PeriodType: value.Billing.PeriodType, PeriodStart: value.Billing.PeriodStart,
			PeriodEnd: value.Billing.PeriodEnd, UsagePercent: value.Billing.UsagePercent,
			ProductUsage: products, MonthlyLimitCents: value.Billing.MonthlyLimitCents,
			UsedCents: value.Billing.UsedCents, OnDemandCapCents: value.Billing.OnDemandCapCents,
			OnDemandUsedCents:  value.Billing.OnDemandUsedCents,
			BillingPeriodStart: value.Billing.BillingPeriodStart,
			BillingPeriodEnd:   value.Billing.BillingPeriodEnd,
		},
		Tier: value.Tier, Header: value.Header.Clone(), AccountObserved: value.AccountObserved,
		AccountQuotaObserved: value.AccountQuotaObserved,
		SurfaceQuotaObserved: value.SurfaceQuotaObserved,
		CreditQuotaObserved:  value.CreditQuotaObserved,
		IncompleteSources:    append([]string(nil), value.IncompleteSources...),
	}, nil
}

func grokOptions(ctx context.Context) (cpaembedded.GrokOptions, error) {
	client, err := subscriptionruntime.HTTPClient(ctx)
	if err != nil {
		return cpaembedded.GrokOptions{}, err
	}
	return cpaembedded.GrokOptions{HTTPClient: client}, nil
}

func IsDefinitiveRefreshRejection(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "invalid_grant", "refresh_token_expired", "refresh_token_revoked", "access_denied":
		return true
	default:
		return false
	}
}

func credentialFromBridge(value cpaembedded.GrokCredential) Credential {
	return Credential{
		Type: value.Type, AccessToken: value.AccessToken, RefreshToken: value.RefreshToken,
		IDToken: value.IDToken, TokenType: value.TokenType, ExpiresIn: value.ExpiresIn,
		Expire: value.Expire, LastRefresh: value.LastRefresh, AccountID: value.AccountID,
		Email: value.Email, TokenEndpoint: value.TokenEndpoint,
	}
}

func credentialToBridge(value Credential) cpaembedded.GrokCredential {
	return cpaembedded.GrokCredential{
		Type: value.Type, AccessToken: value.AccessToken, RefreshToken: value.RefreshToken,
		IDToken: value.IDToken, TokenType: value.TokenType, ExpiresIn: value.ExpiresIn,
		Expire: value.Expire, LastRefresh: value.LastRefresh, AccountID: value.AccountID,
		Email: value.Email, TokenEndpoint: value.TokenEndpoint,
	}
}

func stateFromBridge(value cpaembedded.GrokDeviceState) DeviceAuthorizationState {
	return DeviceAuthorizationState{
		DeviceCode: value.DeviceCode, TokenEndpoint: value.TokenEndpoint,
		UserInfoEndpoint: value.UserInfoEndpoint, PollIntervalSeconds: value.PollIntervalSeconds,
	}
}

func stateToBridge(value DeviceAuthorizationState) cpaembedded.GrokDeviceState {
	return cpaembedded.GrokDeviceState{
		DeviceCode: value.DeviceCode, TokenEndpoint: value.TokenEndpoint,
		UserInfoEndpoint: value.UserInfoEndpoint, PollIntervalSeconds: value.PollIntervalSeconds,
	}
}

func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, cpaembedded.ErrGrokCredentialIdentityChanged) {
		return ErrCredentialIdentityChanged
	}
	var tokenErr *cpaembedded.GrokTokenEndpointError
	if errors.As(err, &tokenErr) {
		return &TokenEndpointError{StatusCode: tokenErr.StatusCode, Code: strings.TrimSpace(tokenErr.Code)}
	}
	var upstream *cpaembedded.GrokUpstreamHTTPError
	if errors.As(err, &upstream) {
		return &UpstreamHTTPError{Operation: strings.TrimSpace(upstream.Operation), StatusCode: upstream.StatusCode}
	}
	if errors.Is(err, cpaembedded.ErrGrokAccountObservationPayloadInvalid) {
		return ErrAccountObservationPayloadInvalid
	}
	if errors.Is(err, cpaembedded.ErrGrokAccountObservationUnavailable) {
		return ErrAccountObservationUnavailable
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
		strings.TrimSpace(value.UserInfoEndpoint) == "" || value.PollIntervalSeconds < 1 || value.PollIntervalSeconds > 60 {
		return DeviceAuthorizationState{}, errors.New("invalid Grok device authorization state")
	}
	return value, nil
}
