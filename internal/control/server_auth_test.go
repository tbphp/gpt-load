package control

import (
	"crypto/sha256"
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

func TestAuthenticateLockedRequestsIgnoreForwardingHeaders(t *testing.T) {
	initControlI18n(t)
	_, engine := newAuthProbeServer(t)
	const peer = "192.0.2.10:1234"

	lockPeer(t, engine, peer)
	for index := 0; index < 2; index++ {
		headers := map[string]string{
			"X-Forwarded-For": "203.0.113." + strconv.Itoa(index+1),
			"X-Real-IP":       "198.51.100." + strconv.Itoa(index+1),
		}
		recorder := serveAuthRequest(
			engine,
			"/api/probe",
			peer,
			"Bearer wrong-key",
			headers,
		)
		if recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("attempt %d response = %d %s, want 429", index+1, recorder.Code, recorder.Body.String())
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

func TestAuthenticateComparesEveryLockedAuthorizationShapeOnce(t *testing.T) {
	initControlI18n(t)
	for index, test := range []struct {
		name          string
		header        string
		wantCandidate string
		wantStatus    int
	}{
		{name: "missing", wantStatus: http.StatusTooManyRequests},
		{name: "malformed", header: "Bearer", wantStatus: http.StatusTooManyRequests},
		{
			name:          "wrong",
			header:        "Bearer wrong-key",
			wantCandidate: "wrong-key",
			wantStatus:    http.StatusTooManyRequests,
		},
		{
			name:          "correct",
			header:        "Bearer " + authTestKey,
			wantCandidate: authTestKey,
			wantStatus:    http.StatusOK,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, engine := newAuthProbeServer(t)
			peer := "192.0.2." + strconv.Itoa(20+index) + ":1234"
			lockPeer(t, engine, peer)

			comparisons := 0
			fixedLength := false
			var comparedCandidate [sha256.Size]byte
			server.compareDigest = func(left, right []byte) int {
				comparisons++
				fixedLength = len(left) == sha256.Size && len(right) == sha256.Size
				copy(comparedCandidate[:], left)
				return subtle.ConstantTimeCompare(left, right)
			}

			recorder := serveAuthRequest(engine, "/api/probe", peer, test.header, nil)

			if recorder.Code != test.wantStatus {
				t.Fatalf("response = %d %s, want %d", recorder.Code, recorder.Body.String(), test.wantStatus)
			}
			if comparisons != 1 {
				t.Fatalf("credential comparisons = %d, want 1", comparisons)
			}
			if !fixedLength {
				t.Fatal("credential comparison did not receive two fixed-length SHA-256 digests")
			}
			if want := sha256.Sum256([]byte(test.wantCandidate)); comparedCandidate != want {
				t.Fatalf("candidate digest = %x, want SHA-256 of selected candidate", comparedCandidate)
			}
		})
	}
}

func TestAuthenticateLockedCorrectKeyClearsPeerAndNextFailureReturns401(t *testing.T) {
	initControlI18n(t)
	_, engine := newAuthProbeServer(t)
	const peer = "192.0.2.30:1234"

	lockPeer(t, engine, peer)
	assertAuthStatus(t, engine, peer, "Bearer "+authTestKey, http.StatusOK)
	assertAuthStatus(t, engine, peer, "Bearer wrong-key", http.StatusUnauthorized)
}

func TestAuthenticateLockedWrongTokensPreserve429ShapeAndRetryAfter(t *testing.T) {
	initControlI18n(t)
	server, engine := newAuthProbeServer(t)
	server.authFailures.now = func() time.Time {
		return time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	}
	const peer = "192.0.2.31:1234"

	lockPeer(t, engine, peer)
	first := serveAuthRequest(engine, "/api/probe", peer, "Bearer first-wrong-key", nil)
	second := serveAuthRequest(engine, "/api/probe", peer, "Bearer second-wrong-key", nil)

	for index, recorder := range []*httptest.ResponseRecorder{first, second} {
		if recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("response %d = %d %s, want 429", index+1, recorder.Code, recorder.Body.String())
		}
		var envelope struct {
			Code string `json:"code"`
			Data struct {
				RetryAfterSeconds int64 `json:"retry_after_seconds"`
			} `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode response %d: %v", index+1, err)
		}
		if envelope.Code != "AUTH_LOCKED" ||
			envelope.Data.RetryAfterSeconds != int64(authLockDuration/time.Second) {
			t.Fatalf("response %d envelope = %#v, want locked response shape", index+1, envelope)
		}
	}
	if first.Header().Get("Retry-After") == "" ||
		first.Header().Get("Retry-After") != second.Header().Get("Retry-After") {
		t.Fatalf(
			"Retry-After values = %q/%q, want equal non-empty values",
			first.Header().Get("Retry-After"),
			second.Header().Get("Retry-After"),
		)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("locked response bodies differ by wrong token: %q/%q", first.Body.String(), second.Body.String())
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
	assertAuthStatus(t, engine, peer, "Bearer wrong-key", http.StatusTooManyRequests)
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

func TestCollectionEndpointsRequireBearerAuthentication(t *testing.T) {
	initControlI18n(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, nil).RegisterRoutes(engine)

	peerIndex := 60
	for _, target := range []string{
		"/api/groups",
		"/api/groups/options",
		"/api/access-keys",
	} {
		for _, authorization := range []string{"", "Bearer wrong-key"} {
			peerIndex++
			recorder := serveAuthRequest(
				engine,
				target,
				"192.0.2."+strconv.Itoa(peerIndex)+":1234",
				authorization,
				nil,
			)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf(
					"GET %s auth %q = %d %s, want 401",
					target,
					authorization,
					recorder.Code,
					recorder.Body.String(),
				)
			}
			var envelope struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode auth envelope: %v", err)
			}
			if envelope.Code != "UNAUTHORIZED" {
				t.Fatalf("GET %s auth code = %q, want UNAUTHORIZED", target, envelope.Code)
			}
		}
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
