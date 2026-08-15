package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/testutil/fakeupstream"
)

func TestHandlerStreamsFakeUpstreamAndRetriesBeforeCommit(t *testing.T) {
	t.Run("valid fixture", func(t *testing.T) {
		upstream := fakeupstream.New(fakeupstream.Step{
			Status: http.StatusOK, Fixture: "openai/stream.sse", Stream: true,
		})
		defer upstream.Close()

		engine, _ := newStreamingGatewayEngine(t, streamGatewayGroup{
			id: 1, name: "openai", upstreamURL: upstream.URL, apiKey: "sk-stream-one",
		})
		recorder := performStreamingRequest(engine)

		want := openAIStreamFixture(t)
		if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), want) || !recorder.Flushed {
			t.Fatalf("response = %d flushed=%t body=%q, want flushed fixture", recorder.Code, recorder.Flushed, recorder.Body.Bytes())
		}
		requests := upstream.Requests()
		if len(requests) != 1 {
			t.Fatalf("upstream requests = %d, want 1", len(requests))
		}
		if got := requests[0].Headers.Get("Accept-Encoding"); got != "identity" {
			t.Fatalf("Accept-Encoding = %q, want identity", got)
		}
		if got := requests[0].Headers.Get("Authorization"); got != "Bearer sk-stream-one" {
			t.Fatalf("Authorization = %q", got)
		}
	})

	t.Run("retryable response then valid fixture", func(t *testing.T) {
		upstream := fakeupstream.New(
			fakeupstream.Step{Status: http.StatusTooManyRequests, Fixture: "openai/429.json"},
			fakeupstream.Step{Status: http.StatusOK, Fixture: "openai/stream.sse", Stream: true},
		)
		defer upstream.Close()

		engine, _ := newStreamingGatewayEngine(t,
			streamGatewayGroup{id: 1, name: "openai", upstreamURL: upstream.URL, apiKey: "sk-stream-one"},
			streamGatewayGroup{id: 2, name: "openai-backup", upstreamURL: upstream.URL, apiKey: "sk-stream-two"},
		)
		recorder := performStreamingRequest(engine)

		if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), openAIStreamFixture(t)) {
			t.Fatalf("response = %d %q", recorder.Code, recorder.Body.Bytes())
		}
		requests := upstream.Requests()
		if len(requests) != 2 {
			t.Fatalf("upstream requests = %d, want 2", len(requests))
		}
		if first, second := requests[0].Headers.Get("Authorization"), requests[1].Headers.Get("Authorization"); first == second {
			t.Fatalf("retry reused credential %q", first)
		}
	})
}

func TestHandlerTerminatesAliasedNonObjectProviderErrorWithoutReplay(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("Retry-After", "1")
		_, _ = io.WriteString(
			writer,
			"event: error\ndata: rate_limit_error provider-model\n\n",
		)
	}))
	defer upstream.Close()

	engine, _ := newStreamingGatewayEngine(
		t,
		streamGatewayGroup{
			id: 1, name: "first", upstreamURL: upstream.URL,
			apiKey: "sk-obviously-fake-first", modelID: "provider-model", alias: "gpt-4o",
		},
		streamGatewayGroup{
			id: 2, name: "second", upstreamURL: upstream.URL,
			apiKey: "sk-obviously-fake-second", modelID: "provider-model", alias: "gpt-4o",
		},
	)
	recorder := performStreamingRequest(engine)

	if recorder.Code != http.StatusBadGateway ||
		!strings.Contains(recorder.Body.String(), reasonUpstreamProtocol.Code) ||
		requests.Load() != 1 {
		t.Fatalf("response/requests = %d/%d body=%s",
			recorder.Code, requests.Load(), recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "rate_limit_error") ||
		strings.Contains(recorder.Body.String(), "provider-model") {
		t.Fatalf("provider payload reached downstream: %s", recorder.Body.String())
	}
}

