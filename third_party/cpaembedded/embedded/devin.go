package embedded

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	devinauth "github.com/router-for-me/CLIProxyAPI/v8/internal/auth/devin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"google.golang.org/protobuf/encoding/protowire"
)

const ProviderDevin = "devin"
const DevinRedirectURI = "http://127.0.0.1:17326/callback"

var ErrDevinCredentialIdentityChanged = errors.New("Devin account identity changed")

type DevinCredential struct {
	Type         string `json:"type"`
	SessionToken string `json:"session_token"`
	UserID       string `json:"user_id"`
	OrgID        string `json:"org_id,omitempty"`
	Email        string `json:"email,omitempty"`
	UserName     string `json:"user_name,omitempty"`
	Plan         string `json:"plan,omitempty"`
	DeviceSeed   string `json:"device_seed,omitempty"`
	LastRefresh  string `json:"last_refresh,omitempty"`
}

// 端点覆盖只供隔离测试；生产授权不采用导入文件或分组的地址。
type DevinOptions struct {
	HTTPClient *http.Client
	TokenURL   string
	StatusURL  string
	Now        func() time.Time
}

type DevinAccountObservation struct {
	Credential      DevinCredential
	DailyRemaining  *int64
	WeeklyRemaining *int64
	DailyResetAt    time.Time
	WeeklyResetAt   time.Time
	PlanStart       time.Time
	PlanEnd         time.Time
}

type DevinHTTPError struct {
	StatusCode int
	RetryAfter time.Duration
}

func (e *DevinHTTPError) Error() string {
	return fmt.Sprintf("Devin endpoint returned status %d", e.StatusCode)
}
func (e *DevinHTTPError) HTTPStatusCode() int { return e.StatusCode }

func devinCanonicalFields() map[string]struct{} {
	return map[string]struct{}{"type": {}, "session_token": {}, "user_id": {}, "org_id": {}, "email": {}, "user_name": {}, "plan": {}, "device_seed": {}, "last_refresh": {}}
}

func ParseDevinCredentialJSON(raw []byte) (DevinCredential, error) {
	if len(raw) == 0 || len(raw) > maxCredentialBytes {
		return DevinCredential{}, errors.New("invalid Devin credential size")
	}
	if err := validateClaudeCredentialObject(raw, devinCanonicalFields()); err != nil {
		return DevinCredential{}, errors.New("invalid Devin credential fields")
	}
	var value DevinCredential
	if err := json.Unmarshal(raw, &value); err != nil {
		return DevinCredential{}, errors.New("invalid Devin credential JSON")
	}
	normalizeDevinCredential(&value)
	if err := validateDevinCredential(value); err != nil {
		return DevinCredential{}, err
	}
	return value, nil
}

func normalizeDevinCredential(value *DevinCredential) {
	value.Type = strings.TrimSpace(value.Type)
	value.SessionToken = devinauth.FormatSessionToken(value.SessionToken)
	for _, field := range []*string{&value.UserID, &value.OrgID, &value.Email, &value.UserName, &value.Plan, &value.DeviceSeed, &value.LastRefresh} {
		*field = strings.TrimSpace(*field)
	}
}

func validateDevinCredential(value DevinCredential) error {
	if value.Type != ProviderDevin || value.SessionToken == "" || value.UserID == "" {
		return errors.New("Devin credential requires type, session_token and verified user_id")
	}
	for _, field := range []string{value.SessionToken, value.UserID, value.OrgID, value.Email, value.UserName, value.Plan, value.DeviceSeed} {
		if len(field) > 8192 || strings.ContainsAny(field, "\r\n\x00") {
			return errors.New("invalid Devin credential value")
		}
	}
	return validateTimestamp("last_refresh", value.LastRefresh)
}

