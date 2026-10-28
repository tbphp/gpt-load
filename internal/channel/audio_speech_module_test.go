package channel

import (
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestAudioSpeechNativeRouteIsLimitedToSupportedChannels(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	supported := map[ID]struct{}{Mistral: {}, OpenRouter: {}}
	for _, descriptor := range registry.List() {
		definition, ok := registry.lookup(descriptor.ID)
		if !ok {
			t.Fatalf("lookup(%q) missing", descriptor.ID)
		}
		mode, ok := definition.modes[protocol.AudioSpeech][execution.OperationAudioSpeech]
		if _, want := supported[descriptor.ID]; want {
			if !ok || mode != RouteNative {
				t.Errorf("%q speech route = %q, %t; want native", descriptor.ID, mode, ok)
			}
		} else if ok {
			t.Errorf("%q unexpectedly advertises speech route %q", descriptor.ID, mode)
		}
		if mode, ok := definition.modes[protocol.AudioSpeech][execution.OperationProbe]; ok {
			t.Errorf("%q advertises speech probe route %q", descriptor.ID, mode)
		}
		if mode, ok := definition.modes[protocol.Mistral][execution.Operation("mistral_audio_speech")]; ok {
			t.Errorf("%q still advertises Mistral speech route %q", descriptor.ID, mode)
		}
	}
}
