package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	metaauth "github.com/router-for-me/CLIProxyAPI/v8/internal/auth/meta"
)

const ProviderMuse = "muse"

var ErrMuseCredentialIdentityChanged = errors.New("refreshed Muse Code account identity changed")

// MuseCredential 仅持有已核实的订阅身份；DCA 有效期与请求密钥有效期不同。
type MuseCredential struct {
	Type        string `json:"type"`
	AccessToken string `json:"access_token"`
	DCAToken    string `json:"dca_token"`
	DCAExpired  string `json:"dca_expired,omitempty"`
	Email       string `json:"email"`
	PlanType    string `json:"plan_type,omitempty"`
	LastRefresh string `json:"last_refresh,omitempty"`
}

func (c MuseCredential) SecretValues() []string {
	return []string{c.AccessToken, c.DCAToken, c.Email}
}

// MuseOptions 的端点覆盖仅用于隔离测试；生产认证固定走官方端点。
type MuseOptions struct {
	HTTPClient *http.Client
	DeviceURL  string
	TokenURL   string
	MintURL    string
	Now        func() time.Time
}

type MuseDeviceState struct {
	DeviceCode   string `json:"device_code"`
	PollInterval int    `json:"poll_interval_seconds"`
	// 已取得的 DCA 令牌随加密 Stage 保存，兑换失败后不再次消费设备码。
	DCAToken   string `json:"dca_token,omitempty"`
	DCAExpired string `json:"dca_expired,omitempty"`
	// Stage 单次轮询间隔最多一分钟，较长的 Retry-After 需随加密状态保存。
	MintRetryAtMS int64 `json:"mint_retry_at_ms,omitempty"`
}

type MuseDeviceAuthorization struct {
	VerificationURL string
	UserCode        string
	State           MuseDeviceState
	ExpiresAt       time.Time
	PollInterval    time.Duration
}

type MuseDevicePoll struct {
	Status       string
	State        MuseDeviceState
	Credential   MuseCredential
	PollInterval time.Duration
}

type MuseHTTPError struct {
	StatusCode int
	Code       string
	RetryAfter time.Duration
}

func (e *MuseHTTPError) Error() string {
	return fmt.Sprintf("Muse Code endpoint returned status %d", e.StatusCode)
}
func (e *MuseHTTPError) HTTPStatusCode() int { return e.StatusCode }

func ParseMuseCredentialJSON(raw []byte) (MuseCredential, error) {
	if len(raw) == 0 || len(raw) > maxCredentialBytes {
		return MuseCredential{}, errors.New("invalid Muse Code credential size")
	}
	allowed := map[string]struct{}{"type": {}, "access_token": {}, "dca_token": {}, "dca_expired": {}, "email": {}, "plan_type": {}, "last_refresh": {}}
	if err := validateClaudeCredentialObject(raw, allowed); err != nil {
		return MuseCredential{}, errors.New("invalid Muse Code credential fields")
	}
	var c MuseCredential
	if err := json.Unmarshal(raw, &c); err != nil {
		return MuseCredential{}, errors.New("invalid Muse Code credential JSON")
	}
	c.Type = strings.TrimSpace(c.Type)
	c.Email = strings.ToLower(strings.TrimSpace(c.Email))
	c.AccessToken = strings.TrimSpace(c.AccessToken)
	c.DCAToken = strings.TrimSpace(c.DCAToken)
	if c.Type != ProviderMuse || c.AccessToken == "" || strings.HasPrefix(c.AccessToken, "dca:") ||
		!strings.HasPrefix(c.DCAToken, "dca:") || len(c.DCAToken) <= 4 || c.Email == "" {
		return MuseCredential{}, errors.New("incomplete Muse Code subscription credential")
	}
	for _, value := range []string{c.AccessToken, c.DCAToken, c.Email, c.PlanType} {
		if strings.ContainsAny(value, "\r\n\x00") {
			return MuseCredential{}, errors.New("invalid Muse Code credential value")
		}
	}
	if len(c.Email) > 2048 || len(c.PlanType) > 256 {
		return MuseCredential{}, errors.New("invalid Muse Code account metadata")
	}
	for _, value := range []string{c.DCAExpired, c.LastRefresh} {
		if value != "" {
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				return MuseCredential{}, errors.New("invalid Muse Code credential timestamp")
			}
		}
	}
	return c, nil
}