func TestHandlerStreamRetryUsesEachGroupsInjectUsageSetting(t *testing.T) {
	first := false
	second := true
	upstream := fakeupstream.New(
		fakeupstream.Step{Status: http.StatusTooManyRequests, Fixture: "openai/429.json"},
		fakeupstream.Step{Status: http.StatusOK, Fixture: "openai/stream.sse", Stream: true},
	)
	defer upstream.Close()

	engine, _ := newStreamingGatewayEngine(t,
		streamGatewayGroup{id: 1, name: "first", upstreamURL: upstream.URL, apiKey: "sk-first", injectUsageOptions: &first},
		streamGatewayGroup{id: 2, name: "second", upstreamURL: upstream.URL, apiKey: "sk-second", injectUsageOptions: &second},
	)
	recorder := performStreamingRequest(engine)
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d body=%q", recorder.Code, recorder.Body.Bytes())
	}
	requests := upstream.Requests()
	if len(requests) != 2 {
		t.Fatalf("upstream requests = %d, want 2", len(requests))
	}
	for index, wantInjected := range []bool{false, true} {
		var body map[string]any
		if err := json.Unmarshal(requests[index].Body, &body); err != nil {
			t.Fatalf("decode upstream request %d: %v", index+1, err)
		}
		_, injected := body["stream_options"]
		if injected != wantInjected {
			t.Fatalf("attempt %d stream_options present = %t, want %t; body=%#v", index+1, injected, wantInjected, body)
		}
	}
}

func TestHandlerStreamingDebugHeadersRejectUpstreamSpoofing(t *testing.T) {
	upstream := fakeupstream.New(fakeupstream.Step{
		Status: http.StatusOK, Fixture: "openai/stream.sse", Stream: true,
		Headers: http.Header{
			"X-GPTLoad-Group":    {"spoofed-group"},
			"X-GPTLoad-Key":      {"sk-spoofed-plaintext"},
			"X-GPTLoad-Attempts": {"999"},
		},
	})
	defer upstream.Close()

	engine, _ := newStreamingGatewayEngine(t, streamGatewayGroup{
		id: 1, name: "stream-group", upstreamURL: upstream.URL, apiKey: "sk-real-stream-key",
	})
	recorder := performStreamingRequest(engine)

	assertDebugHeaders(t, recorder.Header(), "stream-group", "1")
	if strings.Contains(recorder.Body.String(), "sk-real-stream-key") || strings.Contains(recorder.Body.String(), "sk-spoofed-plaintext") {
		t.Fatalf("stream response leaked a plaintext key: %s", recorder.Body.String())
	}
}

func TestHandlerTreatsSDKNormalizedCompressedStreamAsSingleAttempt(t *testing.T) {
	t.Run("normalized response does not blame credential or retry", func(t *testing.T) {
		compressed := fakeupstream.New(
			fakeupstream.Step{
				Status: http.StatusOK, Fixture: "openai/stream.sse", Stream: true,
				Headers: http.Header{"Content-Encoding": {"gzip"}},
			},
			fakeupstream.Step{Status: http.StatusOK, Fixture: "openai/stream.sse", Stream: true},
		)
		defer compressed.Close()
		backup := fakeupstream.New(fakeupstream.Step{
			Status: http.StatusOK, Fixture: "openai/stream.sse", Stream: true,
		})
		defer backup.Close()

		engine, registry := newStreamingGatewayEngine(t,
			streamGatewayGroup{id: 1, name: "compressed", upstreamURL: compressed.URL, apiKey: "sk-compressed"},
			streamGatewayGroup{id: 2, name: "backup", upstreamURL: backup.URL, apiKey: "sk-backup"},
		)
		first := performStreamingRequest(engine)
		if first.Code != http.StatusOK || !bytes.Equal(first.Body.Bytes(), openAIStreamFixture(t)) {
			t.Fatalf("first response = %d %q", first.Code, first.Body.Bytes())
		}
		if len(compressed.Requests()) != 1 || len(backup.Requests()) != 0 {
			t.Fatalf("first request counts = compressed:%d backup:%d", len(compressed.Requests()), len(backup.Requests()))
		}
		if candidates := registry.CollectCredentialCandidates([]uint{1, 2}, nil, time.Time{}); len(candidates) != 2 {
			t.Fatalf("SDK-normalized response changed credential registry: %#v", candidates)
		}

		second := performStreamingRequest(engine)
		if second.Code != http.StatusOK || len(compressed.Requests()) != 2 || len(backup.Requests()) != 0 {
			t.Fatalf("second response/counts = %d compressed:%d backup:%d", second.Code, len(compressed.Requests()), len(backup.Requests()))
		}
	})

	t.Run("multiple candidates still produce one logical attempt", func(t *testing.T) {
		first := fakeupstream.New(fakeupstream.Step{
			Status: http.StatusOK, Fixture: "openai/stream.sse", Stream: true,
			Headers: http.Header{"Content-Encoding": {"gzip"}},
		})
		defer first.Close()
		second := fakeupstream.New(fakeupstream.Step{
			Status: http.StatusOK, Fixture: "openai/stream.sse", Stream: true,
			Headers: http.Header{"Content-Encoding": {"br"}},
		})
		defer second.Close()

		engine, _ := newStreamingGatewayEngine(t,
			streamGatewayGroup{id: 1, name: "compressed-a", upstreamURL: first.URL, apiKey: "sk-plain-a"},
			streamGatewayGroup{id: 2, name: "compressed-b", upstreamURL: second.URL, apiKey: "sk-plain-b"},
		)
		recorder := performStreamingRequest(engine)
		if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), openAIStreamFixture(t)) ||
			len(first.Requests())+len(second.Requests()) != 1 {
			t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
		}
		for _, forbidden := range []string{"sk-plain-a", "sk-plain-b"} {
			if strings.Contains(recorder.Body.String(), forbidden) {
				t.Fatalf("protocol response exposed %q: %s", forbidden, recorder.Body.String())
			}
		}
	})
}

