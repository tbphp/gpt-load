package codexrouting

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	probeClientVersion = "0.155.0"
	probeTimeout       = 45 * time.Second
	candyTimeout       = 180 * time.Second
	maxProbeBodyBytes  = 2 << 20
	defaultProbeURL    = "https://chatgpt.com/backend-api/codex/responses"
)

var (
	ErrDisabled          = errors.New("codex routing is disabled")
	ErrProbeProxyMissing = errors.New("codex routing probe proxy is not configured")
	ErrAlreadyProbing    = errors.New("codex routing probe is already running")
	ErrNoToken           = errors.New("codex routing credential token is unavailable")
)

type AccountSource interface {
	CodexAccounts() []AccountRef
}

type TokenSource interface {
	CodexToken(ctx context.Context, credentialID uint) (accessToken, accountID string, err error)
}

type Keeper struct {
	store     *Store
	accounts  AccountSource
	tokens    TokenSource
	transport http.RoundTripper
	lookupIPs func(string) ([]string, error)
	now       func() time.Time
}

func NewKeeper(store *Store, accounts AccountSource, tokens TokenSource) *Keeper {
	return &Keeper{store: store, accounts: accounts, tokens: tokens, lookupIPs: net.LookupHost, now: time.Now}
}

func (k *Keeper) Run(ctx context.Context) {
	if k == nil || k.store == nil {
		return
	}
	cfg := k.store.Config()
	if !cfg.Enabled || strings.TrimSpace(cfg.ProbeProxy) == "" {
		<-ctx.Done()
		return
	}
	timer := time.NewTicker(cfg.Interval)
	defer timer.Stop()
	k.probeDue(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			k.probeDue(ctx)
		}
	}
}

