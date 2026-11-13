package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	bifrostexecutor "gpt-load/internal/execution/bifrost"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestAudioSpeechGatewayStreamsBeforeCompletion(t *testing.T) {
	for _, test := range []struct {
		name      string
		channelID channel.ID
		basePath  string
		request   string
		first     string
		last      string
		cancel    bool
		wantError string
	}{
		{
			name: "openai", channelID: channel.OpenAI,
			request: `{"model":"public","input":"hello","voice":"alloy","stream_format":"sse"}`,
			first:   "data: {\"type\":\"speech.audio.delta\",\"audio\":\"aGVsbG8=\"}\n\n",
			last:    "data: {\"type\":\"speech.audio.done\",\"usage\":{\"input_tokens\":3,\"output_tokens\":8,\"total_tokens\":11}}\n\n",
		},
		{
			name: "mistral", channelID: channel.Mistral, basePath: "/v1",
			request: `{"model":"public","input":"hello","voice_id":"gb_jane_neutral","stream":true}`,
			first:   "event: speech.audio.delta\ndata: {\"audio_data\":\"aGVsbG8=\"}\n\n",
			last:    "event: speech.audio.done\ndata: {\"usage\":{\"input_tokens\":3,\"output_tokens\":8}}\n\n",
		},
		{
			name: "openai client cancellation", channelID: channel.OpenAI, cancel: true,
			request: `{"model":"public","input":"hello","voice":"alloy","stream_format":"sse"}`,
			first:   "data: {\"type\":\"speech.audio.delta\",\"audio\":\"aGVsbG8=\"}\n\n",
		},
		{
			name: "untyped error after audio", channelID: channel.OpenAI,
			request:   `{"model":"public","input":"hello","voice":"alloy","stream_format":"sse"}`,
			first:     "data: {\"type\":\"speech.audio.delta\",\"audio\":\"aGVsbG8=\"}\n\n",
			last:      "data: {\"error\":{\"message\":\"speech failed\"}}\n\n",
			wantError: "upstream_sse_error",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			finish := make(chan struct{})
			upstreamCanceled := make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(finish) }) }
			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil || r.URL.Path != "/v1/audio/speech" || r.Header.Get("Authorization") != "Bearer sk-upstream" {
					t.Errorf("upstream request path=%s err=%v", r.URL.Path, err)
				}
				want := strings.Replace(test.request, `"model":"public"`, `"model":"provider-tts"`, 1)
				var gotJSON, wantJSON any
				if json.Unmarshal(body, &gotJSON) != nil || json.Unmarshal([]byte(want), &wantJSON) != nil {
					t.Errorf("invalid upstream JSON: %s", body)
				} else {
					got, _ := json.Marshal(gotJSON)
					want, _ := json.Marshal(wantJSON)
					if !bytes.Equal(got, want) {
						t.Errorf("upstream body=%s, want %s", got, want)
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				// 故意拆开一个事件，验证事件分帧与边生成边返回。
				_, _ = io.WriteString(w, test.first[:len(test.first)/2])
				w.(http.Flusher).Flush()
				_, _ = io.WriteString(w, test.first[len(test.first)/2:])
				w.(http.Flusher).Flush()
				select {
				case <-finish:
					_, _ = io.WriteString(w, test.last)
				case <-r.Context().Done():
					close(upstreamCanceled)
				}
			}))
			defer upstream.Close()
			defer release()
			engine, sink := newAudioSpeechStreamGateway(t, test.channelID, upstream.URL+test.basePath)
			gateway := httptest.NewServer(engine)
			defer gateway.Close()
			defer release()
			request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/audio/speech", strings.NewReader(test.request))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer gl-client")
			type firstResult struct {
				response *http.Response
				reader   *bufio.Reader
				data     string
				err      error
			}
			firstCh := make(chan firstResult, 1)
			go func() {
				response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
				if err != nil {
					firstCh <- firstResult{err: err}
					return
				}
				reader := bufio.NewReader(response.Body)
				var data strings.Builder
				for {
					line, readErr := reader.ReadString('\n')
					data.WriteString(line)
					if readErr != nil || line == "\n" {
						firstCh <- firstResult{response, reader, data.String(), readErr}
						return
					}
				}
			}()
			var first firstResult
			select {
			case first = <-firstCh:
			case <-time.After(3 * time.Second):
				t.Fatal("first audio event was buffered until upstream completion")
			}
			if first.response != nil {
				defer first.response.Body.Close()
			}
			if first.err != nil || first.response == nil || first.response.StatusCode != http.StatusOK || first.data != test.first {
				t.Fatalf("first event=%q err=%v response=%v", first.data, first.err, first.response)
			}
			if !strings.HasPrefix(first.response.Header.Get("Content-Type"), "text/event-stream") {
				t.Fatalf("content type=%q", first.response.Header.Get("Content-Type"))
			}
			if test.cancel {
				if err := first.response.Body.Close(); err != nil {
					t.Fatal(err)
				}
				select {
				case <-upstreamCanceled:
				case <-time.After(3 * time.Second):
					t.Fatal("client cancellation did not cancel the upstream speech stream")
				}
				return
			}
			release()
			tail, err := io.ReadAll(first.reader)
			if err != nil || string(tail) != test.last || calls.Load() != 1 {
				t.Fatalf("tail=%q err=%v calls=%d", tail, err, calls.Load())
			}
			events := sink.snapshot()
			if len(events) != 1 || !events[0].Stream || events[0].ErrorCode != test.wantError || events[0].Operation != execution.OperationAudioSpeech {
				t.Fatalf("request events=%+v", events)
			}
		})
	}
}