func TestHandlerStreamsProgressively(t *testing.T) {
	const (
		first  = "data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"first\"},\"finish_reason\":null}]}\n\n"
		second = "data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	)
	firstEventSent := make(chan struct{})
	releaseSecondEvent := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseSecondEvent) }) })

	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(first))
		writer.(http.Flusher).Flush()
		close(firstEventSent)
		select {
		case <-releaseSecondEvent:
			_, _ = writer.Write([]byte(second))
			writer.(http.Flusher).Flush()
		case <-request.Context().Done():
		}
	}))
	defer upstream.Close()

	engine, _ := newStreamingGatewayEngine(t, streamGatewayGroup{
		id: 1, name: "progressive", upstreamURL: upstream.URL, apiKey: "sk-progressive",
	})
	gatewayServer := httptest.NewServer(engine)
	defer gatewayServer.Close()

	request, err := http.NewRequest(http.MethodPost, gatewayServer.URL+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","stream":true}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer gl-client")
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("stream request error = %v", err)
	}
	defer response.Body.Close()

	select {
	case <-firstEventSent:
	case <-time.After(time.Second):
		t.Fatal("upstream did not send first event")
	}
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read first data line: %v", err)
	}
	blank, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read first event boundary: %v", err)
	}
	if line+blank != first {
		t.Fatalf("first progressive event = %q", line+blank)
	}

	releaseOnce.Do(func() { close(releaseSecondEvent) })
	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read remaining stream: %v", err)
	}
	if string(rest) != second {
		t.Fatalf("remaining stream = %q", rest)
	}
}