func MarshalMuseCredential(c MuseCredential) ([]byte, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	parsed, err := ParseMuseCredentialJSON(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(parsed)
}

func validateMuseCredential(c MuseCredential) error {
	raw, err := MarshalMuseCredential(c)
	clear(raw)
	return err
}

func BeginMuseDeviceAuthorization(ctx context.Context, options MuseOptions) (MuseDeviceAuthorization, error) {
	endpoint := museEndpoint(options.DeviceURL, metaauth.DeviceAuthorizationEndpoint)
	body, _, err := museHTTPRequest(ctx, options, http.MethodPost, endpoint, "application/x-www-form-urlencoded", "", []byte(url.Values{"client_id": {metaauth.ClientID}}.Encode()))
	if err != nil {
		return MuseDeviceAuthorization{}, err
	}
	defer clear(body)
	var value metaauth.DeviceCodeResponse
	if json.Unmarshal(body, &value) != nil {
		return MuseDeviceAuthorization{}, errors.New("invalid Muse Code device response")
	}
	verificationURL := value.VerificationURIComplete
	if verificationURL == "" {
		verificationURL = value.VerificationURI
	}
	parsed, err := url.Parse(verificationURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil ||
		strings.TrimSpace(value.DeviceCode) == "" || strings.TrimSpace(value.UserCode) == "" || value.ExpiresIn <= 0 || value.ExpiresIn > 86400 {
		return MuseDeviceAuthorization{}, errors.New("incomplete Muse Code device response")
	}
	interval := max(value.Interval, 5)
	if interval > 60 {
		return MuseDeviceAuthorization{}, errors.New("invalid Muse Code polling interval")
	}
	return MuseDeviceAuthorization{
		VerificationURL: verificationURL, UserCode: value.UserCode,
		State:        MuseDeviceState{DeviceCode: value.DeviceCode, PollInterval: interval},
		ExpiresAt:    museNow(options).Add(min(time.Duration(value.ExpiresIn)*time.Second, metaauth.MaxPollDuration)),
		PollInterval: time.Duration(interval) * time.Second,
	}, nil
}

func PollMuseDeviceAuthorizationOnce(ctx context.Context, state MuseDeviceState, options MuseOptions) (MuseDevicePoll, error) {
	if state.DeviceCode == "" || state.PollInterval < 5 || state.PollInterval > 60 {
		return MuseDevicePoll{}, errors.New("invalid Muse Code device state")
	}
	result := MuseDevicePoll{Status: "pending", State: state, PollInterval: time.Duration(state.PollInterval) * time.Second}
	if state.DCAToken == "" {
		form := url.Values{"client_id": {metaauth.ClientID}, "device_code": {state.DeviceCode}, "grant_type": {metaauth.DeviceCodeGrantType}}
		body, _, err := museHTTPRequest(ctx, options, http.MethodPost, museEndpoint(options.TokenURL, metaauth.TokenEndpoint), "application/x-www-form-urlencoded", "", []byte(form.Encode()))
		if err != nil {
			var response *MuseHTTPError
			if !errors.As(err, &response) {
				return MuseDevicePoll{}, err
			}
			switch response.Code {
			case "authorization_pending":
				return result, nil
			case "slow_down":
				result.State.PollInterval = min(state.PollInterval+5, 60)
				result.PollInterval = time.Duration(result.State.PollInterval) * time.Second
				return result, nil
			case "access_denied":
				result.Status = "denied"
				return result, nil
			case "expired_token":
				result.Status = "expired"
				return result, nil
			default:
				return MuseDevicePoll{}, err
			}
		}
		defer clear(body)
		var token metaauth.TokenData
		if json.Unmarshal(body, &token) != nil || !strings.HasPrefix(token.AccessToken, "dca:") || len(token.AccessToken) <= 4 {
			return MuseDevicePoll{}, errors.New("invalid Muse Code token response")
		}
		result.State.DCAToken = token.AccessToken
		if token.ExpiresAt > 0 && token.ExpiresAt <= 253402300799 {
			result.State.DCAExpired = time.Unix(token.ExpiresAt, 0).UTC().Format(time.RFC3339)
		} else if token.ExpiresIn > 0 && token.ExpiresIn <= 366*86400 {
			result.State.DCAExpired = museNow(options).Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
		}
		// 单次轮询只执行一次认证动作，让 Stage 先可靠保存兑换所需的 DCA。
		return result, nil
	}
	if remaining := time.UnixMilli(state.MintRetryAtMS).Sub(museNow(options)); remaining > 0 {
		result.PollInterval = min(max(remaining, 5*time.Second), time.Minute)
		return result, nil
	}
	result.State.MintRetryAtMS = 0
	credential, err := mintMuseCredential(ctx, MuseCredential{DCAToken: state.DCAToken, DCAExpired: state.DCAExpired}, options)
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return MuseDevicePoll{}, err
		}
		if delay, retryable := museDeviceMintRetryDelay(err, state.PollInterval); retryable {
			result.PollInterval = min(delay, time.Minute)
			result.State.PollInterval = int((result.PollInterval + time.Second - 1) / time.Second)
			if delay > time.Minute {
				result.State.MintRetryAtMS = museNow(options).Add(delay).UnixMilli()
			}
			// 已保存的 DCA 可再次兑换；返回 pending 让管理层继续保存并调度。
			return result, nil
		}
		return MuseDevicePoll{}, err
	}
	result.Status, result.Credential = "authorized", credential
	return result, nil
}

