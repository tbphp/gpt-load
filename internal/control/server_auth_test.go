package control

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/i18n"
)

const authTestKey = "test-auth-key"

func TestNormalizePeerIP(t *testing.T) {
	for _, test := range []struct {
		remote string
		want   string
	}{
		{remote: "192.0.2.1:1234", want: "192.0.2.1"},
		{remote: "[::ffff:192.0.2.1]:1234", want: "192.0.2.1"},
		{remote: "[2001:db8::1]:1234", want: "2001:db8::1"},
		{remote: "[fe80::1%en0]:1234", want: "fe80::1"},
	} {
		t.Run(test.remote, func(t *testing.T) {
			got, err := normalizePeerIP(test.remote)
			if err != nil {
				t.Fatalf("normalizePeerIP(%q) error = %v", test.remote, err)
			}
			if got != test.want {
				t.Fatalf("normalizePeerIP(%q) = %q, want %q", test.remote, got, test.want)
			}
		})
	}

	for _, remote := range []string{
		"",
		"192.0.2.1",
		"192.0.2.1:",
		"192.0.2.1:not-a-port",
		"192.0.2.1:65536",
		"hostname:1234",
		"[2001:db8::1",
	} {
		t.Run("invalid "+remote, func(t *testing.T) {
			if got, err := normalizePeerIP(remote); err == nil {
				t.Fatalf("normalizePeerIP(%q) = %q, want error", remote, got)
			}
		})
	}
}

func TestAuthenticateFailsClosedForInvalidPeerWithoutComparison(t *testing.T) {
	initControlI18n(t)
	for _, remote := range []string{
		"",
		"192.0.2.1",
		"192.0.2.1:",
		"192.0.2.1:not-a-port",
		"192.0.2.1:65536",
		"hostname:1234",
		"[2001:db8::1",
	} {
		t.Run(remote, func(t *testing.T) {
			server, engine := newAuthProbeServer(t)
			comparisons := 0
			server.compareDigest = func(_, _ []byte) int {
				comparisons++
				return 1
			}

			recorder := serveAuthRequest(engine, "/api/probe", remote, "Bearer "+authTestKey, nil)

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("response = %d %s, want 500", recorder.Code, recorder.Body.String())
			}
			var envelope struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if envelope.Code != app_errors.ErrInternalServer.Code {
				t.Fatalf("code = %q, want %q", envelope.Code, app_errors.ErrInternalServer.Code)
			}
			if comparisons != 0 {
				t.Fatalf("credential comparisons = %d, want 0", comparisons)
			}
		})
	}
}

func TestAuthenticateIgnoresForwardingHeaders(t *testing.T) {
	initControlI18n(t)
	_, engine := newAuthProbeServer(t)

	for index, wantStatus := range []int{
		http.StatusUnauthorized,
		http.StatusUnauthorized,
		http.StatusUnauthorized,
		http.StatusUnauthorized,
		http.StatusTooManyRequests,
	} {
		headers := map[string]string{
			"X-Forwarded-For": "203.0.113." + strconv.Itoa(index+1),
			"X-Real-IP":       "198.51.100." + strconv.Itoa(index+1),
		}
		recorder := serveAuthRequest(
			engine,
			"/api/probe",
			"192.0.2.10:1234",
			"Bearer wrong-key",
			headers,
		)
		if recorder.Code != wantStatus {
			t.Fatalf("attempt %d response = %d %s, want %d", index+1, recorder.Code, recorder.Body.String(), wantStatus)
		}
	}
}

func TestAuthenticateSeparatesDifferentRemotePeers(t *testing.T) {
	initControlI18n(t)
	_, engine := newAuthProbeServer(t)

	for attempt := 0; attempt < authFailureLimit-1; attempt++ {
		for _, peer := range []string{"192.0.2.10:1234", "192.0.2.11:1234"} {
			recorder := serveAuthRequest(
				engine,
				"/api/probe",
				peer,
				"Bearer wrong-key",
				map[string]string{
					"X-Forwarded-For": "203.0.113.10",
					"X-Real-IP":       "203.0.113.10",
				},
			)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("peer %s attempt %d response = %d %s, want 401", peer, attempt+1, recorder.Code, recorder.Body.String())
			}
		}
	}
}