func TestAudioSpeechGatewayRejectsUnsupportedNativeStreamModes(t *testing.T) {
	for _, test := range []struct {
		name      string
		channelID channel.ID
		control   string
	}{
		{name: "openai needs stream_format", channelID: channel.OpenAI, control: `"stream":true`},
		{name: "mistral needs stream", channelID: channel.Mistral, control: `"stream_format":"sse"`},
		{name: "openrouter has no SSE route", channelID: channel.OpenRouter, control: `"stream_format":"sse"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer upstream.Close()
			engine, _ := newAudioSpeechStreamGateway(t, test.channelID, upstream.URL)
			request := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"public","input":"hello",`+test.control+`}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || calls.Load() != 0 {
				t.Fatalf("status=%d upstream calls=%d body=%s", response.Code, calls.Load(), response.Body)
			}
		})
	}
}

func TestAudioSpeechGatewayRecordsStreamFailures(t *testing.T) {
	const audio = "data: {\"type\":\"speech.audio.delta\",\"audio\":\"aGVsbG8=\"}\n\n"
	const failed = "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"speech failed\"}}\n\n"
	for _, test := range []struct {
		name   string
		body   string
		status int
	}{
		{name: "missing completion", body: audio, status: http.StatusOK},
		{name: "error after audio", body: audio + failed, status: http.StatusOK},
		{name: "error before audio", body: failed, status: http.StatusBadGateway},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.body)
			}))
			defer upstream.Close()
			engine, sink := newAudioSpeechStreamGateway(t, channel.OpenAI, upstream.URL)
			request := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"public","input":"hello","voice":"alloy","stream_format":"sse"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			events := sink.snapshot()
			if response.Code != test.status || calls.Load() != 1 || len(events) != 1 || events[0].ErrorCode == "" {
				t.Fatalf("status=%d calls=%d events=%+v body=%s", response.Code, calls.Load(), events, response.Body)
			}
			if test.status == http.StatusOK && response.Body.String() != test.body {
				t.Fatalf("stream changed: %q", response.Body.String())
			}
		})
	}
}

func newAudioSpeechStreamGateway(t *testing.T, channelID channel.ID, baseURL string) (*gin.Engine, *recordingRequestLogSink) {
	t.Helper()
	runtime, err := bifrostexecutor.NewRuntime(context.Background(), channel.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Shutdown() })
	sink := &recordingRequestLogSink{}
	engine, handler, manager, _ := newRequestLogHandlerTestRuntime(t, NewExecutionForwarder(runtime), &recordingAccessKeyRPMLimiter{}, sink, "sk-upstream")
	handler.dialects = dialect.NewSet(dialect.NewAudioSpeech())
	params, err := json.Marshal(map[string]string{"base_url": baseURL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ConnectionType: "api_key", ID: 1, Name: "speech", ChannelID: channelID,
			Params: params, Models: []state.ModelConfig{{ID: "provider-tts", Alias: "public"}}, Enabled: true,
		}},
		Credentials: []state.CredentialConfig{testCredentialConfig(1, 1)},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"),
			Status:  state.AccessKeyStatusActive,
			Filters: state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.AudioSpeech: {}}},
		}},
	})
	if err != nil {
		t.Fatalf("publish speech test config: %v", err)
	}
	return engine, sink
}