func TestAliasedStreamRemainsProgressive(t *testing.T) {
	firstEventSent := make(chan struct{})
	releaseSecondEvent := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseSecondEvent) }) })

	first := "data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"model\":\"provider-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"first\"},\"finish_reason\":null}]}\n\n"
	second := "data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"model\":\"provider-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		setRepresentationMetadata(writer.Header())
		_, _ = writer.Write([]byte(first))
		writer.(http.Flusher).Flush()
		close(firstEventSent)
		select {
		case <-releaseSecondEvent:
			_, _ = writer.Write([]byte(second))
			writer.(http.Flusher).Flush()
		case <-request.Context().Done():
		}
	}))
	defer upstream.Close()

	engine, _ := newStreamingGatewayEngine(t, streamGatewayGroup{
		id: 1, name: "alias-progressive", upstreamURL: upstream.URL, apiKey: "sk-progressive",
		modelID: "provider-model", alias: "public-model",
	})
	gatewayServer := httptest.NewServer(engine)
	defer gatewayServer.Close()

	request, err := http.NewRequest(http.MethodPost, gatewayServer.URL+"/v1/chat/completions", strings.NewReader(`{"model":"public-model","stream":true}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer gl-client")
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("stream request error = %v", err)
	}
	defer response.Body.Close()

	select {
	case <-firstEventSent:
	case <-time.After(time.Second):
		t.Fatal("upstream did not send first event")
	}
	reader := bufio.NewReader(response.Body)
	firstLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read rewritten first data line: %v", err)
	}
	blank, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read rewritten first boundary: %v", err)
	}
	if got := firstLine + blank; !strings.Contains(got, `"model":"public-model"`) || strings.Contains(got, `"model":"provider-model"`) {
		t.Fatalf("first progressive alias event = %q", got)
	}
	if response.Header.Get("Content-Length") != "" {
		t.Fatalf("stream Content-Length = %q, want removed", response.Header.Get("Content-Length"))
	}
	assertRepresentationMetadata(t, response.Header, false)

	releaseOnce.Do(func() { close(releaseSecondEvent) })
	rest, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(rest), `"model":"public-model"`) ||
		strings.Contains(string(rest), `"model":"provider-model"`) ||
		!strings.HasSuffix(string(rest), "data: [DONE]\n\n") {
		t.Fatalf("remaining stream = %q, %v", rest, err)
	}
}

func TestHandlerStreamFirstEventTimeout(t *testing.T) {
	t.Run("request-written partial event times out without backup", func(t *testing.T) {
		finished := make(chan struct{})
		partial := newPartialStreamServer(finished)
		defer partial.Close()
		backup := fakeupstream.New(fakeupstream.Step{
			Status: http.StatusOK, Fixture: "openai/stream.sse", Stream: true,
		})
		defer backup.Close()

		engine, _ := newStreamingGatewayEngine(t,
			streamGatewayGroup{id: 1, name: "partial", upstreamURL: partial.URL, apiKey: "sk-partial", firstByte: 40 * time.Millisecond},
			streamGatewayGroup{id: 2, name: "backup", upstreamURL: backup.URL, apiKey: "sk-backup", firstByte: 200 * time.Millisecond},
		)
		recorder := performStreamingRequest(engine)

		if recorder.Code != http.StatusGatewayTimeout ||
			!strings.Contains(recorder.Body.String(), reasonUpstreamTimeout.Code) {
			t.Fatalf("response = %d %q", recorder.Code, recorder.Body.Bytes())
		}
		waitForStreamSignal(t, finished, "finite partial upstream completion")
		if bytes.Contains(recorder.Body.Bytes(), []byte("partial")) || len(backup.Requests()) != 0 {
			t.Fatalf("partial event leaked or backup not used: body=%q backup=%d", recorder.Body.Bytes(), len(backup.Requests()))
		}
	})

	t.Run("first partial candidate returns timeout without retry", func(t *testing.T) {
		firstFinished := make(chan struct{})
		first := newPartialStreamServer(firstFinished)
		defer first.Close()
		secondFinished := make(chan struct{})
		second := newPartialStreamServer(secondFinished)
		defer second.Close()

		engine, _ := newStreamingGatewayEngine(t,
			streamGatewayGroup{id: 1, name: "partial-a", upstreamURL: first.URL, apiKey: "sk-a", firstByte: 30 * time.Millisecond},
			streamGatewayGroup{id: 2, name: "partial-b", upstreamURL: second.URL, apiKey: "sk-b", firstByte: 30 * time.Millisecond},
		)
		recorder := performStreamingRequest(engine)
		waitForStreamSignal(t, firstFinished, "first finite partial upstream completion")
		select {
		case <-secondFinished:
			t.Fatal("second partial upstream was unexpectedly attempted")
		default:
		}

		var body struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if recorder.Code != http.StatusGatewayTimeout || body.Code != reasonUpstreamTimeout.Code || strings.Contains(recorder.Body.String(), "partial") {
			t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestHandlerStreamIdleAndDisconnectNeverRetry(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		idle    time.Duration
		want    string
	}{
		{
			name: "idle after commit",
			idle: 35 * time.Millisecond,
			handler: func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = writer.Write([]byte("data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"first\"},\"finish_reason\":null}]}\n\n"))
				writer.(http.Flusher).Flush()
				// Bifrost Core v1.7.7 can finish the logical stream without
				// synchronously closing the underlying fasthttp connection.
				// Keep the fixture finite while still giving GPT-Load's idle
				// timeout enough time to stop the downstream stream.
				select {
				case <-request.Context().Done():
				case <-time.After(200 * time.Millisecond):
				}
			},
			want: "data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"first\"},\"finish_reason\":null}]}\n\n",
		},
		{
			name: "abrupt EOF after commit",
			idle: time.Second,
			handler: func(writer http.ResponseWriter, _ *http.Request) {
				// Close a chunked response without its terminating zero chunk so
				// the complete first SSE event is observable before the transport
				// reports an unexpected EOF. With a mismatched Content-Length,
				// Bifrost Core v1.7.7 rejects the response before exposing data.
				connection, buffered, err := writer.(http.Hijacker).Hijack()
				if err != nil {
					return
				}
				defer connection.Close()
				payload := "data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"first\"},\"finish_reason\":null}]}\n\n"
				_, _ = fmt.Fprintf(buffered, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n%x\r\n%s\r\n", len(payload), payload)
				_ = buffered.Flush()
			},
			want: "data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"first\"},\"finish_reason\":null}]}\n\n",
		},
		{
			name: "activity resets idle deadline",
			idle: 120 * time.Millisecond,
			handler: func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				for _, event := range []string{
					"data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"one\"},\"finish_reason\":null}]}\n\n",
					"data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"two\"},\"finish_reason\":null}]}\n\n",
					"data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
				} {
					_, _ = writer.Write([]byte(event))
					writer.(http.Flusher).Flush()
					select {
					case <-time.After(40 * time.Millisecond):
					case <-request.Context().Done():
						return
					}
				}
			},
			want: "data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"one\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"two\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			primary := httptest.NewServer(tt.handler)
			defer primary.Close()
			backup := fakeupstream.New(fakeupstream.Step{
				Status: http.StatusOK, Fixture: "openai/stream.sse", Stream: true,
			})
			defer backup.Close()

			engine, _ := newStreamingGatewayEngine(t,
				streamGatewayGroup{id: 1, name: "primary", upstreamURL: primary.URL, apiKey: "sk-primary", streamIdle: tt.idle},
				streamGatewayGroup{id: 2, name: "backup", upstreamURL: backup.URL, apiKey: "sk-backup", streamIdle: time.Second},
			)
			recorder := performStreamingRequest(engine)

			if recorder.Code != http.StatusOK || recorder.Body.String() != tt.want {
				t.Fatalf("response = %d %q, want %q", recorder.Code, recorder.Body.String(), tt.want)
			}
			if len(backup.Requests()) != 0 {
				t.Fatalf("committed stream retried backup %d times", len(backup.Requests()))
			}
		})
	}
}

func TestHandlerStopsAfterDownstreamCancellationWithoutRetry(t *testing.T) {
	upstreamStarted := make(chan struct{})
	upstreamFinished := make(chan struct{})
	primary := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		close(upstreamStarted)
		select {
		case <-request.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
		close(upstreamFinished)
	}))
	defer primary.Close()
	backup := fakeupstream.New(fakeupstream.Step{
		Status: http.StatusOK, Fixture: "openai/stream.sse", Stream: true,
	})
	defer backup.Close()

	engine, _ := newStreamingGatewayEngine(t,
		streamGatewayGroup{id: 1, name: "primary", upstreamURL: primary.URL, apiKey: "sk-primary"},
		streamGatewayGroup{id: 2, name: "backup", upstreamURL: backup.URL, apiKey: "sk-backup"},
	)
	gatewayServer := httptest.NewServer(engine)
	defer gatewayServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, gatewayServer.URL+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","stream":true}`))
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer gl-client")
	done := make(chan error, 1)
	go func() {
		response, doErr := http.DefaultClient.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		done <- doErr
	}()

	waitForStreamSignal(t, upstreamStarted, "primary request start")
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("downstream cancellation returned nil error")
		}
	case <-time.After(time.Second):
		t.Fatal("downstream request did not stop after cancellation")
	}
	waitForStreamSignal(t, upstreamFinished, "finite upstream completion")
	if len(backup.Requests()) != 0 {
		t.Fatalf("downstream cancellation retried backup %d times", len(backup.Requests()))
	}
}

