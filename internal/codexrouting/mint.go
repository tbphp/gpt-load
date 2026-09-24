package codexrouting

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"
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
