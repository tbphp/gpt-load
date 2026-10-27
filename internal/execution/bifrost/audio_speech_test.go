package bifrost

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

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