func TestStreamWriteDeadlineStopsRealTCPSlowReader(t *testing.T) {
	type writeResult struct {
		operation string
		err       error
	}
	done := make(chan writeResult, 1)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/", func(ginContext *gin.Context) {
		controlled := newStreamWriteController(ginContext.Writer, 25*time.Millisecond)
		defer func() { _ = controlled.clear() }()
		if err := controlled.writeHeader(http.StatusOK); err != nil {
			done <- writeResult{operation: "write header", err: err}
			return
		}
		chunk := bytes.Repeat([]byte("x"), 1024)
		for {
			if _, err := controlled.write(chunk); err != nil {
				done <- writeResult{operation: "write", err: err}
				return
			}
			if err := controlled.flush(); err != nil {
				done <- writeResult{operation: "flush", err: err}
				return
			}
		}
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: engine}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(&smallWriteBufferListener{Listener: listener})
	}()

	var client net.Conn
	var responseBody io.ReadCloser
	t.Cleanup(func() {
		if client != nil {
			_ = client.Close()
		}
		if responseBody != nil {
			_ = responseBody.Close()
		}
		_ = server.Close()
		_ = listener.Close()
		select {
		case serveErr := <-serveDone:
			if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				t.Errorf("server.Serve() error = %v", serveErr)
			}
		case <-time.After(time.Second):
			t.Error("server.Serve() did not stop during cleanup")
		}
	})

	client, err = net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if tcp, ok := client.(*net.TCPConn); ok {
		_ = tcp.SetReadBuffer(1024)
	}
	if _, err := fmt.Fprintf(client,
		"GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n",
		listener.Addr().String(),
	); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(client), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	responseBody = response.Body

	select {
	case result := <-done:
		if result.err == nil {
			t.Fatal("handler error = nil")
		}
		if result.operation != "flush" {
			t.Fatalf("deadline error operation = %q, want flush: %v", result.operation, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("slow reader did not trigger the sliding write deadline")
	}
}

func TestBufferedWriteDeadlineStopsRealTCPSlowReader(t *testing.T) {
	done := make(chan error, 1)
	handler := &Handler{writeTimeout: 25 * time.Millisecond}
	body := bytes.Repeat([]byte("x"), int(maxNonStreamingResponseBodyBytes))
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/", func(ginContext *gin.Context) {
		done <- handler.writeUpstreamResponse(ginContext, UpstreamResult{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Length": {strconv.Itoa(len(body))},
				"Content-Type":   {"application/octet-stream"},
			},
			Body: body,
		})
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: engine}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(&smallWriteBufferListener{Listener: listener})
	}()

	var client net.Conn
	var responseBody io.ReadCloser
	t.Cleanup(func() {
		if client != nil {
			_ = client.Close()
		}
		if responseBody != nil {
			_ = responseBody.Close()
		}
		_ = server.Close()
		_ = listener.Close()
		select {
		case serveErr := <-serveDone:
			if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				t.Errorf("server.Serve() error = %v", serveErr)
			}
		case <-time.After(time.Second):
			t.Error("server.Serve() did not stop during cleanup")
		}
	})

	client, err = net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if tcp, ok := client.(*net.TCPConn); ok {
		_ = tcp.SetReadBuffer(1024)
	}
	if _, err := fmt.Fprintf(client,
		"GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n",
		listener.Addr().String(),
	); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(client), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	responseBody = response.Body

	select {
	case writeErr := <-done:
		if writeErr == nil {
			t.Fatal("writeUpstreamResponse() error = nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("slow reader did not trigger the buffered write deadline")
	}
}

func TestBufferedWriteDeadlineStopsEmptyResponseWithLargeHeaderSlowReader(t *testing.T) {
	done := make(chan error, 1)
	handler := &Handler{writeTimeout: 25 * time.Millisecond}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/", func(ginContext *gin.Context) {
		done <- handler.writeUpstreamResponse(ginContext, UpstreamResult{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"X-Large": {strings.Repeat("x", 2<<20)},
			},
		})
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: engine}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(&smallWriteBufferListener{Listener: listener})
	}()

	var client net.Conn
	t.Cleanup(func() {
		if client != nil {
			_ = client.Close()
		}
		_ = server.Close()
		_ = listener.Close()
		select {
		case serveErr := <-serveDone:
			if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				t.Errorf("server.Serve() error = %v", serveErr)
			}
		case <-time.After(time.Second):
			t.Error("server.Serve() did not stop during cleanup")
		}
	})

	client, err = net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if tcp, ok := client.(*net.TCPConn); ok {
		_ = tcp.SetReadBuffer(1024)
	}
	if _, err := fmt.Fprintf(client,
		"GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n",
		listener.Addr().String(),
	); err != nil {
		t.Fatal(err)
	}

	select {
	case writeErr := <-done:
		if writeErr == nil {
			t.Fatal("writeUpstreamResponse() error = nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("slow reader did not trigger the empty buffered response write deadline")
	}
}

type smallWriteBufferListener struct{ net.Listener }

func (listener *smallWriteBufferListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err == nil {
		if tcp, ok := connection.(*net.TCPConn); ok {
			_ = tcp.SetWriteBuffer(1024)
		}
	}
	return connection, err
}

type streamGatewayGroup struct {
	id                 uint
	name               string
	upstreamURL        string
	apiKey             string
	modelID            string
	alias              string
	firstByte          time.Duration
	streamIdle         time.Duration
	injectUsageOptions *bool
}

func newStreamingGatewayEngine(t *testing.T, groups ...streamGatewayGroup) (*gin.Engine, *state.CredentialRegistry) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	keyService, err := encryption.NewService("stream-handler-test-master-key")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	groupConfigs := make([]state.GroupConfig, 0, len(groups))
	entries := make([]state.CredentialEntry, 0, len(groups))
	credentialConfigs := make([]state.CredentialConfig, 0, len(groups))
	for index, group := range groups {
		modelID := group.modelID
		if modelID == "" {
			modelID = "gpt-4o"
		}
		baseURL := testUpstreamBaseURL(group.upstreamURL, protocol.OpenAICompletions)
		channelID, params := testChannelConfig(t, protocol.OpenAICompletions, baseURL)
		groupConfigs = append(groupConfigs, state.GroupConfig{ConnectionType: "api_key", ID: group.id, Name: group.name, ChannelID: channelID, Params: params,
			Models: []state.ModelConfig{{ID: modelID, Alias: group.alias}}, Enabled: true,
			Settings: streamGatewaySettings(group.injectUsageOptions),
		})
		credentialID := uint(index + 1)
		entries = append(entries, testCredentialEntry(t, keyService, credentialID, group.id, group.apiKey))
		credentialConfigs = append(credentialConfigs, testCredentialConfig(credentialID, group.id))
	}

	manager := state.NewManager()
	snapshot, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(), Groups: groupConfigs,
		Credentials: credentialConfigs,
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"),
			Status: state.AccessKeyStatusActive,
		}},
	})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	for _, group := range groups {
		view := snapshot.Groups[group.id]
		if group.firstByte > 0 {
			view.Timeouts.FirstByte = group.firstByte
		}
		if group.streamIdle > 0 {
			view.Timeouts.StreamIdle = group.streamIdle
		}
		snapshot.Groups[group.id] = view
	}

	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatalf("ReplaceCredentials() error = %v", err)
	}
	handler := NewHandler(
		manager,
		registry,
		keyService,
		newTestExecutionForwarder(t),
		dialect.NewSet(dialect.NewOpenAI()),
		health.NewStatsStore(),
		health.NewMutationCoordinator(),
		nil,
		nil,
		nil,
	)
	handler.newRandom = func() *rand.Rand { return rand.New(zeroSource{}) }
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	return engine, registry
}

func streamGatewaySettings(injectUsageOptions *bool) config.Settings {
	if injectUsageOptions == nil {
		return nil
	}
	return config.Settings{state.SettingInjectUsageOptions: *injectUsageOptions}
}

func performStreamingRequest(engine *gin.Engine) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","stream":true}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func openAIStreamFixture(t *testing.T) []byte {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	fixture, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "testutil", "fakeupstream", "testdata", "openai", "stream.sse"))
	if err != nil {
		t.Fatalf("read OpenAI stream fixture: %v", err)
	}
	return fixture
}

func newPartialStreamServer(finished chan<- struct{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: partial\n"))
		writer.(http.Flusher).Flush()
		// Bifrost Core v1.7.7 does not guarantee physical socket closure
		// after caller cancellation, so keep this fixture finite and assert
		// only GPT-Load's logical timeout/commit/retry contract above.
		select {
		case <-request.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
		close(finished)
	}))
}

func waitForStreamSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

type zeroSource struct{}

func (zeroSource) Int63() int64 { return 0 }
func (zeroSource) Seed(int64)   {}

var _ rand.Source = zeroSource{}
