package codexrouting

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	mintResponseLimit  = 256 << 10
	defaultMintGateway = "unified-88"
)

var gatewayNumber = regexp.MustCompile(`(?i)^(?:unified[-_.]?)?(\d+)$`)

type Ticket struct {
	Model     string    `json:"model,omitempty"`
	Value     string    `json:"value,omitempty"`
	Len       int       `json:"len,omitempty"`
	Served    string    `json:"served,omitempty"`
	IssuedAt  time.Time `json:"issued_at,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

type mintResult struct {
	Transport string            `json:"transport"`
	Gateway   string            `json:"gateway"`
	Cookies   map[string]string `json:"cookies"`
	EdgeIP    string            `json:"edge_ip"`
	ExpiresAt time.Time         `json:"expires_at"`
	Tickets   map[string]struct {
		TurnState   string    `json:"turn_state"`
		TicketLen   int       `json:"ticket_len"`
		ServedModel string    `json:"served_model"`
		IssuedAt    time.Time `json:"issued_at"`
		ExpiresAt   time.Time `json:"expires_at"`
	} `json:"tickets"`
}

func normalizeGateway(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" || value == "any" || value == "*" {
		return ""
	}
	if match := gatewayNumber.FindStringSubmatch(value); len(match) == 2 {
		return "unified-" + match[1]
	}
	return value
}

func createdModel(body []byte) string {
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if raw == "" || raw == "[DONE]" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			continue
		}
		typ, _ := obj["type"].(string)
		if typ != "response.created" {
			continue
		}
		resp, _ := obj["response"].(map[string]any)
		if resp == nil {
			continue
		}
		id, _ := resp["id"].(string)
		model, _ := resp["model"].(string)
		model = strings.TrimSpace(model)
		if strings.TrimSpace(id) == "" || model == "" {
			continue
		}
		return model
	}
	return ""
}

func fernetIssuedAt(ticket string, fallback time.Time) time.Time {
	raw, err := base64.RawURLEncoding.DecodeString(ticket)
	if err != nil {
		padded := ticket
		if n := len(padded) % 4; n != 0 {
			padded += strings.Repeat("=", 4-n)
		}
		raw, err = base64.URLEncoding.DecodeString(padded)
		if err != nil {
			return fallback
		}
	}
	if len(raw) < 9 || raw[0] != 0x80 {
		return fallback
	}
	sec := binary.BigEndian.Uint64(raw[1:9])
	if sec == 0 {
		return fallback
	}
	return time.Unix(int64(sec), 0).UTC()
}

func regionFromHeaders(header http.Header) string {
	incoming := parseSetCookies(header)
	oailb, ok := cookieByName(incoming, cookieOaiLB)
	if !ok {
		return ""
	}
	_, region, _, parsed := parseOaiLB(oailb.Value)
	if !parsed {
		return ""
	}
	return region
}

func turnStateValue(header http.Header) string {
	if header == nil {
		return ""
	}
	return strings.TrimSpace(header.Get("X-Codex-Turn-State"))
}

func (k *Keeper) probeRelay(ctx context.Context, credentialID uint, model, accessToken, accountID string) error {
	cfg := k.store.Config()
	entry, err := k.requestMint(ctx, cfg, model, accessToken, accountID)
	k.store.TouchProbe(credentialID)
	if err != nil {
		k.store.DiscardPin(credentialID)
		return err
	}
	header := mintCookieHeader(entry.Cookies)
	if ticket := entry.Tickets[model].TurnState; ticket != "" {
		header.Set("X-Codex-Turn-State", ticket)
	}
	k.store.Capture(WithDiscovery(ctx), credentialID, model, header, http.StatusOK)
	if cfg.TargetGateway != "" && !gatewayAllowed(cfg.TargetGateway, entry.Gateway) {
		k.store.DiscardPin(credentialID)
		return fmt.Errorf("codex routing gateway %s want %s", entry.Gateway, cfg.TargetGateway)
	}
	k.store.ConfirmCandy(credentialID)
	if entry.EdgeIP != "" {
		k.store.SetEdgeIP(credentialID, entry.EdgeIP)
	}
	if ticket := entry.Tickets[model]; ticket.TurnState != "" {
		k.store.SetTicket(credentialID, model, ticket.TurnState, ticket.ServedModel)
	}
	return nil
}

func (k *Keeper) requestMint(ctx context.Context, cfg Config, model, accessToken, accountID string) (mintResult, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.RelayURL), "/") + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return mintResult{}, err
	}
	req.Header.Set("X-Relay-Key", cfg.RelayKey)
	req.Header.Set("X-Relay-Mint", firstGateway(cfg.TargetGateway))
	req.Header.Set("X-Mint-Model", model)
	req.Header.Set("X-Mint-Transport", "sse")
	if cfg.TicketLen > 0 {
		req.Header.Set("X-Mint-Len", strconv.Itoa(cfg.TicketLen))
	}
	if cfg.TicketTTL > 0 {
		req.Header.Set("X-Mint-TTL", strconv.Itoa(int(cfg.TicketTTL.Seconds())))
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if accountID != "" {
		req.Header.Set("Chatgpt-Account-Id", accountID)
	}
	client := &http.Client{
		Timeout: 180 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if k.transport != nil {
		client.Transport = k.transport
		client.Timeout = 0
	}
	resp, err := client.Do(req)
	if err != nil {
		return mintResult{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, mintResponseLimit+1))
	if err != nil || len(raw) > mintResponseLimit {
		return mintResult{}, errors.New("cloud mint response invalid or too large")
	}
	if resp.StatusCode != http.StatusOK {
		return mintResult{}, fmt.Errorf("cloud mint status %d", resp.StatusCode)
	}
	var result mintResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return mintResult{}, errors.New("cloud mint response is not JSON")
	}
	ticket, ok := result.Tickets[model]
	if !ok || ticket.TurnState == "" {
		return mintResult{}, errors.New("cloud mint missing ticket")
	}
	if result.Cookies["__oailb"] == "" || result.Cookies["__cflb"] == "" {
		return mintResult{}, errors.New("cloud mint missing cookie pair")
	}
	if cfg.TicketLen > 0 && len(ticket.TurnState) != cfg.TicketLen {
		return mintResult{}, fmt.Errorf("cloud mint ticket len %d want %d", len(ticket.TurnState), cfg.TicketLen)
	}
	if result.Gateway == "" {
		_, result.Gateway, _, _ = parseOaiLB(result.Cookies["__oailb"])
	}
	return result, nil
}

func mintCookieHeader(cookies map[string]string) http.Header {
	header := make(http.Header)
	for _, name := range []string{cookieOaiLB, cookieCFLB, cookieCFBM} {
		if value := cookies[name]; value != "" {
			header.Add("Set-Cookie", name+"="+value+"; Path=/; Secure")
		}
	}
	return header
}

func firstGateway(targets string) string {
	for _, part := range strings.Split(targets, ",") {
		if value := normalizeGateway(part); value != "" {
			return value
		}
	}
	return defaultMintGateway
}
