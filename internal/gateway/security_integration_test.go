package gateway

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestGatewayNeverExposesPlaintextKeys(t *testing.T) {
	var logs bytes.Buffer
	logger := logrus.StandardLogger()
	previousOutput := logger.Out
	previousFormatter := logger.Formatter
	previousLevel := logger.Level
	previousHooks := logger.ReplaceHooks(make(logrus.LevelHooks))
	logger.SetOutput(&logs)
	logger.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	logger.SetLevel(logrus.DebugLevel)
	logger.AddHook(redact.NewHook(redact.New()))
	t.Cleanup(func() {
		logger.SetOutput(previousOutput)
		logger.SetFormatter(previousFormatter)
		logger.SetLevel(previousLevel)
		logger.ReplaceHooks(previousHooks)
	})

	t.Run("upstream error body", func(t *testing.T) {
		const secret = "custom-upstream-plaintext-credential"
		upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_, _ = io.Copy(io.Discard, request.Body)
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = writer.Write([]byte(`{"error":{"api_key":"` + secret + `"}}`))
		}))
		defer upstream.Close()

		engine, _ := newStreamingGatewayEngine(t, streamGatewayGroup{
			id: 1, name: "error-group", upstreamURL: upstream.URL, apiKey: secret,
		})
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)

		assertNoPlaintextSecrets(t, recorder, logs.String(), secret)
		if !strings.Contains(recorder.Body.String(), redact.Placeholder) {
			t.Fatalf("safe upstream error body = %s, want placeholder", recorder.Body.String())
		}
	})

	t.Run("compressed stream exhaustion", func(t *testing.T) {
		const firstSecret = "custom-compressed-first-credential"
		const secondSecret = "custom-compressed-second-credential"
		compressedServer := func() *httptest.Server {
			return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				_, _ = io.Copy(io.Discard, request.Body)
				writer.Header().Set("Content-Type", "text/event-stream")
				writer.Header().Set("Content-Encoding", "gzip")
				_, _ = writer.Write([]byte("data: forbidden\n\n"))
			}))
		}
		first := compressedServer()
		defer first.Close()
		second := compressedServer()
		defer second.Close()

		engine, _ := newStreamingGatewayEngine(t,
			streamGatewayGroup{id: 1, name: "compressed-a", upstreamURL: first.URL, apiKey: firstSecret},
			streamGatewayGroup{id: 2, name: "compressed-b", upstreamURL: second.URL, apiKey: secondSecret},
		)
		recorder := performStreamingRequest(engine)

		assertNoPlaintextSecrets(t, recorder, logs.String(), firstSecret, secondSecret)
		if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), reasonUpstreamProtocol.Code) {
			t.Fatalf("protocol response = %d %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("ordinary client error body", func(t *testing.T) {
		const secret = "QZVX-provider-secret-WKJP"
		upstream := httptest.NewServer(http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			_, _ = io.Copy(io.Discard, request.Body)
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(
				`{"error":{"api_key":"` + secret + `"}}`,
			))
		}))
		defer upstream.Close()

		engine, _ := newStreamingGatewayEngine(
			t,
			streamGatewayGroup{
				id: 1, name: "client-error-group",
				upstreamURL: upstream.URL, apiKey: secret,
			},
		)
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/chat/completions",
			strings.NewReader(`{"model":"gpt-4o"}`),
		)
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)

		assertNoPlaintextSecrets(t, recorder, logs.String(), secret)
		if !strings.Contains(
			recorder.Body.String(),
			redact.Placeholder,
		) {
			t.Fatalf(
				"safe client error body = %s, want placeholder",
				recorder.Body.String(),
			)
		}
	})
}

