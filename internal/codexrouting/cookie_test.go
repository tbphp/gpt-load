package codexrouting

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestParseOaiLBExtractsUnifiedRegion(t *testing.T) {
	t.Parallel()
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	value := testOaiLB("chat.gateway.unified-122.api.openai.com", exp)
	host, region, expires, ok := parseOaiLB(value)
	if !ok {
		t.Fatal("parseOaiLB() ok = false")
	}
	if host != "chat.gateway.unified-122.api.openai.com" {
		t.Fatalf("host = %q", host)
	}
	if region != "unified-122" {
		t.Fatalf("region = %q", region)
	}
	if !expires.Equal(exp) {
		t.Fatalf("expires = %s, want %s", expires, exp)
	}
}

func TestParseSetCookiesKeepsOnlyRoutingCookies(t *testing.T) {
	t.Parallel()
	header := make(http.Header)
	header.Add("Set-Cookie", "__oailb=abc; Path=/; Secure; HttpOnly")
	header.Add("Set-Cookie", "__cf_bm=bot; Path=/; Secure")
	header.Add("Set-Cookie", "session=nope; Path=/")
	got := parseSetCookies(header)
	if len(got) != 2 {
		t.Fatalf("cookies = %#v", got)
	}
	if got[0].Name != cookieOaiLB || got[1].Name != cookieCFBM {
		t.Fatalf("names = %v", cookieNames(got))
	}
}

func testOaiLB(host string, exp time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, err := json.Marshal(map[string]any{"host": host, "exp": exp.Unix(), "iss": "edge-gateway"})
	if err != nil {
		panic(err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
