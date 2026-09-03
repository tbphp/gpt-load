package embedded

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// KiroDiscovery is a self-exploration result: it locates and parses Kiro
// account data that already exists on the local machine, so a user does not
// need to re-authenticate. Kiro stores its active account token in the AWS SSO
// token cache and its live usage/quota in the Kiro desktop app's global state.
type KiroDiscovery struct {
	// TokenFound is true when a live Kiro OAuth token was discovered on disk.
	TokenFound bool
	// Region is the token's serving region (e.g. "us-east-1").
	Region string
	// ExpiresAt is the token expiry, if known.
	ExpiresAt time.Time
	// Usage carries the live credit/quota mirrors discovered in the desktop app.
	Usage *KiroUsageDiscovery
}

// KiroUsageDiscovery is the quota mirror read from the Kiro desktop app.
type KiroUsageDiscovery struct {
	// ModelID is the account's last selected model.
	ModelID string
	// Breaks reports each credit/resource meter found.
	Breaks []KiroUsageBreak
}

// KiroUsageBreak is one credit/resource meter.
type KiroUsageBreak struct {
	DisplayName        string
	Type               string
	Unit               string
	CurrentUsage       float64
	UsageLimit         float64
	UsageLimitExplicit bool
	PercentageUsed     float64
	ResetDate          string
	CurrencyCode       string
}

// kiroTokenCache is the on-disk shape of ~/.aws/sso/cache/kiro-auth-token.json.
type kiroTokenCache struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	Region       string `json:"region"`
	ExpiresAt    string `json:"expiresAt"`
	Provider     string `json:"provider"`
	ClientIDHash string `json:"clientIdHash"`
}

// kiroProfileFilePaths returns the Kiro desktop app profile-resolver locations.
// The desktop app persists the account's resolved profileArn (world-ARN that the
// management plane uses to scope ListAvailableModels) in its global storage.
func kiroProfileFilePaths() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, "Library", "Application Support", "Kiro", "User", "globalStorage", "kiro.kiroagent", "profile.json"),
	}
}

// KiroProfileArnPath returns the primary on-disk path where the Kiro desktop
// app persists the account's resolved profileArn, or "" when the platform is
// unsupported. Callers use it to probe the same source self-discovery consults.
func KiroProfileArnPath() string {
	paths := kiroProfileFilePaths()
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

// ReadKiroProfileArn reads the resolved profileArn from a Kiro profile file at
// the given path. A missing, unreadable, or empty ARN yields "".
func ReadKiroProfileArn(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var profile struct {
		ARN string `json:"arn"`
	}
	if err := json.Unmarshal(raw, &profile); err != nil {
		return ""
	}
	return strings.TrimSpace(profile.ARN)
}

// resolveKiroProfileArn reads the resolved account profileArn (if any) that the
// Kiro desktop app persists in its global storage. The AWS SSO token cache does
// not carry a profileArn, so self-discovered credentials resolve it from here
// to satisfy the management plane's ListAvailableModels request. A missing or
// empty value is not an error.
func resolveKiroProfileArn() string {
	for _, path := range kiroProfileFilePaths() {
		if arn := ReadKiroProfileArn(path); arn != "" {
			return arn
		}
	}
	return ""
}

// kiroHomeCandidatePaths returns the likely Kiro token-cache locations for the
// current platform. The token lives in the shared AWS credential cache, so it
// is keyed by the user home directory rather than a Kiro-specific folder.
func kiroTokenCachePaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	base := filepath.Join(home, ".aws", "sso", "cache")
	return []string{
		filepath.Join(base, "kiro-auth-token.json"),
	}
}

// kiroVSCDBPaths returns the Kiro desktop app global-state DB locations.
func kiroVSCDBPaths() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, "Library", "Application Support", "Kiro", "User", "globalStorage", "state.vscdb"),
	}
}

// DiscoverKiroLocal scans the usual local locations for an active Kiro account
// and returns whatever it finds. A missing account is not an error: callers
// inspect TokenFound/Usage to decide what to render.
func DiscoverKiroLocal() (KiroDiscovery, error) {
	var discovery KiroDiscovery
	credential, found, err := readKiroTokenCache()
	if err != nil {
		return discovery, err
	}
	if found {
		discovery.TokenFound = true
		discovery.Region = credential.Region
		if expiresAt, ok := KiroCredentialExpiresAt(credential); ok {
			discovery.ExpiresAt = expiresAt
		}
	}
	usage, usageErr := readKiroUsageState()
	if usageErr == nil {
		discovery.Usage = usage
	}
	return discovery, nil
}

// DiscoverKiroCredential returns a KiroCredential from the local token cache,
// or an error when none is present. The locally-discovered token is an OAuth/
// IdC bearer whose profileArn is not stored on disk, so the returned
// credential may carry an empty ProfileARN: callers resolve profileArn at
// execution time via the Kiro management identity endpoint before sending.
func DiscoverKiroCredential() (KiroCredential, error) {
	credential, found, err := readKiroTokenCache()
	if err != nil {
		return KiroCredential{}, err
	}
	if !found {
		return KiroCredential{}, fmt.Errorf("no local Kiro token cache found")
	}
	if strings.TrimSpace(credential.AccessToken) == "" {
		return KiroCredential{}, fmt.Errorf("local Kiro token cache has no access token")
	}
	if _, err := validateKiroRegion(credential.Region); err != nil {
		credential.Region = DefaultKiroRegion
	}
	return credential, nil
}

