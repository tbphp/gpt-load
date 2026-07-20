package gateway

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/platform/redact"
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
}

func TestGatewayNeverExposesProviderKeyInResponseHeaders(t *testing.T) {
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
				writer.Header().Set("Authorization", "Bearer unrelated")
				writer.Header().Set("Proxy-Authorization", "unrelated")
				writer.Header().Set("Api-Key", "unrelated")
				writer.Header().Set("X-Api-Key", "unrelated")
				writer.Header().Set("X-Goog-Api-Key", "unrelated")
				writer.Header().Set("X-Echo", "prefix-"+secret)
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
		for _, surface := range surfaces {
			if strings.Contains(surface, secret) {
				t.Fatalf("gateway exposed plaintext secret %q in %q", secret, surface)
			}
		}
	}
}
