package dialect

import (
	"bytes"
	"net/http"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestAudioSpeechInspectsNativeStreamingModes(t *testing.T) {
	t.Parallel()
	speech := NewAudioSpeech()
	if speech.Protocol() != protocol.AudioSpeech {
		t.Fatalf("protocol = %s", speech.Protocol())
	}
	tests := []struct {
		name   string
		body   string
		model  string
		stream bool
	}{
		{name: "openrouter", body: `{"model":"openai/gpt-4o-mini-tts","input":"hello","voice":"alloy","response_format":"mp3"}`, model: "openai/gpt-4o-mini-tts"},
		{name: "mistral", body: `{"model":"voxtral-mini-tts-2603","input":"hello","voice_id":"gb_jane_neutral"}`, model: "voxtral-mini-tts-2603"},
		{name: "openai sse", body: `{"model":"tts","input":"hello","voice":"alloy","stream_format":"sse"}`, model: "tts", stream: true},
		{name: "mistral sse", body: `{"model":"tts","input":"hello","voice_id":"gb_jane_neutral","stream":true}`, model: "tts", stream: true},
		{name: "openai audio", body: `{"model":"tts","stream_format":"audio"}`, model: "tts"},
		{name: "mistral unary", body: `{"model":"tts","stream":false}`, model: "tts"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			metadata, err := speech.InspectRequest(&ParsedRequest{
				Method: http.MethodPost, Path: "/v1/audio/speech",
				Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(test.body),
			})
			if err != nil {
				t.Fatalf("InspectRequest() error = %v", err)
			}
			if metadata.Operation != execution.OperationAudioSpeech || metadata.Stream != test.stream ||
				metadata.RouteRequirement != execution.RouteRequirementNative {
				t.Fatalf("metadata = %+v", metadata)
			}
			if metadata.Model == nil || *metadata.Model != test.model {
				t.Fatalf("model = %v, want %s", metadata.Model, test.model)
			}
		})
	}
}

func TestAudioSpeechRejectsInvalidRequests(t *testing.T) {
	t.Parallel()
	speech := NewAudioSpeech()
	jsonHeader := http.Header{"Content-Type": {"application/json"}}
	requests := []*ParsedRequest{
		{Method: http.MethodPost, Path: "/v1/audio/speech", Header: jsonHeader, Body: []byte(`{"input":"hello"}`)},
		{Method: http.MethodPost, Path: "/v1/audio/speech", Header: jsonHeader, Body: []byte(`{"model":"tts","stream":"true"}`)},
		{Method: http.MethodPost, Path: "/v1/audio/speech", Header: jsonHeader, Body: []byte(`{"model":"tts","stream_format":null}`)},
		{Method: http.MethodPost, Path: "/v1/audio/speech", Header: jsonHeader, Body: []byte(`{"model":"tts","stream_format":true}`)},
		{Method: http.MethodPost, Path: "/v1/audio/speech", Header: jsonHeader, Body: []byte(`{"model":"tts","stream_format":"json"}`)},
		{Method: http.MethodPost, Path: "/v1/audio/speech", Header: jsonHeader, Body: []byte(`{"model":"tts","stream_format":"sse","stream_format":"audio"}`)},
		{Method: http.MethodPost, Path: "/v1/audio/speech", Header: jsonHeader, Body: []byte(`{"model":"tts","Stream_Format":"sse"}`)},
		{Method: http.MethodPost, Path: "/v1/audio/speech", Header: http.Header{"Content-Type": {"text/plain"}}, Body: []byte("hello")},
		{Method: http.MethodPost, Path: "/v1/audio/transcriptions", Header: jsonHeader, Body: []byte(`{"model":"tts"}`)},
		{Method: http.MethodGet, Path: "/v1/audio/speech"},
	}
	for _, request := range requests {
		if _, err := speech.InspectRequest(request); err == nil {
			t.Fatalf("InspectRequest(%s %s %s) accepted invalid request", request.Method, request.Path, request.Body)
		}
	}
}

