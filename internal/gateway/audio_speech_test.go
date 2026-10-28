package gateway

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	bifrostexecutor "gpt-load/internal/execution/bifrost"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestAudioSpeechGatewayKeepsEachChannelNativeFormat(t *testing.T) {
	speechOnly := state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.AudioSpeech: {}}}
	mistralOnly := state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.Mistral: {}}}
	for _, test := range []struct {
		name          string
		channelID     channel.ID
		basePath      string
		upstreamPath  string
		body          string
		upstreamModel string
		upstreamField string
		contentType   string
		encoding      string
		upstream      []byte
		want          []string
		filters       state.FilterSet
		status        int
		calls         int64
	}{
		{
			name: "openrouter binary", channelID: channel.OpenRouter, basePath: "/api", upstreamPath: "/api/v1/audio/speech",
			body:          `{"model":"public","input":"hello","voice":"alloy","response_format":"mp3"}`,
			upstreamModel: "openai/gpt-4o-mini-tts", upstreamField: `"voice":"alloy"`,
			contentType: "audio/mpeg", upstream: []byte("ID3\x04\x00\x00binary-audio"),
			want: []string{"ID3\x04\x00\x00binary-audio"}, filters: speechOnly, status: http.StatusOK, calls: 1,
		},
		{
			name: "openrouter pcm with compression magic", channelID: channel.OpenRouter, basePath: "/api", upstreamPath: "/api/v1/audio/speech",
			body:          `{"model":"public","input":"hello","voice":"alloy","response_format":"pcm"}`,
			upstreamModel: "openai/gpt-4o-mini-tts", upstreamField: `"response_format":"pcm"`,
			contentType: "audio/pcm", upstream: []byte("\x1f\x8b\x00\x00pcm-samples"),
			want: []string{"\x1f\x8b\x00\x00pcm-samples"}, status: http.StatusOK, calls: 1,
		},
		{
			name: "openrouter gzip audio rejected", channelID: channel.OpenRouter, basePath: "/api", upstreamPath: "/api/v1/audio/speech",
			body:          `{"model":"public","input":"hello","voice":"alloy","response_format":"mp3"}`,
			upstreamModel: "openai/gpt-4o-mini-tts", upstreamField: `"voice":"alloy"`,
			contentType: "audio/mpeg", encoding: "gzip", upstream: gzipForTest("ID3\x04\x00\x00binary-audio"),
			filters: speechOnly, status: http.StatusBadGateway, calls: 1,
		},
		{
			name: "mistral json", channelID: channel.Mistral, basePath: "/v1", upstreamPath: "/v1/audio/speech",
			body:          `{"model":"public","input":"hello","voice_id":"gb_jane_neutral"}`,
			upstreamModel: "voxtral-mini-tts-2603", upstreamField: `"voice_id":"gb_jane_neutral"`,
			contentType: "application/json", upstream: []byte(`{"model":"voxtral-mini-tts-2603","audio_data":"UklGRg=="}`),
			want: []string{`"model":"public"`, `"audio_data":"UklGRg=="`}, status: http.StatusOK, calls: 1,
		},
		{
			name: "mistral protocol filter does not grant speech", channelID: channel.Mistral, basePath: "/v1",
			body:          `{"model":"public","input":"hello","voice_id":"gb_jane_neutral"}`,
			upstreamModel: "voxtral-mini-tts-2603", filters: mistralOnly, status: http.StatusServiceUnavailable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
					return
				}
				if request.URL.Path != test.upstreamPath || request.Header.Get("Authorization") != "Bearer sk-upstream" ||
					!bytes.Contains(body, []byte(`"model":"`+test.upstreamModel+`"`)) ||
					!bytes.Contains(body, []byte(test.upstreamField)) {
					t.Errorf("upstream request %s %s", request.URL, body)
				}
				writer.Header().Set("Content-Type", test.contentType)
				if test.encoding != "" {
					writer.Header().Set("Content-Encoding", test.encoding)
				}
				_, _ = writer.Write(test.upstream)
			}))
			defer server.Close()

			runtime, err := bifrostexecutor.NewRuntime(context.Background(), channel.NewRegistry())
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Shutdown()
			sink := &recordingRequestLogSink{}
			engine, handler, manager, _ := newRequestLogHandlerTestRuntime(
				t,
				NewExecutionForwarder(runtime),
				&recordingAccessKeyRPMLimiter{},
				sink,
				"sk-upstream",
			)
			handler.dialects = dialect.NewSet(dialect.NewAudioSpeech())
			params, _ := json.Marshal(map[string]string{"base_url": server.URL + test.basePath})
			_, err = manager.Publish(state.CompileInput{
				ChannelRegistry: channel.NewRegistry(),
				Groups: []state.GroupConfig{{
					ConnectionType: "api_key", ID: 1, Name: "speech", ChannelID: test.channelID,
					Params: params, Models: []state.ModelConfig{{ID: test.upstreamModel, Alias: "public"}}, Enabled: true,
				}},
				Credentials: []state.CredentialConfig{testCredentialConfig(1, 1)},
				AccessKeys: []state.AccessKeyConfig{{
					ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"),
					Status: state.AccessKeyStatusActive, Filters: test.filters,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer gl-client")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.status || calls.Load() != test.calls {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body)
			}
			if test.status != http.StatusOK {
				return
			}
			if response.Header().Get("Content-Type") != test.contentType {
				t.Fatalf("content type = %q, want %q", response.Header().Get("Content-Type"), test.contentType)
			}
			for _, want := range test.want {
				if !strings.Contains(response.Body.String(), want) {
					t.Fatalf("response = %q, missing %q", response.Body, want)
				}
			}
			events := sink.snapshot()
			if len(events) != 1 || events[0].Protocol != protocol.AudioSpeech ||
				events[0].Operation != execution.OperationAudioSpeech {
				t.Fatalf("events = %#v", events)
			}
		})
	}
}

func gzipForTest(data string) []byte {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write([]byte(data))
	_ = writer.Close()
	return compressed.Bytes()
}