func TestAuthenticateComparesEveryUnlockedCredentialShapeOnce(t *testing.T) {
	initControlI18n(t)
	server, engine := newAuthProbeServer(t)

	for index, test := range []struct {
		name       string
		header     string
		wantStatus int
	}{
		{name: "missing header", wantStatus: http.StatusUnauthorized},
		{name: "single field", header: "Bearer", wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme", header: "Basic " + authTestKey, wantStatus: http.StatusUnauthorized},
		{name: "multiple fields", header: "Bearer " + authTestKey + " extra", wantStatus: http.StatusUnauthorized},
		{name: "wrong token", header: "Bearer wrong-key", wantStatus: http.StatusUnauthorized},
		{name: "valid bearer", header: "Bearer " + authTestKey, wantStatus: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			comparisons := 0
			server.compareDigest = func(left, right []byte) int {
				comparisons++
				return subtle.ConstantTimeCompare(left, right)
			}
			peer := "192.0.2." + strconv.Itoa(index+1) + ":1234"

			recorder := serveAuthRequest(engine, "/api/probe", peer, test.header, nil)

			if recorder.Code != test.wantStatus {
				t.Fatalf("response = %d %s, want %d", recorder.Code, recorder.Body.String(), test.wantStatus)
			}
			if comparisons != 1 {
				t.Fatalf("credential comparisons = %d, want 1", comparisons)
			}
		})
	}
}

func TestAuthenticateLockedCorrectKeyReturns429WithoutComparison(t *testing.T) {
	initControlI18n(t)
	server, engine := newAuthProbeServer(t)
	const peer = "192.0.2.20:1234"

	lockPeer(t, engine, peer)
	comparisons := 0
	server.compareDigest = func(_, _ []byte) int {
		comparisons++
		return 1
	}

	recorder := serveAuthRequest(engine, "/api/probe", peer, "Bearer "+authTestKey, nil)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("response = %d %s, want 429", recorder.Code, recorder.Body.String())
	}
	if comparisons != 0 {
		t.Fatalf("credential comparisons = %d, want 0", comparisons)
	}
}

func TestAuthenticateSuccessBeforeThresholdClearsPeerFailures(t *testing.T) {
	initControlI18n(t)
	_, engine := newAuthProbeServer(t)
	const peer = "192.0.2.21:1234"

	for attempt := 0; attempt < authFailureLimit-1; attempt++ {
		assertAuthStatus(t, engine, peer, "Bearer wrong-key", http.StatusUnauthorized)
	}
	assertAuthStatus(t, engine, peer, "Bearer "+authTestKey, http.StatusOK)
	for attempt := 0; attempt < authFailureLimit-1; attempt++ {
		assertAuthStatus(t, engine, peer, "Bearer wrong-key", http.StatusUnauthorized)
	}
	assertAuthStatus(t, engine, peer, "Bearer wrong-key", http.StatusTooManyRequests)
}

func TestAuthenticateLockExpiresAfterThirtyMinutes(t *testing.T) {
	initControlI18n(t)
	server, engine := newAuthProbeServer(t)
	current := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	server.authFailures.now = func() time.Time { return current }
	const peer = "192.0.2.22:1234"

	lockPeer(t, engine, peer)
	current = current.Add(authLockDuration - time.Second)
	assertAuthStatus(t, engine, peer, "Bearer "+authTestKey, http.StatusTooManyRequests)
	current = current.Add(time.Second)
	assertAuthStatus(t, engine, peer, "Bearer "+authTestKey, http.StatusOK)
}

