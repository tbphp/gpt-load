package codexrouting

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type staticAccounts []AccountRef

func (accounts staticAccounts) CodexAccounts() []AccountRef { return accounts }

type staticTokens struct {
	access string
	id     string
}

func (tokens staticTokens) CodexToken(context.Context, uint) (string, string, error) {
	return tokens.access, tokens.id, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestApplyPlaceholdersCyclesRegions(t *testing.T) {
	t.Parallel()
	template := "http://user-region-{region}-session-{session}:pass@127.0.0.1:5000"
	regions := []string{"US", "KR", "JP", "SG"}
	got := applyPlaceholders(template, "abcd", regionForAttempt(regions, 1))
	if !strings.Contains(got, "-region-KR-") || !strings.Contains(got, "-session-abcd") {
		t.Fatalf("proxy = %q", got)
	}
	if regionForAttempt(regions, 4) != "US" {
		t.Fatalf("wrap = %q", regionForAttempt(regions, 4))
	}
}

func TestProbeOneCapturesUnifiedCookieThroughProxyTransport(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	store := NewStore("", nil, Config{
		Enabled:    true,
		ProbeProxy: "http://user-session-{session}:pass@127.0.0.1:5000",
		Models:     []string{"gpt-6-astra"},
		EventLimit: 8,
	})
	store.now = func() time.Time { return now }
	var seenAuth string
	var cookies []string
	keeper := NewKeeper(store, staticAccounts{{CredentialID: 4}}, staticTokens{access: "tok", id: "acct"})
	keeper.transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		seenAuth = request.Header.Get("Authorization")
		cookies = append(cookies, request.Header.Get("Cookie"))
		header := make(http.Header)
		header.Add("Set-Cookie", "__oailb="+testOaiLB("chat.gateway.unified-15.api.openai.com", now.Add(time.Hour))+"; Path=/")
		header.Set("Cf-Ray", "zzz-SJC")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     header,
			Body:       io.NopCloser(strings.NewReader("data: {}\n\n")),
			Request:    request,
		}, nil
	})
	if err := keeper.ProbeOne(t.Context(), 4, "gpt-6-astra"); err != nil {
		t.Fatalf("ProbeOne() error = %v", err)
	}
	if seenAuth != "Bearer tok" {
		t.Fatalf("Authorization = %q", seenAuth)
	}
	if len(cookies) != 1 || cookies[0] != "" {
		t.Fatalf("first probe Cookie = %#v", cookies)
	}
	status := store.Status([]AccountRef{{CredentialID: 4}})
	if status.Credentials[0].Region != "unified-15" || status.Credentials[0].ProbeColo != "SJC" {
		t.Fatalf("status = %#v", status.Credentials[0])
	}
	if err := keeper.ProbeOne(t.Context(), 4, "gpt-6-astra"); err != nil {
		t.Fatalf("second ProbeOne() error = %v", err)
	}
	if len(cookies) != 2 || !strings.Contains(cookies[1], "__oailb=") {
		t.Fatalf("refresh probe Cookie = %#v", cookies)
	}
}

