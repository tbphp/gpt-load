package embedded

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestObserveGrokAccountCombinesWeeklyAndMonthlyBilling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-secret" ||
			r.Header.Get("X-XAI-Token-Auth") != "xai-grok-cli" ||
			r.Header.Get("x-userid") != "account-1" ||
			r.Header.Get("x-grok-client-version") != grokClientVersion ||
			r.Header.Get("x-grok-client-identifier") != "grok-shell" ||
			r.Header.Get("x-authenticateresponse") != "authenticate-response" ||
			r.Header.Get("User-Agent") != "grok-pager/"+grokClientVersion+" grok-shell/"+grokClientVersion {
			t.Fatalf("billing headers = %#v", r.Header)
		}
		switch r.URL.Path {
		case "/weekly":
			_, _ = w.Write([]byte(`{"config":{"currentPeriod":{"type":"weekly","start":"2026-08-12T00:00:00Z","end":"2026-08-19T00:00:00Z"},"creditUsagePercent":25,"productUsage":[{"product":"GrokBuild","usagePercent":10}],"monthlyLimit":{"val":12000},"used":{"val":2500}}}`))
		case "/monthly":
			_, _ = w.Write([]byte(`{"config":{"currentPeriod":{"type":"monthly"},"creditUsagePercent":90,"monthlyLimit":{"val":15000},"onDemandCap":{"val":5000},"onDemandUsed":{"val":1000},"billingPeriodStart":"2026-08-01T00:00:00Z","billingPeriodEnd":"2026-09-01T00:00:00Z"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	observed, err := ObserveGrokAccount(t.Context(), GrokCredential{
		Type: ProviderGrok, AccessToken: "access-secret", RefreshToken: "refresh-secret",
		AccountID: "account-1", Email: "owner@example.com", Expire: "2030-01-01T00:00:00Z",
		TokenEndpoint: "https://auth.x.ai/oauth/token",
	}, GrokOptions{
		BillingWeeklyURL: server.URL + "/weekly", BillingMonthlyURL: server.URL + "/monthly",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !observed.AccountQuotaObserved || !observed.SurfaceQuotaObserved || !observed.CreditQuotaObserved ||
		observed.Billing.UsagePercent == nil || *observed.Billing.UsagePercent != 25 ||
		observed.Billing.MonthlyLimitCents == nil || *observed.Billing.MonthlyLimitCents != 15000 ||
		observed.Billing.UsedCents == nil || *observed.Billing.UsedCents != 2500 ||
		len(observed.Billing.ProductUsage) != 1 || len(observed.IncompleteSources) != 0 {
		t.Fatalf("observation = %#v", observed)
	}
}

func TestObserveGrokAccountReturnsPartialWhenOneBillingSourceFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/weekly" {
			_, _ = w.Write([]byte(`{"config":{"currentPeriod":{"type":"weekly"},"creditUsagePercent":40}}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	observed, err := ObserveGrokAccount(t.Context(), testGrokAccountCredential(), GrokOptions{
		BillingWeeklyURL: server.URL + "/weekly", BillingMonthlyURL: server.URL + "/monthly",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !observed.AccountQuotaObserved || observed.CreditQuotaObserved ||
		!reflect.DeepEqual(observed.IncompleteSources, []string{"monthly"}) {
		t.Fatalf("partial observation = %#v", observed)
	}
}

func TestObserveGrokAccountInfersZeroUsageForActiveUnifiedWeeklyPeriod(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/weekly" {
			_, _ = w.Write([]byte(`{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-08-15T00:00:00+00:00","end":"2026-08-22T00:00:00+00:00"},"billingPeriodStart":"2026-08-15T00:00:00+00:00","billingPeriodEnd":"2026-08-22T00:00:00+00:00","isUnifiedBillingUser":true}}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	observed, err := ObserveGrokAccount(t.Context(), testGrokAccountCredential(), GrokOptions{
		BillingWeeklyURL: server.URL + "/weekly", BillingMonthlyURL: server.URL + "/monthly",
		HTTPClient: server.Client(), Now: func() time.Time {
			return time.Date(2026, time.August, 19, 0, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !observed.AccountQuotaObserved || observed.Billing.UsagePercent == nil ||
		*observed.Billing.UsagePercent != 0 ||
		!reflect.DeepEqual(observed.IncompleteSources, []string{"monthly"}) {
		t.Fatalf("observation = %#v", observed)
	}
}

func TestDecodeGrokBillingDoesNotInferZeroWithoutConfirmedActiveUnifiedPeriod(t *testing.T) {
	now := time.Date(2026, time.August, 19, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "not unified", body: `{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-08-15T00:00:00Z","end":"2026-08-22T00:00:00Z"},"billingPeriodStart":"2026-08-15T00:00:00Z","billingPeriodEnd":"2026-08-22T00:00:00Z"}}`},
		{name: "bounds differ", body: `{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-08-15T00:00:00Z","end":"2026-08-22T00:00:00Z"},"billingPeriodStart":"2026-08-01T00:00:00Z","billingPeriodEnd":"2026-09-01T00:00:00Z","isUnifiedBillingUser":true}}`},
		{name: "period expired", body: `{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-08-01T00:00:00Z","end":"2026-08-08T00:00:00Z"},"billingPeriodStart":"2026-08-01T00:00:00Z","billingPeriodEnd":"2026-08-08T00:00:00Z","isUnifiedBillingUser":true}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			billing, _, err := decodeGrokBilling([]byte(test.body), now)
			if err != nil {
				t.Fatal(err)
			}
			if billing.UsagePercent != nil {
				t.Fatalf("usage percent = %v", *billing.UsagePercent)
			}
		})
	}
}

func TestObserveGrokAccountRejectsUnusableSuccessAndPreservesAuthStatus(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantErr    error
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{}`, wantStatus: http.StatusUnauthorized},
		{name: "forbidden", status: http.StatusForbidden, body: `{}`, wantStatus: http.StatusForbidden},
		{name: "upstream failure", status: http.StatusServiceUnavailable, body: `{}`, wantStatus: http.StatusServiceUnavailable},
		{name: "empty success", status: http.StatusOK, body: `{"config":{"futureField":true}}`, wantErr: ErrGrokAccountObservationPayloadInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			_, err := ObserveGrokAccount(t.Context(), testGrokAccountCredential(), GrokOptions{
				BillingWeeklyURL: server.URL, BillingMonthlyURL: server.URL, HTTPClient: server.Client(),
			})
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			var upstream *GrokUpstreamHTTPError
			if !errors.As(err, &upstream) || upstream.StatusCode != test.wantStatus {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

func TestObserveGrokAccountTreatsExplicitEmptyProductUsageAsObserved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/weekly" {
			_, _ = w.Write([]byte(`{"config":{"productUsage":[]}}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	observed, err := ObserveGrokAccount(t.Context(), testGrokAccountCredential(), GrokOptions{
		BillingWeeklyURL: server.URL + "/weekly", BillingMonthlyURL: server.URL + "/monthly",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !observed.SurfaceQuotaObserved || len(observed.Billing.ProductUsage) != 0 {
		t.Fatalf("observation = %#v", observed)
	}
}

func testGrokAccountCredential() GrokCredential {
	return GrokCredential{
		Type: ProviderGrok, AccessToken: "access-secret", RefreshToken: "refresh-secret",
		AccountID: "account-1", Email: "owner@example.com", Expire: "2030-01-01T00:00:00Z",
		TokenEndpoint: "https://auth.x.ai/oauth/token",
	}
}
