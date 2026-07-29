package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/utils"
)

func TestGatewaySecurityEventsUseIndependentCounters(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	var logs bytes.Buffer
	handler, _, _ := newHandlerForTest(
		t,
		&scriptedForwarder{},
		"sk-upstream",
	)
	handler.logger = newGatewayJSONLogger(&logs)
	handler.authFailureEvents = utils.NewRateLimitedEventCounter(
		time.Minute,
		func() time.Time { return now },
	)
	handler.routeNotFoundEvents = utils.NewRateLimitedEventCounter(
		time.Minute,
		func() time.Time { return now },
	)

	engine := gin.New()
	handler.RegisterRoutes(engine)
	serveGatewaySecurityRequest(
		engine,
		http.MethodGet,
		"/unknown?key=raw-query-canary",
		"Bearer wrong",
		"192.0.2.10:1000",
	)
	serveGatewaySecurityRequest(
		engine,
		http.MethodGet,
		"/unknown?key=raw-query-canary",
		"Bearer gl-client",
		"192.0.2.10:1000",
	)

	events := decodeGatewayJSONLogs(t, logs.Bytes())
	assertGatewayEventCount(t, events, "data_plane_auth_failed", 1)
	assertGatewayEventCount(t, events, "data_plane_route_not_found", 1)
	authEvent := gatewayEventsNamed(events, "data_plane_auth_failed")[0]
	if authEvent["peer_ip"] != "192.0.2.10" ||
		authEvent["total"] != float64(1) ||
		authEvent["level"] != "warning" ||
		authEvent["msg"] != "Data plane authentication failed" {
		t.Fatalf("auth event = %#v", authEvent)
	}
	routeEvent := gatewayEventsNamed(events, "data_plane_route_not_found")[0]
	if routeEvent["peer_ip"] != "192.0.2.10" ||
		routeEvent["access_key_id"] != float64(1) ||
		routeEvent["total"] != float64(1) ||
		routeEvent["level"] != "warning" ||
		routeEvent["msg"] != "Data plane route not found" {
		t.Fatalf("route event = %#v", routeEvent)
	}
	assertGatewayLogExcludes(
		t,
		logs.String(),
		"/unknown",
		"raw-query-canary",
	)
}

func TestGatewaySecurityEventCountersAccumulateAcrossWindows(t *testing.T) {
	tests := []struct {
		name      string
		eventName string
		serve     func(http.Handler)
	}{
		{
			name:      "authentication",
			eventName: "data_plane_auth_failed",
			serve: func(engine http.Handler) {
				serveGatewaySecurityRequest(
					engine,
					http.MethodGet,
					"/unknown",
					"Bearer wrong",
					"192.0.2.11:1000",
				)
			},
		},
		{
			name:      "route",
			eventName: "data_plane_route_not_found",
			serve: func(engine http.Handler) {
				serveGatewaySecurityRequest(
					engine,
					http.MethodGet,
					"/unknown",
					"Bearer gl-client",
					"192.0.2.11:1000",
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
			var logs bytes.Buffer
			handler, _, _ := newHandlerForTest(
				t,
				&scriptedForwarder{},
				"sk-upstream",
			)
			handler.logger = newGatewayJSONLogger(&logs)
			handler.authFailureEvents = utils.NewRateLimitedEventCounter(
				time.Minute,
				func() time.Time { return now },
			)
			handler.routeNotFoundEvents = utils.NewRateLimitedEventCounter(
				time.Minute,
				func() time.Time { return now },
			)
			engine := gin.New()
			handler.RegisterRoutes(engine)

			test.serve(engine)
			test.serve(engine)
			now = now.Add(time.Minute)
			test.serve(engine)

			events := gatewayEventsNamed(
				decodeGatewayJSONLogs(t, logs.Bytes()),
				test.eventName,
			)
			if len(events) != 2 ||
				events[0]["total"] != float64(1) ||
				events[1]["total"] != float64(3) {
				t.Fatalf("%s events = %#v, want totals 1 and 3", test.name, events)
			}
		})
	}
}

func TestGatewayRouteNotFoundCoversBothAuthenticatedExits(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		prepare func(*Handler)
	}{
		{
			name:    "unrecognized route",
			target:  "/unrecognized",
			prepare: func(*Handler) {},
		},
		{
			name:   "recognized route without dialect",
			target: "/v1/chat/completions",
			prepare: func(handler *Handler) {
				handler.dialects = dialect.NewSet()
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			handler, _, _ := newHandlerForTest(
				t,
				&scriptedForwarder{},
				"sk-upstream",
			)
			handler.logger = newGatewayJSONLogger(&logs)
			test.prepare(handler)
			engine := gin.New()
			handler.RegisterRoutes(engine)

			recorder := serveGatewaySecurityRequest(
				engine,
				http.MethodPost,
				test.target,
				"Bearer gl-client",
				"192.0.2.12:1000",
			)

			if recorder.Code != http.StatusNotFound {
				t.Fatalf("response = %d %s, want 404", recorder.Code, recorder.Body.String())
			}
			events := gatewayEventsNamed(
				decodeGatewayJSONLogs(t, logs.Bytes()),
				"data_plane_route_not_found",
			)
			if len(events) != 1 ||
				events[0]["access_key_id"] != float64(1) {
				t.Fatalf("route events = %#v, want one access_key_id=1", events)
			}
		})
	}
}