func TestProbeCandyRotatesUntilTwentyOne(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	store := NewStore("", nil, Config{
		Enabled:    true,
		Candy:      true,
		MaxRotates: 3,
		ProbeProxy: "http://user-session-{session}:pass@127.0.0.1:5000",
		Models:     []string{"gpt-6-astra"},
		EventLimit: 8,
	})
	store.now = func() time.Time { return now }
	var bodies []string
	keeper := NewKeeper(store, staticAccounts{{CredentialID: 8}}, staticTokens{access: "tok", id: "acct"})
	keeper.lookupIPs = func(host string) ([]string, error) {
		if !strings.Contains(host, "unified-167") {
			t.Fatalf("lookup host = %q", host)
		}
		return []string{"20.9.23.189"}, nil
	}
	keeper.transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		n := len(bodies)
		if n == 0 {
			header.Add("Set-Cookie", "__oailb="+testOaiLB("chat.gateway.unified-122.api.openai.com", now.Add(time.Hour))+"; Path=/")
			bodies = append(bodies, "29")
			return &http.Response{
				StatusCode: http.StatusOK, Header: header,
				Body: io.NopCloser(strings.NewReader(`data: {"type":"response.output_text.delta","delta":"最少 29 个"}

`)),
				Request: request,
			}, nil
		}
		header.Add("Set-Cookie", "__oailb="+testOaiLB("chat.gateway.unified-167.api.openai.com", now.Add(time.Hour))+"; Path=/")
		bodies = append(bodies, "21")
		return &http.Response{
			StatusCode: http.StatusOK, Header: header,
			Body: io.NopCloser(strings.NewReader(`data: {"type":"response.output_text.delta","delta":"最少 21 个"}

`)),
			Request: request,
		}, nil
	})
	if err := keeper.ProbeOne(t.Context(), 8, "gpt-6-astra"); err != nil {
		t.Fatalf("ProbeOne() error = %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("attempts = %d", len(bodies))
	}
	status := store.Status([]AccountRef{{CredentialID: 8}})
	item := status.Credentials[0]
	if item.Region != "unified-167" || !item.CandyOK || item.EdgeIP != "20.9.23.189" || item.Verdict != string(VerdictPinned) {
		t.Fatalf("status = %#v", item)
	}
	injected := make(http.Header)
	store.Inject(t.Context(), 8, "gpt-6-sol", injected)
	if !strings.Contains(injected.Get("Cookie"), "__oailb=") || injected.Get("X-Edge-IP") != "20.9.23.189" {
		t.Fatalf("inject = %v", injected)
	}
}