func museDeviceMintRetryDelay(err error, interval int) (time.Duration, bool) {
	delay := time.Duration(min(interval*2, 60)) * time.Second
	var response *MuseHTTPError
	if errors.As(err, &response) {
		switch response.Code {
		case "invalid_client", "invalid_grant", "invalid_token", "expired_token", "access_denied", "subscription_inactive":
			return 0, false
		case "temporarily_unavailable", "server_error":
			return max(delay, response.RetryAfter), true
		}
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 && response.StatusCode <= 599 {
			return max(delay, response.RetryAfter), true
		}
		return 0, false
	}
	var networkError net.Error
	retryable := errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) || errors.Is(err, museConnectionReset) ||
		errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary())
	return delay, retryable
}

func ImportMuseCredential(ctx context.Context, raw []byte, options MuseOptions) (MuseCredential, error) {
	if len(raw) == 0 || len(raw) > maxCredentialBytes {
		return MuseCredential{}, errors.New("invalid Muse Code credential size")
	}
	allowed := map[string]struct{}{}
	for _, key := range []string{"type", "auth_kind", "access_token", "api_key", "dca_token", "dca_expired", "dca_expires_at", "email", "name", "subs_tier_name", "subs_tier_id", "is_subs_active", "has_payment_method", "base_url", "token_type", "expired", "expires_in", "last_refresh", "plan_type"} {
		allowed[key] = struct{}{}
	}
	allowCPAAuthFileControlFields(allowed)
	if err := validateClaudeCredentialObject(raw, allowed); err != nil {
		return MuseCredential{}, errors.New("invalid Muse Code import fields")
	}
	if err := validateCPAAuthFileControlMetadata(raw); err != nil {
		return MuseCredential{}, errors.New("invalid Muse Code import metadata")
	}
	var input struct {
		Type         string `json:"type"`
		AuthKind     string `json:"auth_kind"`
		DCAToken     string `json:"dca_token"`
		AccessToken  string `json:"access_token"`
		DCAExpired   string `json:"dca_expired"`
		DCAExpiresAt int64  `json:"dca_expires_at"`
		Email        string `json:"email"`
	}
	if json.Unmarshal(raw, &input) != nil || (input.Type != "meta" && input.Type != ProviderMuse) || (input.AuthKind != "" && input.AuthKind != "oauth") {
		return MuseCredential{}, errors.New("invalid Muse Code subscription import")
	}
	if input.DCAToken == "" && strings.HasPrefix(input.AccessToken, "dca:") {
		input.DCAToken = input.AccessToken
	}
	if input.DCAExpired == "" && input.DCAExpiresAt > 0 && input.DCAExpiresAt <= 253402300799 {
		input.DCAExpired = time.Unix(input.DCAExpiresAt, 0).UTC().Format(time.RFC3339)
	}
	return mintMuseCredential(ctx, MuseCredential{DCAToken: input.DCAToken, DCAExpired: input.DCAExpired, Email: input.Email}, options)
}

