package control

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/i18n"
	"gpt-load/internal/platform/utils"
)

func TestAuthenticateEventsCountLockedRequestsWithoutRepeatingTransition(
	t *testing.T,
) {
	initControlI18n(t)
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	var logs bytes.Buffer
	server, engine := newAuthProbeServer(t)
	server.logger = newControlJSONLogger(&logs)
	server.authFailureEvents = utils.NewRateLimitedEventCounter(
		time.Minute,
		func() time.Time { return now },
	)
	server.authFailures.now = func() time.Time {
		return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	}
	const peer = "192.0.2.60:1234"

	for attempt := 1; attempt < authFailureLimit; attempt++ {
		recorder := serveAuthRequest(
			engine,
			"/api/probe",
			peer,
			"Bearer wrong-key",
			map[string]string{
				"X-Forwarded-For": "203.0.113.60",
				"X-Real-IP":       "203.0.113.61",
			},
		)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf(
				"attempt %d response = %d %s, want 401",
				attempt,
				recorder.Code,
				recorder.Body.String(),
			)
		}
	}
	assertControlAuthEventTotals(
		t,
		decodeControlJSONLogs(t, logs.Bytes()),
		"control_plane_auth_failed",
		[]float64{1},
	)

	locked := serveAuthRequest(
		engine,
		"/api/probe",
		peer,
		"Bearer wrong-key",
		nil,
	)
	if locked.Code != http.StatusTooManyRequests {
		t.Fatalf("lock response = %d %s, want 429", locked.Code, locked.Body.String())
	}
	events := decodeControlJSONLogs(t, logs.Bytes())
	assertControlAuthEventTotals(
		t,
		events,
		"control_plane_auth_failed",
		[]float64{1},
	)
	assertControlAuthEventCount(t, events, "control_plane_auth_locked", 1)

	now = now.Add(time.Minute)
	stillLocked := serveAuthRequest(
		engine,
		"/api/probe",
		peer,
		"Bearer wrong-key",
		nil,
	)
	if stillLocked.Code != http.StatusTooManyRequests {
		t.Fatalf(
			"locked response = %d %s, want 429",
			stillLocked.Code,
			stillLocked.Body.String(),
		)
	}
	events = decodeControlJSONLogs(t, logs.Bytes())
	assertControlAuthEventTotals(
		t,
		events,
		"control_plane_auth_failed",
		[]float64{1, 6},
	)
	assertControlAuthEventCount(t, events, "control_plane_auth_locked", 1)

	authEvent := controlEventsNamed(events, "control_plane_auth_failed")[0]
	if authEvent["peer_ip"] != "192.0.2.60" ||
		authEvent["level"] != "warning" ||
		authEvent["plane"] != "control" ||
		authEvent["msg"] != "[CONTROL] Authentication failed" {
		t.Fatalf("auth event = %#v", authEvent)
	}
	lockEvent := controlEventsNamed(events, "control_plane_auth_locked")[0]
	if lockEvent["peer_ip"] != "192.0.2.60" ||
		lockEvent["retry_after_seconds"] != float64(authLockDuration/time.Second) ||
		lockEvent["level"] != "warning" ||
		lockEvent["plane"] != "control" ||
		lockEvent["msg"] != "[CONTROL] Peer locked out" {
		t.Fatalf("lock event = %#v", lockEvent)
	}
	assertControlLogExcludes(
		t,
		logs.String(),
		"203.0.113.60",
		"203.0.113.61",
		"wrong-key",
	)

	beforeValid := len(events)
	valid := serveAuthRequest(
		engine,
		"/api/probe",
		peer,
		"Bearer "+authTestKey,
		nil,
	)
	if valid.Code != http.StatusOK {
		t.Fatalf("valid response = %d %s, want 200", valid.Code, valid.Body.String())
	}
	if afterValid := len(decodeControlJSONLogs(t, logs.Bytes())); afterValid != beforeValid {
		t.Fatalf(
			"valid credential added %d log entries",
			afterValid-beforeValid,
		)
	}
}

func TestAuthenticateLockRequestCanEmitBothEventsWhenGateOpens(t *testing.T) {
	initControlI18n(t)
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	var logs bytes.Buffer
	server, engine := newAuthProbeServer(t)
	server.logger = newControlJSONLogger(&logs)
	server.authFailureEvents = utils.NewRateLimitedEventCounter(
		time.Minute,
		func() time.Time { return now },
	)
	server.authFailures.now = func() time.Time {
		return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	}
	const peer = "192.0.2.61:1234"

	for attempt := 1; attempt < authFailureLimit; attempt++ {
		assertAuthStatus(
			t,
			engine,
			peer,
			"Bearer wrong-key",
			http.StatusUnauthorized,
		)
	}
	now = now.Add(time.Minute)
	assertAuthStatus(
		t,
		engine,
		peer,
		"Bearer wrong-key",
		http.StatusTooManyRequests,
	)

	events := decodeControlJSONLogs(t, logs.Bytes())
	assertControlAuthEventTotals(
		t,
		events,
		"control_plane_auth_failed",
		[]float64{1, 5},
	)
	assertControlAuthEventCount(t, events, "control_plane_auth_locked", 1)

	assertAuthStatus(
		t,
		engine,
		peer,
		"Bearer wrong-key",
		http.StatusTooManyRequests,
	)
	assertAuthStatus(
		t,
		engine,
		peer,
		"Bearer wrong-key",
		http.StatusTooManyRequests,
	)
	assertControlAuthEventCount(
		t,
		decodeControlJSONLogs(t, logs.Bytes()),
		"control_plane_auth_locked",
		1,
	)
}