func TestProbeHuntsTargetGatewayAndInjectsTicket(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	store := NewStore("", nil, Config{
		Enabled:       true,
		TargetGateway: "unified-88",
		Mint:          true,
		MaxRotates:    3,
		ProbeProxy:    "http://user-session-{session}:pass@127.0.0.1:5000",
		Models:        []string{"gpt-6-astra"},
		EventLimit:    8,
	})
	store.now = func() time.Time { return now }
	var regions []string
	keeper := NewKeeper(store, staticAccounts{{CredentialID: 9}}, staticTokens{access: "tok", id: "acct"})
	keeper.lookupIPs = func(host string) ([]string, error) {
		return []string{"1.2.3.4"}, nil
	}
	keeper.transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		if len(regions) == 0 {
			regions = append(regions, "122")
			header.Add("Set-Cookie", "__oailb="+testOaiLB("chat.gateway.unified-122.api.openai.com", now.Add(time.Hour))+"; Path=/")
			return &http.Response{
				StatusCode: http.StatusOK, Header: header,
				Body: io.NopCloser(strings.NewReader(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-6-luna"}}

`)),
				Request: request,
			}, nil
		}
		regions = append(regions, "88")
		header.Add("Set-Cookie", "__cflb=lb; Path=/")
		header.Add("Set-Cookie", "__oailb="+testOaiLB("chat.gateway.unified-88.api.openai.com", now.Add(time.Hour))+"; Path=/")
		header.Set("X-Codex-Turn-State", "gAAAAA-ticket-88")
		return &http.Response{
			StatusCode: http.StatusOK, Header: header,
			Body: io.NopCloser(strings.NewReader(`data: {"type":"response.created","response":{"id":"resp_2","model":"gpt-6-astra"}}

`)),
			Request: request,
		}, nil
	})
	if err := keeper.ProbeOne(t.Context(), 9, "gpt-6-astra"); err != nil {
		t.Fatalf("ProbeOne() error = %v", err)
	}
	if len(regions) != 2 {
		t.Fatalf("attempts = %d", len(regions))
	}
	status := store.Status([]AccountRef{{CredentialID: 9}})
	item := status.Credentials[0]
	if item.Region != "unified-88" || !item.CandyOK || item.Verdict != string(VerdictPinned) {
		t.Fatalf("status = %#v", item)
	}
	injected := make(http.Header)
	store.Inject(t.Context(), 9, "gpt-6-astra", injected)
	if !strings.Contains(injected.Get("Cookie"), "__oailb=") {
		t.Fatalf("cookie = %q", injected.Get("Cookie"))
	}
	if injected.Get("X-Codex-Turn-State") != "gAAAAA-ticket-88" {
		t.Fatalf("ticket = %q", injected.Get("X-Codex-Turn-State"))
	}
}

func TestProbeAcceptsWhitelistedGateway(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	store := NewStore("", nil, Config{
		Enabled: true, TargetGateway: "unified-80,unified-101,unified-191",
		MaxRotates: 3, Models: []string{"gpt-6-astra"}, EventLimit: 8,
		TicketTTL: 240 * time.Second, ProbeProxy: "http://user-region-{region}-session-{session}:pass@127.0.0.1:5000",
	})
	store.now = func() time.Time { return now }
	var hosts []string
	keeper := NewKeeper(store, staticAccounts{{CredentialID: 4}}, staticTokens{access: "tok", id: "acct"})
	keeper.lookupIPs = func(string) ([]string, error) { return []string{"1.1.1.1"}, nil }
	keeper.transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		host := "chat.gateway.unified-122.api.openai.com"
		if len(hosts) > 0 {
			host = "chat.gateway.unified-80.api.openai.com"
		}
		hosts = append(hosts, host)
		header.Add("Set-Cookie", "__oailb="+testOaiLB(host, now.Add(time.Hour))+"; Path=/")
		return &http.Response{
			StatusCode: http.StatusOK, Header: header,
			Body: io.NopCloser(strings.NewReader(`data: {"type":"response.created","response":{"id":"resp","model":"gpt-6-astra"}}

`)),
			Request: request,
		}, nil
	})
	if err := keeper.ProbeOne(t.Context(), 4, "gpt-6-astra"); err != nil {
		t.Fatalf("ProbeOne() error = %v", err)
	}
	item := store.Status([]AccountRef{{CredentialID: 4}}).Credentials[0]
	if item.Region != "unified-80" || item.Verdict != string(VerdictPinned) {
		t.Fatalf("status = %#v hosts=%v", item, hosts)
	}
	if item.TTLSeconds == nil || *item.TTLSeconds > 240 {
		t.Fatalf("ttl = %v", item.TTLSeconds)
	}
}

func TestProbeRelayDiscardsOffTargetPair(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)
	store := NewStore("", nil, Config{
		Enabled: true, RelayURL: "https://relay.example/", RelayKey: "secret",
		TargetGateway: "unified-88", Models: []string{"gpt-6-astra"}, EventLimit: 8,
	})
	store.now = func() time.Time { return now }
	keeper := NewKeeper(store, staticAccounts{{CredentialID: 5}}, staticTokens{access: "tok", id: "acct"})
	keeper.transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"transport":"sse","gateway":"unified-159","cookies":{"__cflb":"lb","__oailb":"` + testOaiLB("chat.gateway.unified-159.api.openai.com", now.Add(time.Hour)) + `"}}`
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})
	if err := keeper.ProbeOne(t.Context(), 5, "gpt-6-astra"); err == nil {
		t.Fatal("ProbeOne() error = nil, want off-target")
	}
	injected := make(http.Header)
	store.Inject(t.Context(), 5, "gpt-6-astra", injected)
	if injected.Get("Cookie") != "" {
		t.Fatalf("injected off-target cookie = %q", injected.Get("Cookie"))
	}
}

func TestProbeRelayMintsCookiePair(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)
	store := NewStore("", nil, Config{
		Enabled: true, RelayURL: "https://relay.example/", RelayKey: "secret",
		TargetGateway: "unified-88", Mint: true, Models: []string{"gpt-6-astra"}, EventLimit: 8,
	})
	store.now = func() time.Time { return now }
	keeper := NewKeeper(store, staticAccounts{{CredentialID: 3}}, staticTokens{access: "tok", id: "acct"})
	keeper.transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("X-Relay-Key") != "secret" || request.Header.Get("X-Relay-Mint") != "any" {
			t.Fatalf("headers = %v", request.Header)
		}
		if request.Header.Get("X-Mint-Attempts") != "1" {
			t.Fatalf("probe attempts = %q", request.Header.Get("X-Mint-Attempts"))
		}
		if request.Header.Get("Cookie") != "" {
			t.Fatalf("mint Cookie = %q", request.Header.Get("Cookie"))
		}
		body := `{"transport":"sse","gateway":"unified-88","cookies":{"__cflb":"lb","__oailb":"` + testOaiLB("chat.gateway.unified-88.api.openai.com", now.Add(time.Hour)) + `"},"tickets":{"gpt-6-astra":{"turn_state":"gAAAAA-ticket-88","ticket_len":16,"served_model":"gpt-6-astra"}}}`
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})
	if err := keeper.ProbeOne(t.Context(), 3, "gpt-6-astra"); err != nil {
		t.Fatalf("ProbeOne() error = %v", err)
	}
	item := store.Status([]AccountRef{{CredentialID: 3}}).Credentials[0]
	if item.Region != "unified-88" || item.Verdict != string(VerdictPinned) {
		t.Fatalf("status = %#v", item)
	}
	injected := make(http.Header)
	store.Inject(t.Context(), 3, "gpt-6-astra", injected)
	if !strings.Contains(injected.Get("Cookie"), "__oailb=") {
		t.Fatalf("cookie = %q", injected.Get("Cookie"))
	}
	if injected.Get("X-Codex-Turn-State") != "gAAAAA-ticket-88" {
		t.Fatalf("ticket = %q", injected.Get("X-Codex-Turn-State"))
	}
}