func MarshalDevinCredential(value DevinCredential) ([]byte, error) {
	normalizeDevinCredential(&value)
	if err := validateDevinCredential(value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func (value DevinCredential) SecretValues() []string {
	return []string{value.SessionToken, strings.TrimPrefix(value.SessionToken, "devin-session-token$"), value.UserID, value.OrgID, value.Email, value.UserName, value.DeviceSeed}
}

func BeginDevinBrowserAuthorization() (BrowserAuthorization, error) {
	pkce, err := devinauth.GeneratePKCECodes()
	if err != nil {
		return BrowserAuthorization{}, err
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return BrowserAuthorization{}, err
	}
	state := base64.RawURLEncoding.EncodeToString(nonce[:])
	service := devinauth.NewDevinAuthService(nil)
	return BrowserAuthorization{AuthorizationURL: service.BuildAuthorizationURL(DevinRedirectURI, pkce.CodeChallenge, state), State: state, CodeVerifier: pkce.CodeVerifier, CodeChallenge: pkce.CodeChallenge, ExpiresAt: time.Now().Add(10 * time.Minute)}, nil
}

func CompleteDevinBrowserAuthorization(ctx context.Context, completion BrowserAuthorizationCompletion, options DevinOptions) (DevinCredential, error) {
	if completion.ExpectedState == "" || !constantTimeEqual(completion.ExpectedState, completion.ReturnedState) || strings.TrimSpace(completion.Code) == "" || completion.CodeVerifier == "" {
		return DevinCredential{}, errors.New("invalid Devin authorization state")
	}
	body, err := json.Marshal(map[string]string{"code": completion.Code, "code_verifier": completion.CodeVerifier})
	if err != nil {
		return DevinCredential{}, err
	}
	defer clear(body)
	endpoint := options.TokenURL
	if endpoint == "" {
		endpoint = devinauth.DefaultAPIBaseURL + "/auth/cli/token"
	}
	raw, err := doDevinControlRequest(ctx, endpoint, "application/json", "", body, options)
	if err != nil {
		return DevinCredential{}, err
	}
	defer clear(raw)
	var response struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &response) != nil || strings.TrimSpace(response.Token) == "" {
		return DevinCredential{}, errors.New("invalid Devin token response")
	}
	return verifyDevinCredential(ctx, DevinCredential{Type: ProviderDevin, SessionToken: devinauth.FormatSessionToken(response.Token)}, options)
}

func ImportDevinCredential(ctx context.Context, raw []byte, options DevinOptions) (DevinCredential, error) {
	allowed := devinCanonicalFields()
	for _, key := range []string{"api_key", "auth_kind", "base_url", "team_id", "org_name"} {
		allowed[key] = struct{}{}
	}
	allowCPAAuthFileControlFields(allowed)
	if len(raw) == 0 || len(raw) > maxCredentialBytes {
		return DevinCredential{}, errors.New("invalid Devin credential size")
	}
	if err := validateClaudeCredentialObject(raw, allowed); err != nil {
		return DevinCredential{}, errors.New("invalid Devin import fields")
	}
	if err := validateCPAAuthFileControlMetadata(raw); err != nil {
		return DevinCredential{}, err
	}
	var file struct {
		DevinCredential
		APIKey   string `json:"api_key"`
		AuthKind string `json:"auth_kind"`
		BaseURL  string `json:"base_url"`
		TeamID   string `json:"team_id"`
		OrgName  string `json:"org_name"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return DevinCredential{}, errors.New("invalid Devin import JSON")
	}
	if file.BaseURL != "" && strings.TrimRight(file.BaseURL, "/") != devinauth.DefaultServerURL || file.AuthKind != "" && file.AuthKind != "oauth" {
		return DevinCredential{}, errors.New("unsupported Devin import configuration")
	}
	value := file.DevinCredential
	normalizeDevinCredential(&value)
	apiKey := devinauth.FormatSessionToken(file.APIKey)
	if value.SessionToken != "" && apiKey != "" && value.SessionToken != apiKey {
		return DevinCredential{}, errors.New("conflicting Devin session tokens")
	}
	if value.SessionToken == "" {
		value.SessionToken = apiKey
	}
	// 缺少账号 ID 的 CPA 文件先通过同一字段校验，身份只在固定上游核实后补全。
	probe := value
	if probe.UserID == "" {
		probe.UserID = "pending-verification"
	}
	if err := validateDevinCredential(probe); err != nil {
		return DevinCredential{}, err
	}
	return verifyDevinCredential(ctx, value, options)
}

func RefreshDevinCredentialOnce(ctx context.Context, value DevinCredential, options DevinOptions) (DevinCredential, error) {
	if err := validateDevinCredential(value); err != nil {
		return DevinCredential{}, err
	}
	// Devin 会话令牌没有 refresh-token 轮换，刷新只核实身份和账号信息。
	return verifyDevinCredential(ctx, value, options)
}

func verifyDevinCredential(ctx context.Context, value DevinCredential, options DevinOptions) (DevinCredential, error) {
	probe := value
	if probe.UserID == "" {
		probe.UserID = "pending-verification"
	}
	if err := validateDevinCredential(probe); err != nil {
		return DevinCredential{}, err
	}
	observation, err := observeDevinAccount(ctx, value, "", options)
	return observation.Credential, err
}

func ObserveDevinAccount(ctx context.Context, value DevinCredential, apiRoot string, options DevinOptions) (DevinAccountObservation, error) {
	if err := validateDevinCredential(value); err != nil {
		return DevinAccountObservation{}, err
	}
	return observeDevinAccount(ctx, value, apiRoot, options)
}

func observeDevinAccount(ctx context.Context, value DevinCredential, apiRoot string, options DevinOptions) (DevinAccountObservation, error) {
	endpoint, err := ResolveAPIEndpoint(apiRoot, devinauth.DefaultServerURL+devinauth.DevinGetUserStatusPath)
	if err != nil {
		return DevinAccountObservation{}, err
	}
	if options.StatusURL != "" {
		endpoint = options.StatusURL
	}
	seed := value.DeviceSeed
	if seed == "" {
		seed = value.SessionToken
	}
	body := devinauth.BuildGetUserStatusRequest(value.SessionToken, devinauth.GenerateDeviceFingerprint(seed))
	defer clear(body)
	raw, err := doDevinControlRequest(ctx, endpoint, "application/proto", "Basic "+value.SessionToken+"-"+value.SessionToken, body, options)
	if err != nil {
		return DevinAccountObservation{}, err
	}
	defer clear(raw)
	status, err := devinauth.ParseGetUserStatusResponse(raw)
	if err != nil || status == nil || strings.TrimSpace(status.UserID) == "" {
		return DevinAccountObservation{}, errors.New("invalid Devin account response")
	}
	if value.UserID != "" && value.UserID != status.UserID || value.OrgID != "" && value.OrgID != status.OrgID {
		return DevinAccountObservation{}, ErrDevinCredentialIdentityChanged
	}
	value.UserID, value.OrgID, value.Email, value.UserName, value.Plan = status.UserID, status.OrgID, status.Email, status.UserName, status.Plan
	value.LastRefresh = devinNow(options).Format(time.RFC3339)
	normalizeDevinCredential(&value)
	if err := validateDevinCredential(value); err != nil {
		return DevinAccountObservation{}, err
	}
	result := DevinAccountObservation{Credential: value, DailyResetAt: status.DailyQuotaResetAt, WeeklyResetAt: status.WeeklyQuotaResetAt, PlanStart: status.PlanStart, PlanEnd: status.PlanEnd}
	user, _, err := devinProtoField(raw, 1, protowire.BytesType)
	if err != nil {
		return DevinAccountObservation{}, err
	}
	plan, _, err := devinProtoField(user, 13, protowire.BytesType)
	if err != nil {
		return DevinAccountObservation{}, err
	}
	for _, quota := range []struct {
		field  protowire.Number
		target **int64
	}{{14, &result.DailyRemaining}, {15, &result.WeeklyRemaining}} {
		rawValue, present, err := devinProtoField(plan, quota.field, protowire.VarintType)
		if err != nil {
			return DevinAccountObservation{}, err
		}
		if present {
			value, n := protowire.ConsumeVarint(rawValue)
			if n <= 0 || value > 100 {
				return DevinAccountObservation{}, errors.New("invalid Devin quota percentage")
			}
			percent := int64(value)
			*quota.target = &percent
		}
	}
	return result, nil
}

// CPA 将缺失的 protobuf 数值解析为零；保留字段存在性，避免误报额度耗尽。
func devinProtoField(raw []byte, field protowire.Number, expected protowire.Type) ([]byte, bool, error) {
	var result []byte
	present := false
	for len(raw) > 0 {
		number, kind, n := protowire.ConsumeTag(raw)
		if n <= 0 {
			return nil, false, errors.New("invalid Devin protobuf response")
		}
		raw = raw[n:]
		length := protowire.ConsumeFieldValue(number, kind, raw)
		if length <= 0 {
			return nil, false, errors.New("invalid Devin protobuf field")
		}
		if number == field {
			if kind != expected || present {
				return nil, false, errors.New("invalid Devin protobuf field type or duplicate")
			}
			present = true
			result = raw[:length]
			if kind == protowire.BytesType {
				result, _ = protowire.ConsumeBytes(result)
			}
		}
		raw = raw[length:]
	}
	return result, present, nil
}

func doDevinControlRequest(ctx context.Context, endpoint, contentType, authorization string, body []byte, options DevinOptions) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", "*/*")
	request.Header["User-Agent"] = []string{""}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
		request.Header.Set("Connect-Protocol-Version", "1")
	}
	client := http.Client{Timeout: 30 * time.Second}
	if options.HTTPClient != nil {
		client = *options.HTTPClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &DevinHTTPError{StatusCode: response.StatusCode, RetryAfter: boundedOAuthRetryAfter(response.Header, devinNow(options))}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		clear(raw)
		return nil, errors.New("invalid Devin response body")
	}
	return raw, nil
}

func devinNow(options DevinOptions) time.Time {
	if options.Now != nil {
		return options.Now().UTC()
	}
	return time.Now().UTC()
}

// 使用 SDK 随版本发布的模型目录，不启动 CPA 全局网络更新器或账号调度器。
func ListDevinModels() []string {
	models := registry.GetDevinModels()
	result := make([]string, 0, len(models))
	for _, model := range models {
		if model != nil && model.ID != "" {
			result = append(result, strings.TrimPrefix(model.ID, "devin/"))
		}
	}
	return result
}