func TestAuthenticateValidCredentialAddsNormalizedPeerToContext(t *testing.T) {
	initControlI18n(t)
	server := NewServer(&config.Config{AuthKey: authTestKey}, nil)
	engine := gin.New()
	api := engine.Group("/api")
	api.Use(i18n.Middleware(), server.authenticate())
	api.GET("/peer", func(c *gin.Context) {
		value, exists := c.Get(controlPeerContextKey)
		if !exists {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.String(http.StatusOK, "%s", value)
	})

	recorder := serveAuthRequest(
		engine,
		"/api/peer",
		"[::ffff:192.0.2.62]:1234",
		"Bearer "+authTestKey,
		nil,
	)

	if recorder.Code != http.StatusOK || recorder.Body.String() != "192.0.2.62" {
		t.Fatalf(
			"response = %d %q, want 200 normalized peer",
			recorder.Code,
			recorder.Body.String(),
		)
	}
}

func TestAuthenticateMalformedPeerDoesNotEmitSecurityEvent(t *testing.T) {
	initControlI18n(t)
	const rawPeer = "malformed:peer:raw-secret"
	var logs bytes.Buffer
	server, engine := newAuthProbeServer(t)
	server.logger = newControlJSONLogger(&logs)

	recorder := serveAuthRequest(
		engine,
		"/api/probe",
		rawPeer,
		"Bearer wrong-key",
		nil,
	)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("response = %d %s, want 500", recorder.Code, recorder.Body.String())
	}
	if len(decodeControlJSONLogs(t, logs.Bytes())) != 0 {
		t.Fatalf("malformed peer logs = %s, want none", logs.String())
	}
	assertControlLogExcludes(t, logs.String(), rawPeer)
}

type controlPanicLogHook struct{}

func (controlPanicLogHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (controlPanicLogHook) Fire(*logrus.Entry) error {
	panic("control log hook must be isolated")
}

func TestAuthenticateLoggerPanicDoesNotChangeResponses(t *testing.T) {
	initControlI18n(t)
	server, engine := newAuthProbeServer(t)
	server.logger = logrus.New()
	server.logger.AddHook(controlPanicLogHook{})
	const peer = "192.0.2.63:1234"

	for attempt := 1; attempt <= authFailureLimit; attempt++ {
		recorder := serveAuthRequest(
			engine,
			"/api/probe",
			peer,
			"Bearer wrong-key",
			nil,
		)
		wantStatus := http.StatusUnauthorized
		if attempt == authFailureLimit {
			wantStatus = http.StatusTooManyRequests
		}
		if recorder.Code != wantStatus {
			t.Fatalf(
				"attempt %d response = %d %s, want %d",
				attempt,
				recorder.Code,
				recorder.Body.String(),
				wantStatus,
			)
		}
	}
}

func newControlJSONLogger(output io.Writer) *logrus.Logger {
	logger := logrus.New()
	logger.SetOutput(output)
	logger.SetLevel(logrus.DebugLevel)
	logger.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	return logger
}

func decodeControlJSONLogs(
	t *testing.T,
	output []byte,
) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(output), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func controlEventsNamed(
	events []map[string]any,
	name string,
) []map[string]any {
	var matching []map[string]any
	for _, event := range events {
		if event["event"] == name {
			matching = append(matching, event)
		}
	}
	return matching
}

func assertControlAuthEventCount(
	t *testing.T,
	events []map[string]any,
	name string,
	want int,
) {
	t.Helper()
	if got := len(controlEventsNamed(events, name)); got != want {
		t.Fatalf("%s event count = %d, want %d: %#v", name, got, want, events)
	}
}

func assertControlAuthEventTotals(
	t *testing.T,
	events []map[string]any,
	name string,
	want []float64,
) {
	t.Helper()
	matching := controlEventsNamed(events, name)
	if len(matching) != len(want) {
		t.Fatalf("%s events = %#v, want totals %v", name, matching, want)
	}
	for index, total := range want {
		if matching[index]["total"] != total {
			t.Fatalf(
				"%s event %d total = %#v, want %v",
				name,
				index,
				matching[index]["total"],
				total,
			)
		}
	}
}

func assertControlLogExcludes(
	t *testing.T,
	output string,
	forbidden ...string,
) {
	t.Helper()
	for _, value := range forbidden {
		if bytes.Contains([]byte(output), []byte(value)) {
			t.Errorf("log output contains forbidden value %q: %s", value, output)
		}
	}
}
