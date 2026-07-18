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