func TestGatewaySecurityEventFormatterSecretMatrix(t *testing.T) {
	const (
		accessKey           = "gl-client-access-secret-0002"
		providerKey         = "QZVX-provider-secret-WKJP"
		authorizationSecret = "gl-authorization-secret-0004"
		xAPISecret          = "sk-x-api-secret-0005"
		xGoogSecret         = "sk-x-goog-secret-0006"
		geminiQuerySecret   = "sk-gemini-query-secret-0007"
		requestBodySecret   = "sk-request-body-secret-0009"
	)
	formatters := map[string]logrus.Formatter{
		"text": &logrus.TextFormatter{
			DisableTimestamp: true,
			DisableColors:    true,
		},
		"json": &logrus.JSONFormatter{DisableTimestamp: true},
	}
	for name, formatter := range formatters {
		t.Run(name, func(t *testing.T) {
			forwarder := &scriptedForwarder{
				results: []UpstreamResult{
					{
						StatusCode: http.StatusBadRequest,
						Header:     make(http.Header),
						Body:       []byte(`{"error":"safe"}`),
						ClassificationBody: []byte(
							`{"error":"safe"}`,
						),
						RequestWritten: true,
					},
					{
						StatusCode: http.StatusBadRequest,
						Header:     make(http.Header),
						Body:       []byte(`{"error":"safe"}`),
						ClassificationBody: []byte(
							`{"error":"safe"}`,
						),
						RequestWritten: true,
					},
				},
			}
			handler, manager, _ := newHandlerForTest(
				t,
				forwarder,
				providerKey,
			)
			if _, err := manager.Publish(state.CompileInput{
				Groups: []state.GroupConfig{{
					ID:          1,
					Name:        "safe-group",
					UpstreamURL: "http://upstream.invalid",
					Protocols:   []protocol.Protocol{protocol.OpenAI},
					Models:      []state.ModelConfig{{ID: "safe-model"}},
					Enabled:     true,
				}},
				AccessKeys: []state.AccessKeyConfig{{
					ID:      77,
					Name:    "safe-client",
					KeyHash: handler.encryption.Hash(accessKey),
					Status:  state.AccessKeyStatusActive,
				}},
			}); err != nil {
				t.Fatalf("publish secret-matrix snapshot: %v", err)
			}

			var output bytes.Buffer
			logger := logrus.New()
			logger.SetOutput(&output)
			logger.SetFormatter(formatter)
			handler.logger = logger
			engine := gin.New()
			handler.RegisterRoutes(engine)
			serveGatewaySecretMatrixRequests(
				t,
				engine,
				accessKey,
				providerKey,
				authorizationSecret,
				xAPISecret,
				xGoogSecret,
				geminiQuerySecret,
				requestBodySecret,
			)
			assertGatewaySecretMatrixOutput(
				t,
				name+" without hook",
				output.String(),
				accessKey,
				providerKey,
				authorizationSecret,
				xAPISecret,
				xGoogSecret,
				geminiQuerySecret,
				requestBodySecret,
			)

			output.Reset()
			logger.AddHook(redact.NewHook(redact.New()))
			handler.authFailureEvents =
				utils.NewRateLimitedEventCounter(time.Minute, time.Now)
			handler.routeNotFoundEvents =
				utils.NewRateLimitedEventCounter(time.Minute, time.Now)
			serveGatewaySecretMatrixRequests(
				t,
				engine,
				accessKey,
				providerKey,
				authorizationSecret,
				xAPISecret,
				xGoogSecret,
				geminiQuerySecret,
				requestBodySecret,
			)
			assertGatewaySecretMatrixOutput(
				t,
				name+" with hook",
				output.String(),
				accessKey,
				providerKey,
				authorizationSecret,
				xAPISecret,
				xGoogSecret,
				geminiQuerySecret,
				requestBodySecret,
			)
		})
	}
}

func serveGatewaySecretMatrixRequests(
	t *testing.T,
	engine http.Handler,
	accessKey string,
	providerKey string,
	authorizationSecret string,
	xAPISecret string,
	xGoogSecret string,
	geminiQuerySecret string,
	requestBodySecret string,
) {
	target := "/raw-" + requestBodySecret +
		"?key=" + geminiQuerySecret
	invalid := httptest.NewRequest(
		http.MethodPost,
		target,
		strings.NewReader(
			`{"secret":"`+requestBodySecret+`"}`,
		),
	)
	invalid.RemoteAddr = "192.0.2.90:1000"
	invalid.Header.Set(
		"Authorization",
		"Bearer "+authorizationSecret,
	)
	invalid.Header.Set("X-Api-Key", xAPISecret)
	invalid.Header.Set("X-Goog-Api-Key", xGoogSecret)
	engine.ServeHTTP(httptest.NewRecorder(), invalid)

	valid := httptest.NewRequest(
		http.MethodPost,
		target,
		strings.NewReader(
			`{"secret":"`+requestBodySecret+`"}`,
		),
	)
	valid.RemoteAddr = "192.0.2.91:1000"
	valid.Header.Set("Authorization", "Bearer "+accessKey)
	valid.Header.Set("X-Api-Key", xAPISecret)
	valid.Header.Set("X-Goog-Api-Key", xGoogSecret)
	engine.ServeHTTP(httptest.NewRecorder(), valid)

	inference := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(
			`{"model":"safe-model","metadata":"`+
				requestBodySecret+`"}`,
		),
	)
	inference.RemoteAddr = "192.0.2.92:1000"
	inference.Header.Set("Authorization", "Bearer "+accessKey)
	inferenceRecorder := httptest.NewRecorder()
	engine.ServeHTTP(inferenceRecorder, inference)
	for _, surface := range []string{
		inferenceRecorder.Body.String(),
		fmt.Sprint(inferenceRecorder.Header()),
	} {
		for _, forbidden := range []string{
			providerKey,
			providerKey[:4],
			providerKey[len(providerKey)-4:],
			utils.MaskAPIKey(providerKey),
		} {
			if strings.Contains(surface, forbidden) {
				t.Fatalf(
					"inference response contains %q: %s",
					forbidden,
					surface,
				)
			}
		}
	}
}