func readKiroTokenCache() (KiroCredential, bool, error) {
	for _, path := range kiroTokenCachePaths() {
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return KiroCredential{}, false, err
		}
		var cache kiroTokenCache
		if err := json.Unmarshal(raw, &cache); err != nil {
			return KiroCredential{}, false, fmt.Errorf("decode Kiro token cache %s: %w", path, err)
		}
		region := strings.TrimSpace(cache.Region)
		if region == "" {
			region = DefaultKiroRegion
		}
		if _, err := validateKiroRegion(region); err != nil {
			region = DefaultKiroRegion
		}
		credential := KiroCredential{
			Type:         ProviderKiro,
			AuthKind:     string(KiroAuthSocial),
			AccessToken:  strings.TrimSpace(cache.AccessToken),
			RefreshToken: strings.TrimSpace(cache.RefreshToken),
			TokenType:    "Bearer",
			Region:       region,
			ProfileARN:   "",
		}
		// The token cache carries no email or account ARN, but clientIdHash is a
		// stable SSO client binding that uniquely identifies the logged-in Kiro
		// account and survives token refresh. Surface it as the account identity
		// so the credential is non-empty and stable across the lifecycle.
		credential.AccountID = strings.TrimSpace(cache.ClientIDHash)
		// The token cache does not store profileArn; the Kiro desktop app
		// persists the account's resolved profileArn in its global storage.
		// Surface it so the management-plane model-discovery call is satisfiable.
		if profileArn := resolveKiroProfileArn(); profileArn != "" {
			credential.ProfileARN = profileArn
		}
		if cache.ExpiresAt != "" {
			if expiresAt, err := time.Parse(time.RFC3339, cache.ExpiresAt); err == nil {
				credential.Expire = expiresAt.Format(time.RFC3339)
			}
		}
		normalizeKiroCredential(&credential)
		credential.KnownLocally = true
		return credential, strings.TrimSpace(credential.AccessToken) != "", nil
	}
	return KiroCredential{}, false, nil
}

// kiroUsageStateJSON is the parsed shape of the Kiro desktop app's
// "kiro.kiroAgent" global-state value. The usage state sits under the literal
// dotted key "kiro.resourceNotifications.usageState", with camelCase fields.
type kiroUsageStateJSON struct {
	LastSelectedModelID      string              `json:"lastSelectedModelId"`
	LastSelectedAutonomyMode string              `json:"lastSelectedAutonomyMode"`
	UsageState               kiroUsageStateValue `json:"kiro.resourceNotifications.usageState"`
}

type kiroUsageStateValue struct {
	UsageBreakdowns []kiroUsageBreakJSON `json:"usageBreakdowns"`
}

type kiroUsageBreakJSON struct {
	Currency struct {
		Code   string `json:"code"`
		Symbol string `json:"symbol"`
	} `json:"currency"`
	CurrentOverages   float64 `json:"currentOverages"`
	CurrentUsage      float64 `json:"currentUsage"`
	DisplayName       string  `json:"displayName"`
	DisplayNamePlural string  `json:"displayNamePlural"`
	PercentageUsed    float64 `json:"percentageUsed"`
	OverageCap        float64 `json:"overageCap"`
	OverageRate       float64 `json:"overageRate"`
	ResetDate         string  `json:"resetDate"`
	Type              string  `json:"type"`
	Unit              string  `json:"unit"`
	UsageLimit        float64 `json:"usageLimit"`
	HasUsageLimit     bool    `json:"hasUsageLimit"`
}

func readKiroUsageState() (*KiroUsageDiscovery, error) {
	usageJSON, ok := readKiroVSCDBValue("kiro.kiroAgent")
	if !ok {
		return nil, fmt.Errorf("Kiro usage state not present")
	}
	var state kiroUsageStateJSON
	if err := json.Unmarshal(usageJSON, &state); err != nil {
		return nil, fmt.Errorf("decode Kiro usage state: %w", err)
	}
	discovery := &KiroUsageDiscovery{ModelID: strings.TrimSpace(state.LastSelectedModelID)}
	for _, entry := range state.UsageState.UsageBreakdowns {
		limit := entry.UsageLimit
		limitExplicit := entry.HasUsageLimit || limit > 0
		discovery.Breaks = append(discovery.Breaks, KiroUsageBreak{
			DisplayName:        firstNonEmpty(entry.DisplayName, entry.DisplayNamePlural),
			Type:               strings.TrimSpace(entry.Type),
			Unit:               strings.TrimSpace(entry.Unit),
			CurrentUsage:       entry.CurrentUsage,
			UsageLimit:         limit,
			UsageLimitExplicit: limitExplicit,
			PercentageUsed:     entry.PercentageUsed,
			ResetDate:          strings.TrimSpace(entry.ResetDate),
			CurrencyCode:       strings.TrimSpace(entry.Currency.Code),
		})
	}
	return discovery, nil
}

// readKiroVSCDBValue reads a single key from a Kiro/VS Code style global-state
// SQLite DB (ItemTable). It shells out to the platform sqlite3 CLI, which is
// present on macOS and Linux ahead of install, and returns "" when the value
// or the db is unavailable.
func readKiroVSCDBValue(key string) ([]byte, bool) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return nil, false
	}
	for _, path := range kiroVSCDBPaths() {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		out, err := exec.Command("sqlite3", path, fmt.Sprintf("SELECT value FROM ItemTable WHERE key=%q;", key)).Output()
		if err != nil {
			continue
		}
		if len(out) > 0 {
			return out, true
		}
	}
	return nil, false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
