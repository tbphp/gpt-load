package embedded

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMuseDeviceAuthorizationPreservesDCAAcrossMintRetry(t *testing.T) {
	var tokenCalls, mintCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/device":
			if err := r.ParseForm(); err != nil || r.Form.Get("client_id") != "1031625952748946" {
				t.Error("missing official device client ID")
			}
			_, _ = w.Write([]byte(`{"device_code":"device-secret","user_code":"ABCD","verification_uri":"https://auth.meta.com/device","expires_in":600,"interval":5}`))
		case "/token":
			tokenCalls++
			if err := r.ParseForm(); err != nil || r.Form.Get("device_code") != "device-secret" {
				t.Error("wrong device token request")
			}
			if tokenCalls == 1 {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":"slow_down"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"dca:renewal-secret","expires_in":600}`))
		case "/mint":
			mintCalls++
			if r.Header.Get("Authorization") != "Bearer dca:renewal-secret" {
				t.Error("wrong DCA authorization")
			}
			if mintCalls == 1 {
				w.Header().Set("Retry-After", "20")
				w.WriteHeader(429)
				return
			}
			_, _ = w.Write([]byte(`{"api_key":"minted-secret","user_email":"Muse@Example.com","subs_tier_name":"High Usage","is_subs_active":true}`))
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	now := time.Date(2026, 11, 2, 1, 0, 0, 0, time.UTC)
	options := MuseOptions{DeviceURL: server.URL + "/device", TokenURL: server.URL + "/token", MintURL: server.URL + "/mint", Now: func() time.Time { return now }}
	challenge, err := BeginMuseDeviceAuthorization(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if challenge.UserCode != "ABCD" || challenge.ExpiresAt != now.Add(10*time.Minute) {
		t.Fatal("invalid device challenge")
	}
	pending, err := PollMuseDeviceAuthorizationOnce(t.Context(), challenge.State, options)
	if err != nil || pending.Status != "pending" || pending.PollInterval != 10*time.Second {
		t.Fatalf("slow down: %s, %v", pending.Status, err)
	}
	authorized, err := PollMuseDeviceAuthorizationOnce(t.Context(), pending.State, options)
	if err != nil || authorized.Status != "pending" || authorized.State.DCAToken != "dca:renewal-secret" || mintCalls != 0 {
		t.Fatal("DCA was not checkpointed before minting")
	}
	retry, err := PollMuseDeviceAuthorizationOnce(t.Context(), authorized.State, options)
	if err != nil || retry.Status != "pending" || retry.State.DCAToken != authorized.State.DCAToken || retry.PollInterval != 20*time.Second {
		t.Fatalf("mint did not remain pending: status=%s interval=%s error=%v", retry.Status, retry.PollInterval, err)
	}
	now = now.Add(retry.PollInterval)
	completed, err := PollMuseDeviceAuthorizationOnce(t.Context(), retry.State, options)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "authorized" || completed.Credential.Email != "muse@example.com" || completed.Credential.AccessToken != "minted-secret" || tokenCalls != 2 || mintCalls != 2 {
		t.Fatal("authorization did not resume minting once")
	}
}

func TestMuseDeviceMintHonorsLongRetryAfter(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"api_key":"minted-secret","user_email":"account","is_subs_active":true}`))
	}))
	defer server.Close()
	now := time.Date(2026, 11, 2, 1, 0, 0, 0, time.UTC)
	options := MuseOptions{MintURL: server.URL, Now: func() time.Time { return now }}
	state := MuseDeviceState{DeviceCode: "device", PollInterval: 5, DCAToken: "dca:saved"}
	for attempt := 0; attempt < 2; attempt++ {
		pending, err := PollMuseDeviceAuthorizationOnce(t.Context(), state, options)
		if err != nil || pending.Status != "pending" || pending.PollInterval != time.Minute || calls != 1 {
			t.Fatalf("early mint retry: status=%s interval=%s calls=%d error=%v", pending.Status, pending.PollInterval, calls, err)
		}
		// 模拟 DriverState 加密落库后的 JSON 往返，不能只依赖进程内计时。
		raw, err := json.Marshal(pending.State)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &state); err != nil {
			t.Fatal(err)
		}
		now = now.Add(pending.PollInterval)
	}
	completed, err := PollMuseDeviceAuthorizationOnce(t.Context(), state, options)
	if err != nil || completed.Status != "authorized" || calls != 2 {
		t.Fatalf("mint did not resume: status=%s calls=%d error=%v", completed.Status, calls, err)
	}
}

func TestMuseDeviceMintRejectsDefinitiveFailures(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"invalid token", `{"error":"invalid_token"}`, http.StatusUnauthorized},
		{"inactive subscription", `{"api_key":"paid-key","user_email":"account","is_subs_active":false}`, http.StatusOK},
		{"invalid grant on server error", `{"error":"invalid_grant"}`, http.StatusServiceUnavailable},
		{"malformed success", `{`, http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			result, err := PollMuseDeviceAuthorizationOnce(t.Context(), MuseDeviceState{DeviceCode: "device", PollInterval: 5, DCAToken: "dca:saved"}, MuseOptions{MintURL: server.URL})
			if err == nil || result.Status == "pending" {
				t.Fatal("definitive mint failure became retryable")
			}
		})
	}
}

type museMintTransport func(*http.Request) (*http.Response, error)

func (transport museMintTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestMuseDeviceMintRetriesInterruptedTransportButNotCancellation(t *testing.T) {
	state := MuseDeviceState{DeviceCode: "device", PollInterval: 5, DCAToken: "dca:saved"}
	for _, interruption := range []error{io.ErrUnexpectedEOF, &net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "read", Err: museConnectionReset}}} {
		t.Run(interruption.Error(), func(t *testing.T) {
			calls := 0
			options := MuseOptions{HTTPClient: &http.Client{Transport: museMintTransport(func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return nil, interruption
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"api_key":"minted-key","user_email":"account","is_subs_active":true}`))}, nil
			})}}
			pending, err := PollMuseDeviceAuthorizationOnce(t.Context(), state, options)
			if err != nil || pending.Status != "pending" || pending.PollInterval != 10*time.Second || pending.State.DCAToken != state.DCAToken {
				t.Fatalf("interrupted mint did not remain pending: status=%s interval=%s error=%v", pending.Status, pending.PollInterval, err)
			}
			completed, err := PollMuseDeviceAuthorizationOnce(t.Context(), pending.State, options)
			if err != nil || completed.Status != "authorized" || calls != 2 {
				t.Fatalf("mint did not recover: status=%s calls=%d error=%v", completed.Status, calls, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	options := MuseOptions{HTTPClient: &http.Client{Transport: museMintTransport(func(request *http.Request) (*http.Response, error) {
		cancel()
		return nil, request.Context().Err()
	})}}
	defer cancel()
	if _, err := PollMuseDeviceAuthorizationOnce(ctx, state, options); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation became a retry: %v", err)
	}
}