func assertGatewaySecretMatrixOutput(
	t *testing.T,
	label string,
	logText string,
	accessKey string,
	providerKey string,
	secrets ...string,
) {
	t.Helper()
	for _, required := range []string{
		"data_plane_auth_failed",
		"data_plane_route_not_found",
		"access_key_id",
	} {
		if !strings.Contains(logText, required) {
			t.Fatalf("%s log missing %q: %s", label, required, logText)
		}
	}
	for _, forbidden := range append(
		[]string{
			accessKey,
			providerKey,
			providerKey[:4],
			providerKey[len(providerKey)-4:],
			utils.MaskAPIKey(providerKey),
		},
		secrets...,
	) {
		if strings.Contains(logText, forbidden) {
			t.Fatalf(
				"%s log contains %q: %s",
				label,
				forbidden,
				logText,
			)
		}
	}
}

func TestForwardStripsCookiesAndCredentialHeadersOnEveryPath(t *testing.T) {
	for _, test := range []struct {
		name   string
		stream bool
		status int
	}{
		{name: "nonstream success", status: http.StatusOK},
		{name: "nonstream error", status: http.StatusBadRequest},
		{name: "stream success", stream: true, status: http.StatusOK},
		{name: "stream error", stream: true, status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			const secret = "provider-secret-client-surface"
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Connection", "X-Upstream-Hop")
				writer.Header().Set("X-Upstream-Hop", "drop")
				writer.Header().Set("Authorization", "Bearer unrelated")
				writer.Header().Set("Proxy-Authorization", "unrelated")
				writer.Header().Set("Api-Key", "unrelated")
				writer.Header().Set("X-Api-Key", "unrelated")
				writer.Header().Set("X-Goog-Api-Key", "unrelated")
				writer.Header().Set("X-Echo", "prefix-"+secret)
				writer.Header().Add("Set-Cookie", "session=fake; Secure")
				writer.Header().Add("Set-Cookie2", "legacy=fake; Secure")
				writer.Header().Set("X-Safe", "kept")
				writer.WriteHeader(test.status)
				if test.stream && test.status == http.StatusOK {
					_, _ = writer.Write([]byte("data: ok\n\n"))
					return
				}
				_, _ = writer.Write([]byte(`{"ok":true}`))
			}))
			defer upstream.Close()

			engine, _ := newStreamingGatewayEngine(t, streamGatewayGroup{
				id: 1, name: "header-group", upstreamURL: upstream.URL, apiKey: secret,
			})
			body := `{"model":"gpt-4o"}`
			if test.stream {
				body = `{"model":"gpt-4o","stream":true}`
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if recorder.Code != test.status || recorder.Header().Get("X-Safe") != "kept" {
				t.Fatalf("response = %d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
			}
			for _, name := range []string{
				"Authorization", "Proxy-Authorization", "Api-Key",
				"X-Api-Key", "X-Goog-Api-Key", "X-Echo",
				"Connection", "X-Upstream-Hop", "Set-Cookie", "Set-Cookie2",
			} {
				if recorder.Header().Values(name) != nil {
					t.Fatalf("client response Header %s survived: %#v", name, recorder.Header().Values(name))
				}
			}
			if strings.Contains(fmt.Sprint(recorder.Header()), secret) {
				t.Fatalf("client response headers expose provider key: %v", recorder.Header())
			}
		})
	}
}

func assertNoPlaintextSecrets(t *testing.T, recorder *httptest.ResponseRecorder, logs string, secrets ...string) {
	t.Helper()
	surfaces := []string{recorder.Body.String(), fmt.Sprint(recorder.Header()), logs}
	for _, secret := range secrets {
		if len(secret) <= 16 {
			t.Fatalf("test secret %q is too short to verify masked fragments", secret)
		}
		for _, forbidden := range []string{
			secret,
			secret[:4],
			secret[len(secret)-4:],
			utils.MaskAPIKey(secret),
		} {
			for _, surface := range surfaces {
				if strings.Contains(surface, forbidden) {
					t.Fatalf("gateway exposed provider key fragment %q in %q", forbidden, surface)
				}
			}
		}
	}
}
