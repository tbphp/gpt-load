package mirasim

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"time"
)

const (
	credentialType            = "mirasim"
	defaultRelayURL           = "https://relay.mirasim.ai"
	defaultAdminURL           = "https://auth.mirasim.ai"
	defaultClientVersion      = "0.0.336"
	opaqueAccessTokenLifetime = 30 * time.Minute
)

// Storage is the CPA Mirasim OAuth file, kept self-contained so a group can
// import it without a second provider binding.
type Storage struct {
	Type             string `json:"type"`
	AccessToken      string `json:"access_token,omitempty"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	Expired          string `json:"expired,omitempty"`
	LastRefresh      string `json:"last_refresh,omitempty"`
	AccountID        string `json:"account_id,omitempty"`
	Email            string `json:"email,omitempty"`
	Plan             string `json:"plan,omitempty"`
	PlanExpiresAt    *int64 `json:"plan_exp,omitempty"`
	DevicePrivateKey string `json:"device_private_key,omitempty"`
	RelayURL         string `json:"relay_url,omitempty"`
	AdminURL         string `json:"admin_url,omitempty"`
	ClientVersion    string `json:"client_version,omitempty"`
}

func ParseCredentialJSON(raw []byte) (Storage, error) {
	if len(bytesTrim(raw)) == 0 {
		return Storage{}, fmt.Errorf("Mirasim credential is empty")
	}
	var storage Storage
	if err := json.Unmarshal(raw, &storage); err != nil {
		return Storage{}, fmt.Errorf("decode Mirasim credential: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(storage.Type), credentialType) {
		return Storage{}, fmt.Errorf("Mirasim credential type is missing")
	}
	storage.applyDefaults()
	storage.ensureTokenTiming(time.Now())
	storage.PopulateIdentityFromAccessToken()
	storage.PopulatePlanFromAccessToken()
	if err := storage.Validate(); err != nil {
		return Storage{}, err
	}
	return storage, nil
}

func MarshalCredential(storage Storage) ([]byte, error) {
	storage.applyDefaults()
	if err := storage.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(storage)
	if err != nil {
		return nil, fmt.Errorf("encode Mirasim credential: %w", err)
	}
	return raw, nil
}

func (s *Storage) applyDefaults() {
	if s == nil {
		return
	}
	s.Type = credentialType
	s.AccessToken = strings.TrimSpace(s.AccessToken)
	s.RefreshToken = strings.TrimSpace(s.RefreshToken)
	s.Expired = normalizeTimestamp(s.Expired)
	s.LastRefresh = normalizeTimestamp(s.LastRefresh)
	s.AccountID = strings.TrimSpace(s.AccountID)
	s.Email = strings.TrimSpace(s.Email)
	s.Plan = strings.TrimSpace(s.Plan)
	s.DevicePrivateKey = strings.TrimSpace(s.DevicePrivateKey)
	s.RelayURL = strings.TrimRight(strings.TrimSpace(s.RelayURL), "/")
	s.AdminURL = strings.TrimRight(strings.TrimSpace(s.AdminURL), "/")
	s.ClientVersion = strings.TrimSpace(s.ClientVersion)
	if s.RelayURL == "" {
		s.RelayURL = defaultRelayURL
	}
	if s.AdminURL == "" {
		s.AdminURL = defaultAdminURL
	}
	if s.ClientVersion == "" {
		s.ClientVersion = defaultClientVersion
	}
}

func (s Storage) Validate() error {
	if _, err := normalizeStoredSecret("access token", s.AccessToken); err != nil {
		return err
	}
	if _, err := normalizeStoredSecret("refresh token", s.RefreshToken); err != nil {
		return err
	}
	if strings.TrimSpace(s.DevicePrivateKey) == "" {
		return fmt.Errorf("Mirasim device private key is missing")
	}
	if !validDeviceKey([]byte(strings.TrimSpace(s.DevicePrivateKey))) {
		return fmt.Errorf("Mirasim device private key is not a valid Ed25519 PKCS#8 PEM")
	}
	return nil
}

func (s Storage) Identity() string {
	if accountID := strings.TrimSpace(s.AccountID); accountID != "" {
		return accountID
	}
	return deviceFingerprint(s.DevicePrivateKey)
}

func (s Storage) SecretValues() []string {
	values := make([]string, 0, 3)
	for _, value := range []string{s.AccessToken, s.RefreshToken, s.DevicePrivateKey} {
		value = strings.TrimSpace(value)
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

func (s *Storage) PopulateIdentityFromAccessToken() {
	if s == nil {
		return
	}
	claims := jwtClaims(s.AccessToken)
	if strings.TrimSpace(s.AccountID) == "" {
		s.AccountID = firstClaimString(claims, "account_id", "accountId", "user_id", "userId", "sub")
	}
	if strings.TrimSpace(s.Email) == "" {
		s.Email = firstClaimString(claims, "email")
	}
	s.AccountID = strings.TrimSpace(s.AccountID)
	s.Email = strings.TrimSpace(s.Email)
}

func AccessTokenAgentAccount(token string) string {
	return firstClaimString(jwtClaims(token), "account_id", "accountId")
}

func (s *Storage) PopulatePlanFromAccessToken() {
	if s == nil || strings.TrimSpace(s.Plan) != "" {
		return
	}
	plan, expiresAt := AccessTokenPlan(s.AccessToken)
	if plan == "" {
		return
	}
	s.Plan = plan
	s.PlanExpiresAt = cloneInt64(expiresAt)
}

func AccessTokenPlan(token string) (string, *int64) {
	claims := jwtClaims(token)
	plan, _ := claims["plan"].(string)
	plan = strings.TrimSpace(plan)
	if plan == "" {
		return "", nil
	}
	var expiresAt int64
	switch value := claims["plan_exp"].(type) {
	case float64:
		if value > 0 && value == float64(int64(value)) {
			expiresAt = int64(value)
		}
	case json.Number:
		expiresAt, _ = value.Int64()
	}
	if expiresAt <= 0 {
		return plan, nil
	}
	return plan, &expiresAt
}

func (s *Storage) RecordTokenTiming(accessToken string, expiresIn int64, now time.Time) {
	if s == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	s.Expired = ResolveAccessTokenExpiry(accessToken, expiresIn, now).Format(time.RFC3339)
	s.LastRefresh = now.Format(time.RFC3339)
}

func (s Storage) AccessTokenExpiry(now time.Time) time.Time {
	if parsed, ok := parseTimestamp(s.Expired); ok {
		return parsed
	}
	return ResolveAccessTokenExpiry(s.AccessToken, 0, now)
}

func ResolveAccessTokenExpiry(accessToken string, expiresIn int64, now time.Time) time.Time {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	if expiresIn > 0 {
		return now.Add(time.Duration(expiresIn) * time.Second)
	}
	if expiry := jwtExpiry(accessToken); !expiry.IsZero() {
		return expiry.UTC()
	}
	return now.Add(opaqueAccessTokenLifetime)
}

func (s *Storage) ensureTokenTiming(now time.Time) {
	if s == nil || strings.TrimSpace(s.AccessToken) == "" {
		return
	}
	if _, ok := parseTimestamp(s.Expired); !ok {
		s.Expired = ResolveAccessTokenExpiry(s.AccessToken, 0, now).Format(time.RFC3339)
	}
}

func InstallOAuth(storage Storage, accessToken, refreshToken string, now time.Time) (Storage, error) {
	accessToken, err := normalizeStoredSecret("access token", accessToken)
	if err != nil {
		return Storage{}, err
	}
	refreshToken, err = normalizeStoredSecret("refresh token", refreshToken)
	if err != nil {
		return Storage{}, err
	}
	keyPEM := strings.TrimSpace(storage.DevicePrivateKey)
	if keyPEM == "" || !validDeviceKey([]byte(keyPEM)) {
		generated, errKey := newDeviceKey()
		if errKey != nil {
			return Storage{}, errKey
		}
		keyPEM = strings.TrimSpace(string(generated))
	}
	storage.AccessToken = accessToken
	storage.RefreshToken = refreshToken
	storage.DevicePrivateKey = keyPEM
	storage.RecordTokenTiming(accessToken, 0, now)
	storage.PopulateIdentityFromAccessToken()
	storage.PopulatePlanFromAccessToken()
	storage.applyDefaults()
	if err := storage.Validate(); err != nil {
		return Storage{}, err
	}
	return storage, nil
}

func matchesRefreshIdentity(current, refreshed Storage) bool {
	if deviceFingerprint(current.DevicePrivateKey) != deviceFingerprint(refreshed.DevicePrivateKey) {
		return false
	}
	if current.AccountID != "" && current.AccountID != refreshed.AccountID {
		return false
	}
	if current.Email != "" && refreshed.Email != "" && !strings.EqualFold(current.Email, refreshed.Email) {
		return false
	}
	return true
}

func normalizeStoredSecret(label, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("Mirasim %s is missing", label)
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("Mirasim %s contains an invalid control character", label)
	}
	if len(value) > 64<<10 {
		return "", fmt.Errorf("Mirasim %s is unexpectedly large", label)
	}
	return value, nil
}

func newDeviceKey() ([]byte, error) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate Mirasim device private key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("marshal Mirasim device private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func validDeviceKey(raw []byte) bool {
	block, rest := pem.Decode(raw)
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return false
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return false
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	return ok && len(privateKey) == ed25519.PrivateKeySize
}

func deviceFingerprint(keyPEM string) string {
	block, _ := pem.Decode([]byte(strings.TrimSpace(keyPEM)))
	if block != nil {
		if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
			if privateKey, ok := parsed.(ed25519.PrivateKey); ok {
				if publicDER, errPublic := x509.MarshalPKIXPublicKey(privateKey.Public()); errPublic == nil {
					digest := sha256.Sum256(publicDER)
					return base64.RawURLEncoding.EncodeToString(digest[:12])
				}
			}
		}
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(keyPEM)))
	return base64.RawURLEncoding.EncodeToString(digest[:12])
}

func normalizeTimestamp(value string) string {
	if parsed, ok := parseTimestamp(value); ok {
		return parsed.Format(time.RFC3339)
	}
	return ""
}

func parseTimestamp(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func jwtExpiry(token string) time.Time {
	claims := jwtClaims(token)
	switch value := claims["exp"].(type) {
	case json.Number:
		seconds, err := value.Int64()
		if err != nil || seconds <= 0 {
			return time.Time{}
		}
		return time.Unix(seconds, 0).UTC()
	case float64:
		if value <= 0 {
			return time.Time{}
		}
		return time.Unix(int64(value), 0).UTC()
	default:
		return time.Time{}
	}
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if err := decoder.Decode(&claims); err != nil {
		return nil
	}
	return claims
}

func firstClaimString(claims map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := claims[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func bytesTrim(raw []byte) []byte {
	return []byte(strings.TrimSpace(string(raw)))
}