func TestProbeRelayFallsBackAcrossTargetGateways(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 25, 4, 0, 0, 0, time.UTC)
	store := NewStore("", nil, Config{
		Enabled: true, RelayURL: "https://relay.example/", RelayKey: "secret",
		TargetGateway: "unified-96,unified-159", Mint: true,
		Models: []string{"gpt-6-astra"}, EventLimit: 8,
	})
	store.now = func() time.Time { return now }
	var seen []string
	keeper := NewKeeper(store, staticAccounts{{CredentialID: 5}}, staticTokens{access: "tok", id: "acct"})
	keeper.transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		gateway := request.Header.Get("X-Relay-Mint")
		seen = append(seen, gateway)
		if gateway != "unified-159" {
			body := `{"error":{"code":"mint_exhausted","gateways_seen":["unified-96"]}}`
			return &http.Response{
				StatusCode: http.StatusBadGateway, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(body)), Request: request,
			}, nil
		}
		body := `{"transport":"sse","gateway":"unified-159","cookies":{"__cflb":"lb","__oailb":"` + testOaiLB("chat.gateway.unified-159.api.openai.com", now.Add(time.Hour)) + `"},"tickets":{"gpt-6-astra":{"turn_state":"gAAAAA-ticket-159","ticket_len":17,"served_model":"gpt-6-astra"}}}`
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})
	if err := keeper.ProbeOne(t.Context(), 5, "gpt-6-astra"); err != nil {
		t.Fatalf("ProbeOne() error = %v", err)
	}
	want := []string{"any", "unified-96", "unified-159"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Fatalf("mint sequence = %v want %v", seen, want)
	}
	item := store.Status([]AccountRef{{CredentialID: 5}}).Credentials[0]
	if item.Region != "unified-159" || item.Verdict != string(VerdictPinned) {
		t.Fatalf("status = %#v", item)
	}
}