func RefreshMuseCredentialOnce(ctx context.Context, current MuseCredential, options MuseOptions) (MuseCredential, error) {
	if err := validateMuseCredential(current); err != nil {
		return MuseCredential{}, err
	}
	return mintMuseCredential(ctx, current, options)
}

func mintMuseCredential(ctx context.Context, current MuseCredential, options MuseOptions) (MuseCredential, error) {
	dca := strings.TrimSpace(current.DCAToken)
	if !strings.HasPrefix(dca, "dca:") || len(dca) <= 4 || strings.ContainsAny(dca, "\r\n\x00") {
		return MuseCredential{}, errors.New("Muse Code DCA token is required")
	}
	payload, err := json.Marshal(map[string]string{"dca_token": dca})
	if err != nil {
		return MuseCredential{}, err
	}
	defer clear(payload)
	body, _, err := museHTTPRequest(ctx, options, http.MethodPost, museEndpoint(options.MintURL, "https://api.meta.ai/muse-code/key"), "application/json", dca, payload)
	if err != nil {
		return MuseCredential{}, err
	}
	defer clear(body)
	var minted metaauth.MintedKeyResponse
	if json.Unmarshal(body, &minted) != nil {
		return MuseCredential{}, errors.New("invalid Muse Code key response")
	}
	if !minted.IsSubsActive {
		return MuseCredential{}, &MuseHTTPError{StatusCode: http.StatusForbidden, Code: "subscription_inactive"}
	}
	email := strings.ToLower(strings.TrimSpace(minted.UserEmail))
	if current.Email != "" && !strings.EqualFold(strings.TrimSpace(current.Email), email) {
		return MuseCredential{}, ErrMuseCredentialIdentityChanged
	}
	credential := MuseCredential{Type: ProviderMuse, AccessToken: minted.APIKey, DCAToken: dca, DCAExpired: current.DCAExpired,
		Email: email, PlanType: strings.TrimSpace(minted.SubsTierName), LastRefresh: museNow(options).Format(time.RFC3339)}
	raw, err := MarshalMuseCredential(credential)
	clear(raw)
	if err != nil {
		return MuseCredential{}, err
	}
	return credential, nil
}

func ListMuseModels(ctx context.Context, credential MuseCredential, apiRoot string, options MuseOptions) ([]string, error) {
	if err := validateMuseCredential(credential); err != nil {
		return nil, err
	}
	endpoint, err := ResolveAPIEndpoint(apiRoot, metaauth.DefaultAPIBaseURL+"/models")
	if err != nil {
		return nil, err
	}
	body, _, err := museHTTPRequest(ctx, options, http.MethodGet, endpoint, "", credential.AccessToken, nil)
	if err != nil {
		return nil, err
	}
	defer clear(body)
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Data == nil {
		return nil, errors.New("invalid Muse Code model list")
	}
	models := make([]string, 0, len(payload.Data))
	for _, model := range payload.Data {
		models = append(models, model.ID)
	}
	return normalizeModelIDs(models), nil
}

func museHTTPRequest(ctx context.Context, options MuseOptions, method, endpoint, contentType, token string, payload []byte) ([]byte, http.Header, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, nil, errors.New("invalid Muse Code request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "muse-code/1.0.2")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := options.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := clientWithoutRedirects(client).Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxTokenResponse+1))
	if err != nil {
		clear(body)
		return nil, nil, err
	}
	if len(body) > maxTokenResponse {
		clear(body)
		return nil, nil, errors.New("Muse Code response is too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error json.RawMessage `json:"error"`
		}
		code := ""
		if json.Unmarshal(body, &failure) == nil {
			_ = json.Unmarshal(failure.Error, &code)
		}
		clear(body)
		switch code {
		case "authorization_pending", "slow_down", "access_denied", "expired_token", "invalid_grant", "invalid_token", "invalid_client", "temporarily_unavailable", "server_error":
		default:
			code = ""
		}
		return nil, response.Header.Clone(), &MuseHTTPError{StatusCode: response.StatusCode, Code: code, RetryAfter: boundedOAuthRetryAfter(response.Header, museNow(options))}
	}
	return body, response.Header.Clone(), nil
}

func museEndpoint(override, fallback string) string {
	if override != "" {
		return override
	}
	return fallback
}

func museNow(options MuseOptions) time.Time {
	if options.Now != nil {
		return options.Now().UTC()
	}
	return time.Now().UTC()
}