func (k *Keeper) ProbeOne(ctx context.Context, credentialID uint, model string) error {
	if k == nil || k.store == nil {
		return ErrDisabled
	}
	cfg := k.store.Config()
	if !cfg.Enabled {
		return ErrDisabled
	}
	if strings.TrimSpace(cfg.ProbeProxy) == "" && k.transport == nil {
		return ErrProbeProxyMissing
	}
	if credentialID == 0 {
		return fmt.Errorf("credential id is required")
	}
	if model == "" {
		model = cfg.Models[0]
	}
	if !k.store.MarkProbing(credentialID) {
		return ErrAlreadyProbing
	}
	defer k.store.UnmarkProbing(credentialID)
	if k.tokens == nil {
		return ErrNoToken
	}
	accessToken, accountID, err := k.tokens.CodexToken(ctx, credentialID)
	if err != nil {
		return err
	}
	if accessToken == "" {
		return ErrNoToken
	}
	var lastErr error
	attempts := 1
	if cfg.Candy || cfg.TargetGateway != "" {
		attempts = cfg.MaxRotates
		if attempts <= 0 {
			attempts = defaultMaxRotates
		}
	}
	for i := 0; i < attempts; i++ {
		session := k.store.SessionFor(credentialID)
		rotate := i > 0 || session == "" || k.store.NeedsNewSession(credentialID)
		if rotate {
			session = newSessionID()
			k.store.AssignSession(credentialID, session)
		}
		egress := regionForAttempt(cfg.ProbeRegions, i)
		proxyURL := applyPlaceholders(cfg.ProbeProxy, session, egress)
		k.store.SetProbeEgress(credentialID, egress)
		cookie := ""
		if !rotate {
			cookie = k.store.CookieHeader(credentialID)
		}
		status, header, body, err := k.roundTrip(ctx, credentialID, model, accessToken, accountID, proxyURL, cookie)
		k.store.TouchProbe(credentialID)
		if err != nil {
			lastErr = err
			k.store.DiscardPin(credentialID)
			continue
		}
		if status < 200 || status >= 300 {
			lastErr = fmt.Errorf("codex routing probe status %d", status)
			k.store.DiscardPin(credentialID)
			continue
		}
		region := regionFromHeaders(header)
		if cfg.TargetGateway != "" && !gatewayAllowed(cfg.TargetGateway, region) {
			k.store.DiscardPin(credentialID)
			lastErr = fmt.Errorf("codex routing gateway %s want %s", region, cfg.TargetGateway)
			continue
		}
		if cfg.Candy && !candyPassed(body) {
			k.store.DiscardPin(credentialID)
			lastErr = fmt.Errorf("codex routing candy probe failed")
			continue
		}
		served := createdModel(body)
		if served != "" && served != model {
			k.store.DiscardPin(credentialID)
			lastErr = fmt.Errorf("codex routing served %s want %s", served, model)
			continue
		}
		ticket := turnStateValue(header)
		if cfg.TicketLen > 0 && (len(ticket) == 0 || len(ticket) != cfg.TicketLen) {
			k.store.DiscardPin(credentialID)
			lastErr = fmt.Errorf("codex routing ticket len %d want %d", len(ticket), cfg.TicketLen)
			continue
		}
		k.store.Capture(WithDiscovery(ctx), credentialID, model, header, status)
		host := k.store.HostFor(credentialID)
		if host == "" && (cfg.Candy || cfg.TargetGateway != "") {
			k.store.DiscardPin(credentialID)
			lastErr = fmt.Errorf("codex routing probe missing region")
			continue
		}
		if cfg.Candy || cfg.TargetGateway != "" {
			k.store.ConfirmCandy(credentialID)
		}
		if ip := k.edgeIPForHost(host); ip != "" {
			k.store.SetEdgeIP(credentialID, ip)
		}
		if ticket != "" {
			k.store.SetTicket(credentialID, model, ticket, served)
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("codex routing probe failed")
	}
	logrus.WithError(lastErr).WithFields(logrus.Fields{
		"event":         "codex_routing.probe_failed",
		"credential_id": credentialID,
		"model":         model,
		"target":        cfg.TargetGateway,
		"regions":       strings.Join(cfg.ProbeRegions, ","),
	}).Warn("codex routing probe exhausted")
	return lastErr
}

func (k *Keeper) probeDue(ctx context.Context) {
	if k.accounts == nil {
		return
	}
	cfg := k.store.Config()
	model := cfg.Models[0]
	now := time.Now().UTC()
	if k.now != nil {
		now = k.now().UTC()
	}
	status := k.store.Status(k.accounts.CodexAccounts())
	for _, item := range status.Credentials {
		if ctx.Err() != nil {
			return
		}
		verdict := Verdict(item.Verdict)
		switch verdict {
		case VerdictEmpty, VerdictStale, VerdictRotated, VerdictDegraded:
		case VerdictPinned:
			if !cfg.Mint || item.TicketTTLSeconds == nil || *item.TicketTTLSeconds > int64(cfg.RefreshBefore.Seconds()) {
				continue
			}
		default:
			continue
		}
		if verdict == VerdictEmpty && recentlyProbed(item.LastProbeAtMS, now, cfg.Interval) {
			continue
		}
		_ = k.ProbeOne(ctx, item.CredentialID, model)
	}
}

func (k *Keeper) roundTrip(
	ctx context.Context,
	credentialID uint,
	model, accessToken, accountID, proxyURL, cookie string,
) (int, http.Header, []byte, error) {
	cfg := k.store.Config()
	payload := probePayload(model)
	if cfg.Candy {
		payload = candyPayload(model)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, nil, err
	}
	timeout := probeTimeout
	if cfg.Candy {
		timeout = candyTimeout
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, defaultProbeURL, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if accountID != "" {
		req.Header.Set("Chatgpt-Account-Id", accountID)
	}
	req.Header.Set("Originator", "codex_cli_rs")
	req.Header.Set("User-Agent", "codex_cli_rs/"+probeClientVersion)
	req.Header.Set("Version", probeClientVersion)
	req.Header.Set("Session-Id", newRequestID())
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	if edgeIP := cfg.EdgeIP; edgeIP != "" {
		req.Header.Set("X-Edge-IP", edgeIP)
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	client, err := k.httpClient(proxyURL, timeout)
	if err != nil {
		return 0, nil, nil, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req = req.WithContext(probeCtx)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxProbeBodyBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return resp.StatusCode, resp.Header.Clone(), nil, err
	}
	if len(raw) > maxProbeBodyBytes {
		raw = raw[:maxProbeBodyBytes]
	}
	return resp.StatusCode, resp.Header.Clone(), raw, nil
}

func (k *Keeper) edgeIPForHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	lookup := k.lookupIPs
	if lookup == nil {
		lookup = net.LookupHost
	}
	ips, err := lookup(host)
	if err != nil {
		return ""
	}
	for _, ip := range ips {
		parsed := net.ParseIP(ip)
		if parsed != nil && parsed.To4() != nil {
			return parsed.To4().String()
		}
	}
	if len(ips) > 0 {
		return ips[0]
	}
	return ""
}

func (k *Keeper) httpClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	transport := k.transport
	if transport == nil {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, err
		}
		if parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("invalid probe proxy url")
		}
		transport = &http.Transport{
			Proxy:               http.ProxyURL(parsed),
			TLSHandshakeTimeout: 15 * time.Second,
		}
	}
	if timeout <= 0 {
		timeout = probeTimeout
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func recentlyProbed(lastProbeAtMS *int64, now time.Time, interval time.Duration) bool {
	if lastProbeAtMS == nil || interval <= 0 {
		return false
	}
	return now.UnixMilli()-*lastProbeAtMS < interval.Milliseconds()
}

func regionForAttempt(regions []string, attempt int) string {
	if len(regions) == 0 {
		return ""
	}
	if attempt < 0 {
		attempt = 0
	}
	return regions[attempt%len(regions)]
}

func applyPlaceholders(proxyURL, session, region string) string {
	out := proxyURL
	if session != "" {
		out = strings.ReplaceAll(out, "{session}", session)
	}
	if region != "" {
		out = strings.ReplaceAll(out, "{region}", region)
	}
	return out
}

func applySession(proxyURL, session string) string {
	return applyPlaceholders(proxyURL, session, "")
}

func probePayload(model string) map[string]any {
	return map[string]any{
		"model":        model,
		"instructions": "Reply with exactly: pong",
		"input": []map[string]any{
			{
				"type": "message",
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": "Reply with exactly the single word: pong"},
				},
			},
		},
		"stream":              true,
		"store":               false,
		"reasoning":           map[string]any{"effort": "low"},
		"tools":               []any{},
		"parallel_tool_calls": false,
	}
}

func newSessionID() string {
	var raw [4]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

func newRequestID() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:])
}