func TestGatewayInvalidAccessKeyDoesNotEmitRouteEvent(t *testing.T) {
	var logs bytes.Buffer
	handler, _, _ := newHandlerForTest(
		t,
		&scriptedForwarder{},
		"sk-upstream",
	)
	handler.logger = newGatewayJSONLogger(&logs)
	engine := gin.New()
	handler.RegisterRoutes(engine)

	recorder := serveGatewaySecurityRequest(
		engine,
		http.MethodGet,
		"/unrecognized",
		"Bearer wrong",
		"192.0.2.13:1000",
	)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("response = %d %s, want 401", recorder.Code, recorder.Body.String())
	}
	events := decodeGatewayJSONLogs(t, logs.Bytes())
	assertGatewayEventCount(t, events, "data_plane_auth_failed", 1)
	assertGatewayEventCount(t, events, "data_plane_route_not_found", 0)
}

func TestGatewayMalformedPeerDoesNotChangeResponseOrLeakRawValue(t *testing.T) {
	const rawPeer = "malformed:peer:raw-secret"
	var logs bytes.Buffer
	handler, _, _ := newHandlerForTest(
		t,
		&scriptedForwarder{},
		"sk-upstream",
	)
	handler.logger = newGatewayJSONLogger(&logs)
	engine := gin.New()
	handler.RegisterRoutes(engine)

	recorder := serveGatewaySecurityRequest(
		engine,
		http.MethodGet,
		"/unrecognized",
		"Bearer wrong",
		rawPeer,
	)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("response = %d %s, want 401", recorder.Code, recorder.Body.String())
	}
	events := gatewayEventsNamed(
		decodeGatewayJSONLogs(t, logs.Bytes()),
		"data_plane_auth_failed",
	)
	if len(events) != 1 || events[0]["peer_ip"] != "" {
		t.Fatalf("auth events = %#v, want empty peer_ip", events)
	}
	assertGatewayLogExcludes(t, logs.String(), rawPeer)
}

func TestGatewaySecurityEventsIgnoreForwardedPeerHeaders(t *testing.T) {
	const forwardedSecret = "203.0.113.99"
	var logs bytes.Buffer
	handler, _, _ := newHandlerForTest(
		t,
		&scriptedForwarder{},
		"sk-upstream",
	)
	handler.logger = newGatewayJSONLogger(&logs)
	engine := gin.New()
	handler.RegisterRoutes(engine)
	request := httptest.NewRequest(http.MethodGet, "/unrecognized", nil)
	request.RemoteAddr = "192.0.2.15:1000"
	request.Header.Set("Authorization", "Bearer wrong")
	request.Header.Set("X-Forwarded-For", forwardedSecret)
	request.Header.Set("X-Real-IP", forwardedSecret)
	recorder := httptest.NewRecorder()

	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("response = %d %s, want 401", recorder.Code, recorder.Body.String())
	}
	events := gatewayEventsNamed(
		decodeGatewayJSONLogs(t, logs.Bytes()),
		"data_plane_auth_failed",
	)
	if len(events) != 1 || events[0]["peer_ip"] != "192.0.2.15" {
		t.Fatalf("auth events = %#v, want direct peer", events)
	}
	assertGatewayLogExcludes(t, logs.String(), forwardedSecret)
}

type gatewayPanicLogHook struct{}

func (gatewayPanicLogHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (gatewayPanicLogHook) Fire(*logrus.Entry) error {
	panic("gateway log hook must be isolated")
}

func TestGatewaySecurityLoggerPanicDoesNotChangeResponses(t *testing.T) {
	handler, _, _ := newHandlerForTest(
		t,
		&scriptedForwarder{},
		"sk-upstream",
	)
	handler.logger = logrus.New()
	handler.logger.AddHook(gatewayPanicLogHook{})
	engine := gin.New()
	handler.RegisterRoutes(engine)

	unauthorized := serveGatewaySecurityRequest(
		engine,
		http.MethodGet,
		"/unrecognized",
		"Bearer wrong",
		"192.0.2.14:1000",
	)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf(
			"unauthorized response = %d %s, want 401",
			unauthorized.Code,
			unauthorized.Body.String(),
		)
	}

	notFound := serveGatewaySecurityRequest(
		engine,
		http.MethodGet,
		"/unrecognized",
		"Bearer gl-client",
		"192.0.2.14:1000",
	)
	if notFound.Code != http.StatusNotFound {
		t.Fatalf(
			"not-found response = %d %s, want 404",
			notFound.Code,
			notFound.Body.String(),
		)
	}
}

func newGatewayJSONLogger(output io.Writer) *logrus.Logger {
	logger := logrus.New()
	logger.SetOutput(output)
	logger.SetLevel(logrus.DebugLevel)
	logger.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	return logger
}

func serveGatewaySecurityRequest(
	engine http.Handler,
	method string,
	target string,
	authorization string,
	remoteAddr string,
) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, nil)
	request.RemoteAddr = remoteAddr
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	engine.ServeHTTP(recorder, request)
	return recorder
}

func decodeGatewayJSONLogs(
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

func gatewayEventsNamed(
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

func assertGatewayEventCount(
	t *testing.T,
	events []map[string]any,
	name string,
	want int,
) {
	t.Helper()
	if got := len(gatewayEventsNamed(events, name)); got != want {
		t.Fatalf("%s event count = %d, want %d: %#v", name, got, want, events)
	}
}

func assertGatewayLogExcludes(
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
