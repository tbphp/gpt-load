package bifrost

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestAudioSpeechKeepsEachChannelNativeFormat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		channelID   channel.ID
		basePath    string
		wantPath    string
		body        string
		model       string
		wantFields  []string
		contentType string
		response    []byte
	}{
		{
			name: "mistral json", channelID: channel.Mistral, basePath: "/v1", wantPath: "/v1/audio/speech",
			body:  `{"model":"public","input":"hello","voice_id":"gb_jane_neutral"}`,
			model: "voxtral-mini-tts-2603", wantFields: []string{`"model":"voxtral-mini-tts-2603"`, `"voice_id":"gb_jane_neutral"`},
			contentType: "application/json", response: []byte(`{"audio_data":"UklGRg=="}`),
		},
		{
			name: "openrouter binary", channelID: channel.OpenRouter, basePath: "/proxy/api", wantPath: "/proxy/api/v1/audio/speech",
			body:  `{"model":"public","input":"hello","voice":"alloy","response_format":"mp3"}`,
			model: "openai/gpt-4o-mini-tts", wantFields: []string{`"model":"openai/gpt-4o-mini-tts"`, `"voice":"alloy"`, `"response_format":"mp3"`},
			contentType: "audio/mpeg", response: []byte("ID3\x04\x00\x00binary-audio"),
		},
		{
			name: "openrouter pcm with compression magic", channelID: channel.OpenRouter, basePath: "/proxy/api", wantPath: "/proxy/api/v1/audio/speech",
			body:  `{"model":"public","input":"hello","voice":"alloy","response_format":"pcm"}`,
			model: "openai/gpt-4o-mini-tts", wantFields: []string{`"response_format":"pcm"`},
			contentType: "audio/pcm", response: []byte("\x1f\x8b\x00\x00\x28\xb5\x2f\xfdpcm-samples"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.Method != http.MethodPost || request.URL.Path != test.wantPath {
					t.Errorf("target = %s %s", request.Method, request.URL.Path)
				}
				if request.Header.Get("Authorization") != "Bearer "+testAPIKey || request.Header.Get("X-Api-Key") != "" {
					t.Error("credential headers were not replaced")
				}
				payload, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
					return
				}
				for _, field := range test.wantFields {
					if !bytes.Contains(payload, []byte(field)) {
						t.Errorf("body = %s, missing %s", payload, field)
					}
				}
				writer.Header().Set("Content-Type", test.contentType)
				writer.Header().Set("X-Request-Id", "speech-request")
				_, _ = writer.Write(test.response)
			}))
			defer server.Close()

			spec := openAIEmbeddingsSpec(test.channelID, server.URL+test.basePath, []byte(test.body))
			spec.ClientProtocol = protocol.AudioSpeech
			spec.Operation = execution.OperationAudioSpeech
			spec.Path = "/v1/audio/speech"
			spec.ClientModel = "public"
			spec.UpstreamModel = test.model
			spec = freezeTestAttempt(spec)
			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			result := runtime.Execute(context.Background(), spec)
			if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK || calls.Load() != 1 {
				t.Fatalf("calls=%d result=%+v error=%+v contract=%v body=%s", calls.Load(), result, result.Error, err, result.Body)
			}
			if !bytes.Equal(result.Body, test.response) || result.Header.Get("Content-Type") != test.contentType {
				t.Fatalf("response = %q (%s), want %q (%s)", result.Body, result.Header.Get("Content-Type"), test.response, test.contentType)
			}
			if result.UpstreamProtocol != protocol.AudioSpeech || result.UpstreamRequestID != "speech-request" {
				t.Fatalf("protocol/request id = %s / %s", result.UpstreamProtocol, result.UpstreamRequestID)
			}
		})
	}
}

func TestAudioSpeechRejectsCompressedAudioWithoutEncoding(t *testing.T) {
	t.Parallel()
	audio := []byte("ID3\x04\x00\x00binary-audio")
	var gzipped bytes.Buffer
	gzipWriter := gzip.NewWriter(&gzipped)
	if _, err := gzipWriter.Write(audio); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	zstdEncoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	zstded := zstdEncoder.EncodeAll(audio, nil)
	if err := zstdEncoder.Close(); err != nil {
		t.Fatal(err)
	}
	for encoding, body := range map[string][]byte{"gzip": gzipped.Bytes(), "zstd": zstded} {
		t.Run(encoding, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				// The SDK drops Content-Encoding from passthrough headers but keeps the raw body.
				writer.Header().Set("Content-Type", "audio/mpeg")
				writer.Header().Set("Content-Encoding", encoding)
				_, _ = writer.Write(body)
			}))
			defer server.Close()

			spec := openAIEmbeddingsSpec(channel.OpenRouter, server.URL+"/api", []byte(`{"model":"public","input":"hello","response_format":"mp3"}`))
			spec.ClientProtocol = protocol.AudioSpeech
			spec.Operation = execution.OperationAudioSpeech
			spec.Path = "/v1/audio/speech"
			spec.ClientModel = "public"
			spec.UpstreamModel = "openai/gpt-4o-mini-tts"
			spec = freezeTestAttempt(spec)
			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			result := runtime.Execute(context.Background(), spec)
			if result.Error == nil || result.Error.Kind != execution.ErrorKindInternal {
				t.Fatalf("result = %+v error = %+v; want compressed audio rejected", result, result.Error)
			}
		})
	}
}

func TestAudioSpeechTargetsUseChannelDefaults(t *testing.T) {
	registry := channel.NewRegistry()
	for _, test := range []struct {
		channelID channel.ID
		baseURL   string
		path      string
	}{
		{channelID: channel.Mistral, baseURL: "https://api.mistral.ai/v1", path: "/audio/speech"},
		{channelID: channel.OpenRouter, baseURL: "https://openrouter.ai/api", path: "/v1/audio/speech"},
	} {
		resolved, err := registry.Resolve(test.channelID, nil)
		if err != nil {
			t.Fatal(err)
		}
		baseURL, path, err := audioSpeechTarget(resolved, "/v1/audio/speech")
		if err != nil || baseURL != test.baseURL || path != test.path {
			t.Fatalf("%s target = %q %q, %v", test.channelID, baseURL, path, err)
		}
	}
}
