package embedded

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ProviderKiro identifies the Kiro subscription provider in the embedded CPA
// bridge. Kiro (kiro.dev) is an Amazon Q Developer / AWS CodeWhisperer based
// agent offering Claude-family and GPT-family models under a subscription.
const ProviderKiro = "kiro"

// KiroAuthKind enumerates the supported Kiro credential kinds.
type KiroAuthKind string

const (
	KiroAuthAPIKey KiroAuthKind = "api_key"
	KiroAuthSocial KiroAuthKind = "social"
	KiroAuthOIDC   KiroAuthKind = "oidc"
)

var (
	// ErrKiroCredentialIdentityChanged indicates a refreshed Kiro credential
	// no longer belongs to the same account.
	ErrKiroCredentialIdentityChanged = errors.New("refreshed kiro credential identity changed")
	// ErrKiroAccountObservationUnavailable indicates Kiro account observation is unavailable.
	ErrKiroAccountObservationUnavailable = errors.New("Kiro account observation is unavailable")
	// ErrKiroAccountObservationPayloadInvalid indicates a malformed Kiro account payload.
	ErrKiroAccountObservationPayloadInvalid = errors.New("Kiro account observation payload is invalid")
	// ErrKiroModelDiscoveryUnavailable indicates model discovery returned no catalog.
	ErrKiroModelDiscoveryUnavailable = errors.New("Kiro model discovery is unavailable")
)

// DefaultKiroRegion is the Kiro API region used when none is derivable.
const DefaultKiroRegion = "us-east-1"

// KiroCredential is GPT-Load's canonical, execution-only Kiro credential
// schema. It supports both API-key and OAuth (social / OIDC) token kinds.
type KiroCredential struct {
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
	// KnownLocally is set by self-exploration when the credential was discovered
	// on disk (e.g. the Kiro desktop / AWS SSO token cache) rather than imported
	// or obtained through device authorization. It is not serialized.
	KnownLocally bool `json:"-"`
}

// KiroOptions provides test-only endpoint/clock seams. Production callers use
// zero values so all endpoints come from fixed Kiro service discovery.
type KiroOptions struct {
	Region         string
	RuntimeHost    string
	ManagementHost string
	AuthHost       string
	HTTPClient     *http.Client
	Now            func() time.Time
}