func TestMuseDeviceAuthorizationTerminalStates(t *testing.T) {
	for _, code := range []string{"authorization_pending", "access_denied", "expired_token"} {
		t.Run(code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
			}))
			defer server.Close()
			result, err := PollMuseDeviceAuthorizationOnce(t.Context(), MuseDeviceState{DeviceCode: "code", PollInterval: 5}, MuseOptions{TokenURL: server.URL})
			want := map[string]string{"authorization_pending": "pending", "access_denied": "denied", "expired_token": "expired"}[code]
			if err != nil || result.Status != want {
				t.Fatalf("status %s: %v", result.Status, err)
			}
		})
	}
}

func TestMuseImportVerifiesIdentityAndIgnoresImportedEndpoints(t *testing.T) {
	var calls int
	email := "muse@example.com"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer dca:original" {
			t.Error("wrong import token")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"api_key": "verified-key", "user_email": email, "is_subs_active": true})
	}))
	defer server.Close()
	options := MuseOptions{MintURL: server.URL}
	credential, err := ImportMuseCredential(t.Context(), []byte(`{"type":"meta","auth_kind":"oauth","access_token":"untrusted-key","dca_token":"dca:original","email":"muse@example.com","base_url":"https://untrusted.example/v1","dca_expired":"2000-01-01T00:00:00Z"}`), options)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || credential.AccessToken != "verified-key" {
		t.Fatal("import did not verify and mint")
	}
	email = "another@example.com"
	if _, err := RefreshMuseCredentialOnce(t.Context(), credential, options); !errors.Is(err, ErrMuseCredentialIdentityChanged) {
		t.Fatalf("changed identity: %v", err)
	}
}

func TestMuseImportRejectsInactiveSubscription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"api_key":"paid-key","user_email":"muse@example.com","is_subs_active":false,"has_payment_method":true}`))
	}))
	defer server.Close()
	_, err := ImportMuseCredential(t.Context(), []byte(`{"type":"meta","dca_token":"dca:test"}`), MuseOptions{MintURL: server.URL})
	if err == nil {
		t.Fatal("inactive subscription silently accepted a potentially pay-as-you-go key")
	}
}

func TestMuseAuthErrorsStayBoundedAndDoNotRedirect(t *testing.T) {
	var redirected bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { redirected = true; w.WriteHeader(200) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(302)
		_, _ = w.Write([]byte(`{"error":"dca:private-secret","message":"minted-private-secret"}`))
	}))
	defer server.Close()
	_, err := ImportMuseCredential(t.Context(), []byte(`{"type":"meta","dca_token":"dca:test"}`), MuseOptions{MintURL: server.URL})
	if err == nil || redirected || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("unsafe auth failure: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = BeginMuseDeviceAuthorization(ctx, MuseOptions{DeviceURL: server.URL})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled auth: %v", err)
	}
}

func TestMuseCredentialsRejectIncompleteOrUnexpectedFields(t *testing.T) {
	for _, raw := range []string{
		`{"type":"muse","access_token":"key","email":"x"}`,
		`{"type":"muse","type":"muse","access_token":"key","dca_token":"dca:x","email":"x"}`,
		`{"type":"muse","access_token":"dca:x","dca_token":"dca:x","email":"x"}`,
		`{"type":"muse","access_token":"key","dca_token":"dca:x","email":"x","base_url":"https://other.example"}`,
	} {
		if _, err := ParseMuseCredentialJSON([]byte(raw)); err == nil {
			t.Fatal("accepted invalid credential")
		}
	}
}

func TestMuseModelsUseLiveAccountResults(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer minted" {
			t.Error("incorrect model discovery")
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"muse-wonder-1.0"},{"id":"muse-spark-1.3"},{"id":"muse-spark-1.3"}]}`))
	}))
	defer server.Close()
	models, err := ListMuseModels(t.Context(), MuseCredential{Type: ProviderMuse, AccessToken: "minted", DCAToken: "dca:test", Email: "muse@example.com"}, server.URL, MuseOptions{HTTPClient: server.Client()})
	if err != nil || !reflect.DeepEqual(models, []string{"muse-wonder-1.0", "muse-spark-1.3"}) {
		t.Fatalf("models = %v: %v", models, err)
	}
}
