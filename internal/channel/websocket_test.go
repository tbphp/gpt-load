package channel

import (
	"testing"
)

func TestResponsesWebsocketCapabilitiesAreIndependent(t *testing.T) {
	registry := NewRegistry()
	for _, test := range []struct {
		id                        ID
		native, stored, multiplex bool
	}{
		{OpenAI, true, true, true}, {XAI, true, true, false},
		{Codex, true, false, false}, {CLIProxyAPI, true, false, false},
		{Sub2API, true, false, false}, {GPTLoad, true, true, true},
		{NewAPI, false, false, false}, {Grok, false, false, false},
	} {
		t.Run(string(test.id), func(t *testing.T) {
			target, err := registry.Resolve(test.id, []byte(`{"base_url":"https://example.test"}`))
			if err != nil {
				t.Fatal(err)
			}
			c := target.ResponsesWebsocket
			if c.Native != test.native || c.StoredResponses != test.stored || c.Multiplex != test.multiplex ||
				c.Continuation != test.native || c.Prewarm != test.native {
				t.Fatalf("unexpected WS capabilities: %+v", c)
			}
		})
	}
}