func TestAuthenticateRetryAfterMatchesResponseData(t *testing.T) {
	initControlI18n(t)
	_, engine := newAuthProbeServer(t)
	const peer = "192.0.2.23:1234"

	recorder := lockPeer(t, engine, peer)
	var envelope struct {
		Code string `json:"code"`
		Data struct {
			RetryAfterSeconds int64 `json:"retry_after_seconds"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Code != "AUTH_LOCKED" {
		t.Fatalf("code = %q, want AUTH_LOCKED", envelope.Code)
	}
	headerSeconds, err := strconv.ParseInt(recorder.Header().Get("Retry-After"), 10, 64)
	if err != nil || headerSeconds < 1 ||
		headerSeconds != envelope.Data.RetryAfterSeconds {
		t.Fatalf(
			"Retry-After = %q, body seconds = %d, parse error = %v",
			recorder.Header().Get("Retry-After"),
			envelope.Data.RetryAfterSeconds,
			err,
		)
	}
}

func TestAuthenticateMessagesAreLocalized(t *testing.T) {
	initControlI18n(t)
	for index, test := range []struct {
		language     string
		unauthorized string
		locked       string
	}{
		{
			language:     "zh-CN",
			unauthorized: "无效的授权密钥",
			locked:       "认证尝试过多，请稍后重试",
		},
		{
			language:     "en-US",
			unauthorized: "Invalid authorization key",
			locked:       "Too many authentication attempts; try again later",
		},
		{
			language:     "ja-JP",
			unauthorized: "無効な認証キー",
			locked:       "認証試行回数が多すぎます。しばらくしてから再試行してください",
		},
	} {
		t.Run(test.language, func(t *testing.T) {
			_, engine := newAuthProbeServer(t)
			unauthorizedPeer := "192.0.2." + strconv.Itoa(30+index) + ":1234"
			unauthorized := serveAuthRequest(
				engine,
				"/api/probe",
				unauthorizedPeer,
				"Bearer wrong-key",
				map[string]string{"Accept-Language": test.language},
			)
			assertAuthMessage(t, unauthorized, http.StatusUnauthorized, "UNAUTHORIZED", test.unauthorized)

			lockedPeer := "192.0.2." + strconv.Itoa(40+index) + ":1234"
			var locked *httptest.ResponseRecorder
			for attempt := 0; attempt < authFailureLimit; attempt++ {
				locked = serveAuthRequest(
					engine,
					"/api/probe",
					lockedPeer,
					"Bearer wrong-key",
					map[string]string{"Accept-Language": test.language},
				)
			}
			assertAuthMessage(t, locked, http.StatusTooManyRequests, "AUTH_LOCKED", test.locked)
		})
	}
}

func TestAuthSessionEndpointReturnsAuthenticatedWithoutDatabaseAccess(t *testing.T) {
	initControlI18n(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, nil).RegisterRoutes(engine)

	recorder := serveAuthRequest(
		engine,
		"/api/auth/session",
		"192.0.2.50:1234",
		"Bearer "+authTestKey,
		nil,
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s, want 200", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var data map[string]bool
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if envelope.Code != 0 || len(data) != 1 || !data["authenticated"] {
		t.Fatalf("envelope code/data = %d/%s, want only authenticated=true", envelope.Code, envelope.Data)
	}
}

func TestAuthSessionEndpointRequiresAuthentication(t *testing.T) {
	initControlI18n(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, nil).RegisterRoutes(engine)

	recorder := serveAuthRequest(engine, "/api/auth/session", "192.0.2.51:1234", "", nil)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("response = %d %s, want 401", recorder.Code, recorder.Body.String())
	}
}

func TestAuthSessionEndpointUsesLimiter(t *testing.T) {
	initControlI18n(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, nil).RegisterRoutes(engine)
	const peer = "192.0.2.52:1234"

	for attempt, wantStatus := range []int{
		http.StatusUnauthorized,
		http.StatusUnauthorized,
		http.StatusUnauthorized,
		http.StatusUnauthorized,
		http.StatusTooManyRequests,
	} {
		recorder := serveAuthRequest(engine, "/api/auth/session", peer, "Bearer wrong-key", nil)
		if recorder.Code != wantStatus {
			t.Fatalf("attempt %d response = %d %s, want %d", attempt+1, recorder.Code, recorder.Body.String(), wantStatus)
		}
	}
}

func newAuthProbeServer(t *testing.T) (*Server, *gin.Engine) {
	t.Helper()
	server := NewServer(&config.Config{AuthKey: authTestKey}, nil)
	engine := gin.New()
	api := engine.Group("/api")
	api.Use(i18n.Middleware(), server.authenticate())
	api.GET("/probe", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return server, engine
}

func serveAuthRequest(
	engine *gin.Engine,
	target string,
	remoteAddr string,
	authorization string,
	headers map[string]string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.RemoteAddr = remoteAddr
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func lockPeer(t *testing.T, engine *gin.Engine, peer string) *httptest.ResponseRecorder {
	t.Helper()
	var recorder *httptest.ResponseRecorder
	for attempt := 0; attempt < authFailureLimit; attempt++ {
		recorder = serveAuthRequest(engine, "/api/probe", peer, "Bearer wrong-key", nil)
		wantStatus := http.StatusUnauthorized
		if attempt == authFailureLimit-1 {
			wantStatus = http.StatusTooManyRequests
		}
		if recorder.Code != wantStatus {
			t.Fatalf("attempt %d response = %d %s, want %d", attempt+1, recorder.Code, recorder.Body.String(), wantStatus)
		}
	}
	return recorder
}

func assertAuthStatus(
	t *testing.T,
	engine *gin.Engine,
	peer string,
	authorization string,
	wantStatus int,
) {
	t.Helper()
	recorder := serveAuthRequest(engine, "/api/probe", peer, authorization, nil)
	if recorder.Code != wantStatus {
		t.Fatalf("response = %d %s, want %d", recorder.Code, recorder.Body.String(), wantStatus)
	}
}

func assertAuthMessage(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	wantStatus int,
	wantCode string,
	wantMessage string,
) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("response = %d %s, want %d", recorder.Code, recorder.Body.String(), wantStatus)
	}
	var envelope struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Code != wantCode || envelope.Message != wantMessage {
		t.Fatalf("envelope = %#v, want code %q message %q", envelope, wantCode, wantMessage)
	}
}
