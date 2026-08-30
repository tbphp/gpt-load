package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const ProviderGrok = "grok"

var ErrGrokCredentialIdentityChanged = errors.New("refreshed grok credential identity changed")

// GrokCredential is GPT-Load's canonical, execution-only xAI OAuth schema.
type GrokCredential struct {
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

// GrokOptions provides test-only endpoint/clock seams. Production callers use
// zero values so all endpoints come from xAI's fixed issuer discovery.
type GrokOptions struct {
	DiscoveryURL      string
	UserInfoURL       string
	ModelsURL         string
	BillingWeeklyURL  string
	BillingMonthlyURL string
	HTTPClient        *http.Client
	Now               func() time.Time
}

type GrokDeviceState struct {
	DeviceCode          string `json:"device_code"`
	TokenEndpoint       string `json:"token_endpoint"`
	UserInfoEndpoint    string `json:"userinfo_endpoint"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
}

type GrokDeviceAuthorization struct {
	VerificationURL string
	UserCode        string
	State           GrokDeviceState
	ExpiresAt       time.Time
	PollInterval    time.Duration
}

type GrokDeviceStatus string

const (
	GrokDevicePending    GrokDeviceStatus = "pending"
	GrokDeviceAuthorized GrokDeviceStatus = "authorized"
	GrokDeviceDenied     GrokDeviceStatus = "denied"
	GrokDeviceExpired    GrokDeviceStatus = "expired"
)

type GrokDevicePoll struct {
	Status       GrokDeviceStatus
	State        GrokDeviceState
	Credential   GrokCredential
	PollInterval time.Duration
}

type GrokTokenEndpointError struct {
	StatusCode int
	Code       string
	RetryAfter time.Duration
}

func (err *GrokTokenEndpointError) Error() string {
	if err == nil {
		return "Grok token endpoint failed"
	}
	return fmt.Sprintf("Grok token endpoint returned status %d", err.StatusCode)
}

func (err *GrokTokenEndpointError) HTTPStatusCode() int {
	if err == nil {
		return 0
	}
	return err.StatusCode
}

type GrokUpstreamHTTPError struct {
	Operation  string
	StatusCode int
}

func (err *GrokUpstreamHTTPError) Error() string {
	if err == nil {
		return "Grok upstream request failed"
	}
	return fmt.Sprintf("Grok %s endpoint returned status %d", err.Operation, err.StatusCode)
}

func (err *GrokUpstreamHTTPError) HTTPStatusCode() int {
	if err == nil {
		return 0
	}
	return err.StatusCode
}

func ParseGrokCredentialJSON(raw []byte) (GrokCredential, error) {
	if len(raw) == 0 || len(raw) > maxCredentialBytes {
		return GrokCredential{}, fmt.Errorf("credential JSON size is invalid")
	}
	allowed := grokCanonicalFields()
	allowCPAAuthFileControlFields(allowed)
	if err := validateClaudeCredentialObject(raw, allowed); err != nil {
		return GrokCredential{}, err
	}
	if err := validateCPAAuthFileControlMetadata(raw); err != nil {
		return GrokCredential{}, err
	}
	var credential GrokCredential
	if err := json.Unmarshal(raw, &credential); err != nil {
		return GrokCredential{}, fmt.Errorf("decode credential: %w", err)
	}
	normalizeGrokCredential(&credential)
	if err := validateGrokCredential(credential); err != nil {
		return GrokCredential{}, err
	}
	return credential, nil
}

func MarshalGrokCredential(credential GrokCredential) ([]byte, error) {
	raw, err := json.Marshal(credential)
	if err != nil {
		return nil, err
	}
	parsed, err := ParseGrokCredentialJSON(raw)
	clear(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(parsed)
}

func grokCanonicalFields() map[string]struct{} {
	return map[string]struct{}{
		"type": {}, "access_token": {}, "refresh_token": {}, "id_token": {},
		"token_type": {}, "expires_in": {}, "expired": {}, "last_refresh": {},
		"account_id": {}, "email": {}, "token_endpoint": {},
	}
}

func normalizeGrokCredential(credential *GrokCredential) {
	credential.Type = strings.ToLower(strings.TrimSpace(credential.Type))
	credential.AccessToken = strings.TrimSpace(credential.AccessToken)
	credential.RefreshToken = strings.TrimSpace(credential.RefreshToken)
	credential.IDToken = strings.TrimSpace(credential.IDToken)
	credential.TokenType = strings.TrimSpace(credential.TokenType)
	if credential.TokenType == "" {
		credential.TokenType = "Bearer"
	}
	credential.Expire = strings.TrimSpace(credential.Expire)
	credential.LastRefresh = strings.TrimSpace(credential.LastRefresh)
	credential.AccountID = strings.TrimSpace(credential.AccountID)
	credential.Email = strings.TrimSpace(credential.Email)
	credential.TokenEndpoint = strings.TrimSpace(credential.TokenEndpoint)
}

func validateGrokCredential(credential GrokCredential) error {
	return validateGrokCredentialWithOptions(credential, GrokOptions{})
}

func validateGrokCredentialWithOptions(credential GrokCredential, options GrokOptions) error {
	if credential.Type != ProviderGrok {
		return fmt.Errorf("credential type must be grok")
	}
	for field, value := range map[string]string{
		"access_token": credential.AccessToken, "refresh_token": credential.RefreshToken,
		"account_id": credential.AccountID, "email": credential.Email,
		"expired": credential.Expire, "token_endpoint": credential.TokenEndpoint,
	} {
		if value == "" {
			return fmt.Errorf("credential %s is required", field)
		}
	}
	if credential.ExpiresIn < 0 {
		return fmt.Errorf("credential expires_in is invalid")
	}
	if !strings.EqualFold(credential.TokenType, "Bearer") {
		return fmt.Errorf("credential token_type must be Bearer")
	}
	if err := validateTimestamp("expired", credential.Expire); err != nil {
		return err
	}
	if err := validateTimestamp("last_refresh", credential.LastRefresh); err != nil {
		return err
	}
	for field, value := range map[string]string{
		"account_id": credential.AccountID, "email": credential.Email,
		"token_type": credential.TokenType, "token_endpoint": credential.TokenEndpoint,
	} {
		if len(value) > 2048 || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("credential %s is invalid", field)
		}
	}
	if _, err := validateGrokOAuthEndpoint(
		credential.TokenEndpoint,
		"token_endpoint",
		grokAllowsTestEndpoints(options),
	); err != nil {
		return err
	}
	return nil
}

func grokAllowsTestEndpoints(options GrokOptions) bool {
	return strings.TrimSpace(options.DiscoveryURL) != "" ||
		strings.TrimSpace(options.UserInfoURL) != "" ||
		strings.TrimSpace(options.ModelsURL) != "" ||
		strings.TrimSpace(options.BillingWeeklyURL) != "" ||
		strings.TrimSpace(options.BillingMonthlyURL) != "" ||
		options.HTTPClient != nil
}

func GrokCredentialExpiresAt(credential GrokCredential) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(credential.Expire))
	return parsed, err == nil
}

func (credential GrokCredential) SecretValues() []string {
	return []string{
		credential.AccessToken, credential.RefreshToken, credential.IDToken,
		credential.AccountID, credential.Email,
	}
}

func BeginGrokDeviceAuthorization(ctx context.Context, options GrokOptions) (GrokDeviceAuthorization, error) {
	return beginGrokDeviceAuthorization(ctx, options)
}

func PollGrokDeviceAuthorizationOnce(ctx context.Context, state GrokDeviceState, options GrokOptions) (GrokDevicePoll, error) {
	return pollGrokDeviceAuthorizationOnce(ctx, state, options)
}

func ImportGrokCredential(ctx context.Context, raw []byte, options GrokOptions) (GrokCredential, error) {
	return importGrokCredential(ctx, raw, options)
}

func RefreshGrokCredentialOnce(ctx context.Context, current GrokCredential, options GrokOptions) (GrokCredential, error) {
	return refreshGrokCredentialOnce(ctx, current, options)
}

func grokNow(options GrokOptions) time.Time {
	if options.Now != nil {
		return options.Now().UTC()
	}
	return time.Now().UTC()
}

func grokHTTPClient(options GrokOptions) *http.Client {
	if options.HTTPClient != nil {
		return clientWithoutRedirects(options.HTTPClient)
	}
	return clientWithoutRedirects(http.DefaultClient)
}

func decodeGrokDeviceState(raw []byte) (GrokDeviceState, error) {
	var state GrokDeviceState
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return GrokDeviceState{}, err
	}
	if strings.TrimSpace(state.DeviceCode) == "" || strings.TrimSpace(state.TokenEndpoint) == "" ||
		strings.TrimSpace(state.UserInfoEndpoint) == "" || state.PollIntervalSeconds < 1 || state.PollIntervalSeconds > 60 {
		return GrokDeviceState{}, fmt.Errorf("Grok device authorization state is invalid")
	}
	return state, nil
}
