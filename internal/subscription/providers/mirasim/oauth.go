package mirasim

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	// CallbackRedirectURI is the fixed loopback endpoint gpt-load listens on.
	// Mirasim keeps the query of redirect_uri and appends the tokens, so the
	// OAuth state travels inside that address rather than as a login parameter
	// the provider drops.
	CallbackRedirectURI  = "http://localhost:19827/mirasim/oauth/callback"
	oauthLoginTTL        = 30 * time.Minute
	defaultLoginProvider = "github"
)

var providerSlug = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type oauthLogin struct {
	URL         string
	State       string
	DriverState []byte
	ExpiresAt   time.Time
}

type oauthDriverState struct {
	DevicePrivateKey string `json:"device_private_key"`
	RelayURL         string `json:"relay_url"`
	AdminURL         string `json:"admin_url"`
	ClientVersion    string `json:"client_version"`
}

func BeginBrowserLogin(ctx context.Context, adminURL string) (oauthLogin, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	adminURL = strings.TrimRight(strings.TrimSpace(adminURL), "/")
	if adminURL == "" {
		adminURL = defaultAdminURL
	}
	if _, err := adminBaseURL(adminURL); err != nil {
		return oauthLogin{}, err
	}
	providers, err := discoverLoginProviders(ctx, adminURL)
	if err != nil {
		return oauthLogin{}, err
	}
	provider := defaultLoginProvider
	if !providerOffered(providers, provider) {
		provider = providers[0]
	}
	state, err := randomOAuthValue(32)
	if err != nil {
		return oauthLogin{}, err
	}
	deviceKey, err := newDeviceKey()
	if err != nil {
		return oauthLogin{}, err
	}
	callback, err := url.Parse(CallbackRedirectURI)
	if err != nil {
		return oauthLogin{}, err
	}
	callback.RawQuery = url.Values{"state": []string{state}}.Encode()
	authURL, err := buildMirasimOAuthURL(adminURL, provider, callback.String(), state)
	if err != nil {
		return oauthLogin{}, err
	}
	encoded, err := json.Marshal(oauthDriverState{
		DevicePrivateKey: strings.TrimSpace(string(deviceKey)),
		RelayURL:         defaultRelayURL,
		AdminURL:         adminURL,
		ClientVersion:    defaultClientVersion,
	})
	if err != nil {
		return oauthLogin{}, err
	}
	return oauthLogin{
		URL: authURL, State: state, DriverState: encoded,
		ExpiresAt: time.Now().Add(oauthLoginTTL),
	}, nil
}

func CompleteBrowserLogin(state oauthDriverState, accessToken, refreshToken string, now time.Time) (Storage, error) {
	storage := Storage{
		DevicePrivateKey: state.DevicePrivateKey,
		RelayURL:         state.RelayURL,
		AdminURL:         state.AdminURL,
		ClientVersion:    state.ClientVersion,
	}
	return InstallOAuth(storage, accessToken, refreshToken, now)
}

func discoverLoginProviders(ctx context.Context, adminURL string) ([]string, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, adminURL+"/auth/oauth/providers", nil)
	if err != nil {
		return nil, fmt.Errorf("invalid Mirasim OAuth discovery URL")
	}
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("Mirasim sign-in providers are unreachable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Mirasim sign-in provider discovery returned HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, fmt.Errorf("invalid Mirasim sign-in provider response")
	}
	var payload struct {
		Providers []string `json:"providers"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return nil, fmt.Errorf("invalid Mirasim sign-in provider response")
	}
	providers := make([]string, 0, len(payload.Providers))
	seen := map[string]bool{}
	for _, id := range payload.Providers {
		id = strings.ToLower(strings.TrimSpace(id))
		if !providerSlug.MatchString(id) || seen[id] {
			continue
		}
		seen[id] = true
		providers = append(providers, id)
	}
	if len(providers) == 0 {
		return nil, fmt.Errorf("Mirasim offered no sign-in providers")
	}
	return providers, nil
}

func providerOffered(providers []string, id string) bool {
	for _, provider := range providers {
		if provider == id {
			return true
		}
	}
	return false
}

func buildMirasimOAuthURL(adminURL, provider, callbackURL, state string) (string, error) {
	base, err := adminBaseURL(adminURL)
	if err != nil {
		return "", err
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/auth/oauth/" + url.PathEscape(provider) + "/login"
	query := base.Query()
	query.Set("redirect_uri", callbackURL)
	query.Set("state", state)
	base.RawQuery = query.Encode()
	return base.String(), nil
}

func adminBaseURL(adminURL string) (*url.URL, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(adminURL), "/"))
	if err != nil || base.Scheme == "" || base.Host == "" || base.User != nil || base.Opaque != "" || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("invalid Mirasim authentication service URL")
	}
	base.Scheme = strings.ToLower(base.Scheme)
	if base.Scheme != "https" && !(base.Scheme == "http" && isLoopbackHost(base.Hostname())) {
		return nil, fmt.Errorf("Mirasim authentication service URL must use HTTPS unless it is loopback")
	}
	return base, nil
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func randomOAuthValue(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate Mirasim OAuth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