// KiroDeviceState is the persisted client-side device authorization state.
type KiroDeviceState struct {
	DeviceCode          string `json:"device_code"`
	TokenEndpoint       string `json:"token_endpoint"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	LoginProvider       string `json:"login_provider,omitempty"`
}

// KiroDeviceAuthorization is the result of beginning device authorization.
type KiroDeviceAuthorization struct {
	VerificationURL string
	UserCode        string
	State           KiroDeviceState
	ExpiresAt       time.Time
	PollInterval    time.Duration
}

// KiroDeviceStatus is the status of a device authorization poll.
type KiroDeviceStatus string

const (
	KiroDevicePending    KiroDeviceStatus = "pending"
	KiroDeviceAuthorized KiroDeviceStatus = "authorized"
	KiroDeviceDenied     KiroDeviceStatus = "denied"
	KiroDeviceExpired    KiroDeviceStatus = "expired"
)

// KiroDevicePoll is the result of one device authorization poll.
type KiroDevicePoll struct {
	Status       KiroDeviceStatus
	State        KiroDeviceState
	Credential   KiroCredential
	PollInterval time.Duration
}

// KiroTokenEndpointError describes a failed Kiro OAuth token endpoint call.
type KiroTokenEndpointError struct {
	StatusCode int
	Code       string
	RetryAfter time.Duration
}

func (err *KiroTokenEndpointError) Error() string {
	if err == nil {
		return "Kiro token endpoint failed"
	}
	return fmt.Sprintf("Kiro token endpoint returned status %d", err.StatusCode)
}

func (err *KiroTokenEndpointError) HTTPStatusCode() int {
	if err == nil {
		return 0
	}
	return err.StatusCode
}

// KiroUpstreamHTTPError describes a failed Kiro upstream request.
type KiroUpstreamHTTPError struct {
	Operation  string
	StatusCode int
}

func (err *KiroUpstreamHTTPError) Error() string {
	if err == nil {
		return "Kiro upstream request failed"
	}
	return fmt.Sprintf("Kiro %s endpoint returned status %d", err.Operation, err.StatusCode)
}

func (err *KiroUpstreamHTTPError) HTTPStatusCode() int {
	if err == nil {
		return 0
	}
	return err.StatusCode
}

// ParseKiroCredentialJSON decodes and validates a Kiro credential.
func ParseKiroCredentialJSON(raw []byte) (KiroCredential, error) {
	if len(raw) == 0 || len(raw) > maxCredentialBytes {
		return KiroCredential{}, fmt.Errorf("credential JSON size is invalid")
	}
	allowed := kiroCanonicalFields()
	allowCPAAuthFileControlFields(allowed)
	if err := validateClaudeCredentialObject(raw, allowed); err != nil {
		return KiroCredential{}, err
	}
	if err := validateCPAAuthFileControlMetadata(raw); err != nil {
		return KiroCredential{}, err
	}
	var credential KiroCredential
	if err := json.Unmarshal(raw, &credential); err != nil {
		return KiroCredential{}, fmt.Errorf("decode credential: %w", err)
	}
	normalizeKiroCredential(&credential)
	if err := validateKiroCredential(credential); err != nil {
		return KiroCredential{}, err
	}
	return credential, nil
}

// MarshalKiroCredential encodes a Kiro credential to its canonical JSON.
func MarshalKiroCredential(credential KiroCredential) ([]byte, error) {
	raw, err := json.Marshal(credential)
	if err != nil {
		return nil, err
	}
	parsed, err := ParseKiroCredentialJSON(raw)
	clear(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(parsed)
}

func kiroCanonicalFields() map[string]struct{} {
	return map[string]struct{}{
		"type": {}, "auth_kind": {}, "access_token": {}, "refresh_token": {},
		"token_type": {}, "expires_in": {}, "expired": {}, "last_refresh": {},
		"account_id": {}, "email": {}, "region": {}, "profile_arn": {}, "token_endpoint": {},
	}
}

func normalizeKiroCredential(credential *KiroCredential) {
	credential.Type = strings.ToLower(strings.TrimSpace(credential.Type))
	credential.AuthKind = strings.ToLower(strings.TrimSpace(credential.AuthKind))
	if credential.AuthKind == "" {
		credential.AuthKind = string(KiroAuthSocial)
	}
	credential.AccessToken = strings.TrimSpace(credential.AccessToken)
	credential.RefreshToken = strings.TrimSpace(credential.RefreshToken)
	credential.TokenType = strings.TrimSpace(credential.TokenType)
	if credential.TokenType == "" {
		credential.TokenType = "Bearer"
	}
	credential.Expire = strings.TrimSpace(credential.Expire)
	credential.LastRefresh = strings.TrimSpace(credential.LastRefresh)
	credential.AccountID = strings.TrimSpace(credential.AccountID)
	credential.Email = strings.TrimSpace(credential.Email)
	credential.Region = strings.TrimSpace(credential.Region)
	credential.ProfileARN = strings.TrimSpace(credential.ProfileARN)
	credential.TokenEndpoint = strings.TrimSpace(credential.TokenEndpoint)
	if credential.Region == "" {
		credential.Region = DefaultKiroRegion
	}
}

func validateKiroCredential(credential KiroCredential) error {
	return validateKiroCredentialWithOptions(credential, KiroOptions{})
}

func validateKiroCredentialWithOptions(credential KiroCredential, options KiroOptions) error {
	if credential.Type != ProviderKiro {
		return fmt.Errorf("credential type must be kiro")
	}
	kind := KiroAuthKind(credential.AuthKind)
	switch kind {
	case KiroAuthAPIKey:
		if strings.TrimSpace(credential.AccessToken) == "" {
			return fmt.Errorf("credential access_token is required")
		}
	case KiroAuthSocial, KiroAuthOIDC:
		if strings.TrimSpace(credential.AccessToken) == "" || strings.TrimSpace(credential.RefreshToken) == "" {
			return fmt.Errorf("credential tokens are required")
		}
		// profile_arn is intentionally optional: Kiro authenticates with the
		// bearer access token, and a self-discovered account (see kiro_discovery)
		// does not persist profileArn on disk. Discovery/model endpoints that need
		// a profileArn enforce it themselves.
		if err := validateTimestamp("expired", credential.Expire); err != nil {
			return err
		}
	default:
		return fmt.Errorf("credential auth_kind is invalid")
	}
	if credential.ExpiresIn < 0 {
		return fmt.Errorf("credential expires_in is invalid")
	}
	if !strings.EqualFold(credential.TokenType, "Bearer") {
		return fmt.Errorf("credential token_type must be Bearer")
	}
	if len(credential.AccessToken) > 16384 || strings.ContainsAny(credential.AccessToken, "\r\n\x00") {
		return fmt.Errorf("credential access_token is invalid")
	}
	for field, value := range map[string]string{
		"account_id": credential.AccountID, "email": credential.Email,
		"region": credential.Region, "profile_arn": credential.ProfileARN,
		"token_endpoint": credential.TokenEndpoint,
	} {
		if len(value) > 2048 || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("credential %s is invalid", field)
		}
	}
	region := strings.TrimSpace(credential.Region)
	if region == "" {
		region = DefaultKiroRegion
	}
	if _, err := validateKiroRegion(region); err != nil {
		return err
	}
	if credentials, ok := kiroCredentialRegion(options); ok {
		_ = credentials
	}
	return nil
}

func validateKiroRegion(region string) (string, error) {
	region = strings.TrimSpace(region)
	if region == "" {
		return "", fmt.Errorf("Kiro region is required")
	}
	matched := true
	if len(region) > 64 {
		matched = false
	}
	for _, r := range region {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			matched = false
		}
	}
	if !matched {
		return "", fmt.Errorf("Kiro region is invalid")
	}
	return region, nil
}

func kiroCredentialRegion(options KiroOptions) (string, bool) {
	region, err := validateKiroRegion(options.Region)
	return region, err == nil
}

// KiroCredentialExpiresAt returns the credential's expiry, if known.
func KiroCredentialExpiresAt(credential KiroCredential) (time.Time, bool) {
	if strings.TrimSpace(credential.Expire) == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(credential.Expire))
	return parsed, err == nil
}

// SecretValues returns the fields that must be redacted in logs.
func (credential KiroCredential) SecretValues() []string {
	return []string{
		credential.AccessToken, credential.RefreshToken, credential.AccountID,
		credential.Email, credential.ProfileARN,
	}
}

// kiroNow returns the current UTC time, honoring the options clock seam.
func kiroNow(options KiroOptions) time.Time {
	if options.Now != nil {
		return options.Now().UTC()
	}
	return time.Now().UTC()
}

// kiroHTTPClient returns the option-aware HTTP client without redirects.
func kiroHTTPClient(options KiroOptions) *http.Client {
	if options.HTTPClient != nil {
		return clientWithoutRedirects(options.HTTPClient)
	}
	return clientWithoutRedirects(http.DefaultClient)
}

// KiroRuntimeURL builds the Kiro runtime host for a given region.
func KiroRuntimeURL(region string) (string, error) {
	region, err := validateKiroRegion(region)
	if err != nil {
		return "", err
	}
	return "https://runtime." + region + ".kiro.dev/", nil
}

// KiroManagementURL builds the Kiro management host for a given region.
func KiroManagementURL(region string) (string, error) {
	region, err := validateKiroRegion(region)
	if err != nil {
		return "", err
	}
	return "https://management." + region + ".kiro.dev/", nil
}

// KiroAuthURL builds the Kiro social auth host for a given region.
func KiroAuthURL(region string) (string, error) {
	region, err := validateKiroRegion(region)
	if err != nil {
		return "", err
	}
	return "https://prod." + region + ".auth.desktop.kiro.dev/", nil
}

func decodeKiroDeviceState(raw []byte) (KiroDeviceState, error) {
	var state KiroDeviceState
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return KiroDeviceState{}, err
	}
	if strings.TrimSpace(state.DeviceCode) == "" || strings.TrimSpace(state.TokenEndpoint) == "" ||
		state.PollIntervalSeconds < 1 || state.PollIntervalSeconds > 60 {
		return KiroDeviceState{}, fmt.Errorf("Kiro device authorization state is invalid")
	}
	return state, nil
}
