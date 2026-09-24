package codexrouting

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	cookieOaiLB = "__oailb"
	cookieCFLB  = "__cflb"
	cookieCFBM  = "__cf_bm"
)

var unifiedRegionPattern = regexp.MustCompile(`unified-\d+`)

var persistedCookieNames = map[string]struct{}{
	cookieOaiLB: {},
	cookieCFLB:  {},
	cookieCFBM:  {},
}

type Cookie struct {
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	Domain   string    `json:"domain,omitempty"`
	Path     string    `json:"path,omitempty"`
	Expires  time.Time `json:"expires,omitempty"`
	Secure   bool      `json:"secure,omitempty"`
	HTTPOnly bool      `json:"http_only,omitempty"`
}

type oaiLBClaims struct {
	Host string `json:"host"`
	Exp  int64  `json:"exp"`
	Iss  string `json:"iss"`
}

func parseSetCookies(header http.Header) []Cookie {
	if header == nil {
		return nil
	}
	parsed := (&http.Response{Header: header}).Cookies()
	if len(parsed) == 0 {
		return nil
	}
	out := make([]Cookie, 0, len(parsed))
	for _, cookie := range parsed {
		if cookie == nil || cookie.Name == "" {
			continue
		}
		if _, keep := persistedCookieNames[cookie.Name]; !keep {
			continue
		}
		item := Cookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Domain:   cookie.Domain,
			Path:     cookie.Path,
			Secure:   cookie.Secure,
			HTTPOnly: cookie.HttpOnly,
		}
		if !cookie.Expires.IsZero() {
			item.Expires = cookie.Expires.UTC()
		}
		out = append(out, item)
	}
	return out
}

func cookieByName(cookies []Cookie, name string) (Cookie, bool) {
	for _, cookie := range cookies {
		if cookie.Name == name && cookie.Value != "" {
			return cookie, true
		}
	}
	return Cookie{}, false
}

func parseOaiLB(value string) (host, region string, expires time.Time, ok bool) {
	parts := strings.Split(value, ".")
	if len(parts) < 2 {
		return "", "", time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return "", "", time.Time{}, false
		}
	}
	var claims oaiLBClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", "", time.Time{}, false
	}
	host = strings.TrimSpace(claims.Host)
	region = unifiedRegion(host)
	if region == "" {
		return host, "", time.Time{}, host != ""
	}
	if claims.Exp > 0 {
		expires = time.Unix(claims.Exp, 0).UTC()
	}
	return host, region, expires, true
}

func unifiedRegion(host string) string {
	return unifiedRegionPattern.FindString(host)
}

func cookieHeader(cookies []Cookie) string {
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie.Name == "" || cookie.Value == "" {
			continue
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(parts, "; ")
}

func cookieNames(cookies []Cookie) []string {
	names := make([]string, 0, len(cookies))
	seen := make(map[string]struct{}, len(cookies))
	for _, cookie := range cookies {
		if cookie.Name == "" {
			continue
		}
		if _, exists := seen[cookie.Name]; exists {
			continue
		}
		seen[cookie.Name] = struct{}{}
		names = append(names, cookie.Name)
	}
	return names
}

func mergeCookies(existing, incoming []Cookie) []Cookie {
	index := make(map[string]int, len(existing)+len(incoming))
	out := make([]Cookie, 0, len(existing)+len(incoming))
	for _, cookie := range existing {
		if cookie.Name == "" {
			continue
		}
		index[cookie.Name] = len(out)
		out = append(out, cookie)
	}
	for _, cookie := range incoming {
		if cookie.Name == "" {
			continue
		}
		if at, ok := index[cookie.Name]; ok {
			out[at] = cookie
			continue
		}
		index[cookie.Name] = len(out)
		out = append(out, cookie)
	}
	return out
}

func cfRayColo(header http.Header) string {
	ray := strings.TrimSpace(header.Get("Cf-Ray"))
	if ray == "" {
		return ""
	}
	if i := strings.LastIndex(ray, "-"); i >= 0 && i+1 < len(ray) {
		return strings.ToUpper(ray[i+1:])
	}
	return ""
}

func optionalBoolHeader(header http.Header, name string) *bool {
	raw := strings.TrimSpace(header.Get(name))
	if raw == "" {
		return nil
	}
	switch strings.ToLower(raw) {
	case "true", "1":
		value := true
		return &value
	case "false", "0":
		value := false
		return &value
	default:
		return nil
	}
}
