package embedded

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
)

func devinTestBytes(field protowire.Number, value []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, field, protowire.BytesType), value)
}

func devinTestStatus(userID string, quotas bool) []byte {
	plan := devinTestBytes(1, devinTestBytes(2, []byte("Pro")))
	if quotas {
		plan = protowire.AppendVarint(protowire.AppendTag(plan, 14, protowire.VarintType), 0)
		plan = protowire.AppendVarint(protowire.AppendTag(plan, 15, protowire.VarintType), 75)
		plan = protowire.AppendVarint(protowire.AppendTag(plan, 17, protowire.VarintType), 1900000000)
	}
	user := append(devinTestBytes(36, []byte(userID)), devinTestBytes(7, []byte("devin@example.test"))...)
	user = append(user, devinTestBytes(13, plan)...)
	return devinTestBytes(1, user)
}

func TestDevinAuthorizationUsesStagePKCEAndVerifiesIdentity(t *testing.T) {
	authorization, err := BeginDevinBrowserAuthorization()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(authorization.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(authorization.CodeVerifier))
	if u.Host != "app.devin.ai" || u.Query().Get("redirect_uri") != DevinRedirectURI || u.Query().Get("state") != authorization.State || u.Query().Get("code_challenge") != base64.RawURLEncoding.EncodeToString(sum[:]) || !authorization.ExpiresAt.After(time.Now()) {
		t.Fatal("invalid PKCE authorization")
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/token" {
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["code"] != "auth-code" || body["code_verifier"] != authorization.CodeVerifier {
				t.Error("wrong token exchange")
			}
			_, _ = io.WriteString(w, `{"token":"devin-session-token$test-secret"}`)
			return
		}
		if r.Header.Get("Authorization") != "Basic devin-session-token$test-secret-devin-session-token$test-secret" {
			t.Error("missing Devin wire authorization")
		}
		_, _ = w.Write(devinTestStatus("account-one", true))
	}))
	defer server.Close()
	options := DevinOptions{HTTPClient: server.Client(), TokenURL: server.URL + "/token", StatusURL: server.URL + "/status"}
	completion := BrowserAuthorizationCompletion{ExpectedState: authorization.State, ReturnedState: "wrong", Code: "auth-code", CodeVerifier: authorization.CodeVerifier}
	if _, err := CompleteDevinBrowserAuthorization(context.Background(), completion, options); err == nil || calls != 0 {
		t.Fatal("state mismatch must fail before HTTP")
	}
	completion.ReturnedState = authorization.State
	value, err := CompleteDevinBrowserAuthorization(context.Background(), completion, options)
	if err != nil {
		t.Fatal(err)
	}
	if value.UserID != "account-one" || value.Plan != "Pro" || value.SessionToken != "devin-session-token$test-secret" || calls != 2 {
		t.Fatal("authorization did not verify account")
	}
	raw, err := MarshalDevinCredential(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseDevinCredentialJSON(raw); err != nil {
		t.Fatal(err)
	}
}

func TestDevinImportRefreshAndQuotaPresence(t *testing.T) {
	userID, quotas, calls := "account-one", false, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = w.Write(devinTestStatus(userID, quotas)) }))
	defer server.Close()
	options := DevinOptions{HTTPClient: server.Client(), StatusURL: server.URL}
	ctx := context.Background()
	value, err := ImportDevinCredential(ctx, []byte(`{"type":"devin","api_key":"devin-session-token$secret","auth_kind":"oauth","base_url":"https://server.codeium.com","disabled":false,"note":"imported"}`), options)
	if err != nil {
		t.Fatal(err)
	}
	if value.UserID != userID || calls != 1 {
		t.Fatal("import must verify identity")
	}
	for _, raw := range []string{
		`{"type":"devin","session_token":"secret","base_url":"https://attacker.invalid"}`,
		`{"type":"devin","session_token":"secret","proxy_url":"https://attacker.invalid"}`,
		`{"type":"devin","session_token":"one","api_key":"two"}`,
		`{"type":"devin","type":"claude","session_token":"one"}`,
	} {
		before := calls
		if _, err := ImportDevinCredential(ctx, []byte(raw), options); err == nil || before != calls {
			t.Fatal("invalid import reached upstream")
		}
	}
	observed, err := ObserveDevinAccount(ctx, value, "", options)
	if err != nil {
		t.Fatal(err)
	}
	if observed.DailyRemaining != nil || observed.WeeklyRemaining != nil {
		t.Fatal("absent quota must remain unknown")
	}
	quotas = true
	observed, err = ObserveDevinAccount(ctx, value, "", options)
	if err != nil {
		t.Fatal(err)
	}
	if observed.DailyRemaining == nil || *observed.DailyRemaining != 0 || observed.WeeklyRemaining == nil || *observed.WeeklyRemaining != 75 {
		t.Fatal("explicit quota values were lost")
	}
	userID = "other-account"
	if _, err := RefreshDevinCredentialOnce(ctx, value, options); !errors.Is(err, ErrDevinCredentialIdentityChanged) {
		t.Fatal("refresh accepted identity change")
	}
}

func TestDevinControlErrorsDoNotExposeSecretsOrFollowRedirects(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/stolen")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "devin-session-token$secret")
			}))
			defer server.Close()
			_, err := ImportDevinCredential(context.Background(), []byte(`{"type":"devin","session_token":"devin-session-token$secret"}`), DevinOptions{HTTPClient: server.Client(), StatusURL: server.URL})
			if err == nil || strings.Contains(err.Error(), "$secret") {
				t.Fatal("unsafe error")
			}
		})
	}
}