func TestAudioSpeechClassifiesStreamLifecycle(t *testing.T) {
	speech := NewAudioSpeech()
	lifecycle, ok := any(speech).(StreamEventClassifier)
	if !ok {
		t.Fatal("audio speech must classify streaming completion and errors")
	}
	if !lifecycle.RequiresTerminalEvent() {
		t.Fatal("audio speech must require a completion event")
	}
	for _, test := range []struct {
		name        string
		event       StreamEvent
		disposition StreamEventDisposition
		invalid     bool
	}{
		{name: "openai delta", event: StreamEvent{Payload: []byte(`{"type":"speech.audio.delta","audio":"aGVsbG8="}`)}},
		{name: "mistral delta", event: StreamEvent{Name: "speech.audio.delta", Payload: []byte(`{"audio_data":"aGVsbG8="}`)}},
		{name: "openai done", event: StreamEvent{Payload: []byte(`{"type":"speech.audio.done","usage":{"input_tokens":3,"output_tokens":8,"total_tokens":11}}`)}, disposition: StreamEventCompleted},
		{name: "mistral done", event: StreamEvent{Name: "speech.audio.done", Payload: []byte(`{"usage":{"input_tokens":3,"output_tokens":8}}`)}, disposition: StreamEventCompleted},
		{name: "error", event: StreamEvent{Name: "error", Payload: []byte(`{"error":{"message":"failed"}}`)}, disposition: StreamEventFailed},
		{name: "untyped error", event: StreamEvent{Payload: []byte(`{"error":{"message":"failed"}}`)}, disposition: StreamEventFailed},
		{name: "untyped empty error", event: StreamEvent{Payload: []byte(`{"error":{}}`)}, invalid: true},
		{name: "untyped null error", event: StreamEvent{Payload: []byte(`{"error":null}`)}, invalid: true},
		{name: "future event", event: StreamEvent{Payload: []byte(`{"type":"speech.metadata","value":"kept"}`)}},
		{name: "conflicting event names", event: StreamEvent{Name: "speech.audio.done", Payload: []byte(`{"type":"speech.audio.delta","audio":"aGVsbG8="}`)}, invalid: true},
		{name: "invalid event", event: StreamEvent{Payload: []byte(`null`)}, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			classification, err := lifecycle.ClassifyStreamEvent(test.event)
			if test.invalid {
				if err == nil {
					t.Fatal("accepted invalid SSE event")
				}
				return
			}
			if err != nil || classification.Disposition != test.disposition {
				t.Fatalf("classification=%+v err=%v", classification, err)
			}
		})
	}
}

func TestAudioSpeechRewritesModelAndKeepsAudio(t *testing.T) {
	t.Parallel()
	speech := NewAudioSpeech()
	rewritten, err := speech.RewriteRequestModel(&ParsedRequest{
		Method: http.MethodPost, Path: "/v1/audio/speech",
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   []byte(`{"model":"public","input":"hello","voice":"alloy","provider":{"zdr":true}}`),
	}, "openai/gpt-4o-mini-tts")
	if err != nil || !bytes.Contains(rewritten.Body, []byte(`"model":"openai/gpt-4o-mini-tts"`)) ||
		!bytes.Contains(rewritten.Body, []byte(`"voice":"alloy"`)) || !bytes.Contains(rewritten.Body, []byte(`"provider":{"zdr":true}`)) {
		t.Fatalf("request rewrite = %s, err = %v", rewritten.Body, err)
	}

	audio := []byte("ID3\x04\x00binary-audio")
	response, err := speech.RewriteResponseModel(audio, "public")
	if err != nil || !bytes.Equal(response, audio) {
		t.Fatalf("binary response rewrite = %q, err = %v", response, err)
	}
	response, err = speech.RewriteResponseModel([]byte(`{"model":"voxtral-mini-tts-2603","audio_data":"UklGRg=="}`), "public")
	if err != nil || !bytes.Contains(response, []byte(`"model":"public"`)) || !bytes.Contains(response, []byte(`"audio_data":"UklGRg=="`)) {
		t.Fatalf("json response rewrite = %s, err = %v", response, err)
	}
}

func TestInspectStandardRequestCoversAudioSpeech(t *testing.T) {
	t.Parallel()
	metadata, err := InspectStandardRequest(protocol.AudioSpeech, "voxtral-mini-tts-2603")
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Operation != execution.OperationAudioSpeech || metadata.Model == nil || *metadata.Model != "voxtral-mini-tts-2603" {
		t.Fatalf("metadata = %+v", metadata)
	}
}
