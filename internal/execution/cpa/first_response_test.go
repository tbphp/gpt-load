package cpa

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/subscription/providers/claude"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestConvertedCodexSamplesMetadataBeforeCPAConversion(t *testing.T) {
	canonical := credentialJSON("access", "refresh", time.Now().Add(time.Hour))
	adapter := NewAdapter(nil, channel.NewRegistry())
	adapter.credentials = &fakeCredentialPreparer{credential: subscriptionruntime.NewCredential(canonical, "timing-account", subscriptionruntime.Account{}, time.Time{}, false, nil)}
	spec := execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID: "timing-request", AttemptID: "timing-attempt", Sequence: 1,
		ChannelID: string(channel.Codex), RouteMode: execution.RouteConverted,
		ClientProtocol: protocol.Anthropic, Operation: execution.OperationChatCompletion,
		ClientModel: "gpt-5.6-luna", UpstreamModel: "gpt-5.6-luna", Method: http.MethodPost, Path: "/v1/messages",
		Body:       []byte(`{"model":"gpt-5.6-luna","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
		Credential: execution.NewCredentialSnapshot(1, 1, 1, canonical),
	})
	observed := make(chan struct{})
	reader, writer := io.Pipe()
	defer reader.Close()
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}, nil
	})
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
	ctx, stop := execution.WithFirstResponseObserver(ctx, func() { close(observed) })
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer writer.Close()
		_, _ = io.WriteString(writer, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_timing\",\"object\":\"response\",\"model\":\"gpt-5.6-luna\"}}\n")
		select {
		case <-observed:
		case <-time.After(time.Second):
			t.Error("upstream metadata was held for translation")
		}
		_, _ = io.WriteString(writer, "\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_timing\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"gpt-5.6-luna\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
	}()
	result := adapter.ExecuteStream(ctx, spec, func(execution.StreamEvent) error { return nil })
	_ = reader.Close()
	<-done
	if result.Error != nil {
		t.Fatalf("stream: %+v", result.Error)
	}
}

func TestCompressedObservationPreservesWireBytesAndErrors(t *testing.T) {
	for _, encoding := range []string{"gzip", "deflate", "br", "zstd", "invalid"} {
		t.Run(encoding, func(t *testing.T) {
			var wire bytes.Buffer
			var writer io.WriteCloser
			switch encoding {
			case "gzip":
				writer = gzip.NewWriter(&wire)
			case "deflate":
				writer = zlib.NewWriter(&wire)
			case "br":
				writer = brotli.NewWriter(&wire)
			case "zstd":
				value, err := zstd.NewWriter(&wire)
				if err != nil {
					t.Fatal(err)
				}
				writer = value
			case "invalid":
				wire.WriteString("not compressed data")
			}
			if writer != nil {
				if _, err := io.WriteString(writer, "data: {\"type\":\"response.created\"}\n\n"); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			ctx, stop := execution.WithFirstResponseObserver(t.Context(), func() { calls++ })
			defer stop()
			body := observeStreamBody(ctx, io.NopCloser(bytes.NewReader(wire.Bytes())), encoding)
			got, err := io.ReadAll(body)
			closeErr := body.Close()
			if err != nil || closeErr != nil || !bytes.Equal(got, wire.Bytes()) {
				t.Fatalf("wire changed: read=%v close=%v", err, closeErr)
			}
			want := 1
			if encoding == "invalid" {
				want = 0
			}
			if calls != want {
				t.Fatalf("observations = %d, want %d", calls, want)
			}
		})
	}
}

func TestCompressedClaudeSamplesMetadataBeforeConversion(t *testing.T) {
	canonical, err := claude.MarshalCredential(claudeProviderCredentialForTest(t).value)
	if err != nil {
		t.Fatal(err)
	}
	adapter, _, _, keyService, row := newSubscriptionAdapterFixture(t, string(channel.Claude), canonical, "timing-account")
	spec := validClaudeSpec(t, row, keyService)
	spec.RouteMode, spec.ClientProtocol, spec.Path = execution.RouteConverted, protocol.OpenAICompletions, "/v1/chat/completions"
	reader, writer := io.Pipe()
	defer reader.Close()
	observed := make(chan struct{})
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}, "Content-Encoding": {"gzip"}}, Body: reader}, nil
	})
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
	ctx, stop := execution.WithFirstResponseObserver(ctx, func() { close(observed) })
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer writer.Close()
		compressed := gzip.NewWriter(writer)
		defer compressed.Close()
		_, _ = io.WriteString(compressed, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n")
		_ = compressed.Flush()
		select {
		case <-observed:
		case <-time.After(time.Second):
			t.Error("compressed upstream metadata was not observed before conversion")
		}
		_, _ = io.WriteString(compressed, "\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}()
	result := adapter.ExecuteStream(ctx, spec, func(execution.StreamEvent) error { return nil })
	_ = reader.Close()
	<-done
	if result.Error != nil {
		t.Fatalf("stream: %+v", result.Error)
	}
}
