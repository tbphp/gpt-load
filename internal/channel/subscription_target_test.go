package channel

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSubscriptionTargetsKeepOfficialDefaultsOutOfConfiguration(t *testing.T) {
	registry := NewRegistry()
	for _, test := range []struct {
		id    ID
		roots []string
	}{
		{Codex, []string{"https://chatgpt.com"}},
		{Claude, []string{"https://api.anthropic.com"}},
		{Antigravity, []string{"https://daily-cloudcode-pa.googleapis.com", "https://cloudcode-pa.googleapis.com"}},
		{Grok, []string{"https://cli-chat-proxy.grok.com"}},
	} {
		t.Run(string(test.id), func(t *testing.T) {
			for _, raw := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`{"base_url":"  "}`)} {
				resolved, err := registry.Resolve(test.id, raw)
				if err != nil || string(resolved.TargetConfig) != `{}` {
					t.Errorf("official target for %s = %s, %v", raw, resolved.TargetConfig, err)
				}
			}
			descriptor, _ := registry.Get(test.id)
			encoded, err := json.Marshal(descriptor)
			if err != nil {
				t.Fatal(err)
			}
			var hints struct {
				Roots []string `json:"default_base_urls"`
			}
			if err := json.Unmarshal(encoded, &hints); err != nil || !reflect.DeepEqual(hints.Roots, test.roots) {
				t.Errorf("official hints = %v, %v; want %v", hints.Roots, err, test.roots)
			}
			if len(descriptor.ParamFields) != 1 || descriptor.ParamFields[0].Key != "base_url" ||
				descriptor.ParamFields[0].Required || descriptor.ParamFields[0].DefaultValue != nil {
				t.Errorf("optional root field = %#v", descriptor.ParamFields)
			}
			resolved, err := registry.Resolve(test.id, json.RawMessage(`{"base_url":"HTTPS://RELAY.EXAMPLE:443/team-a/"}`))
			if err != nil || string(resolved.TargetConfig) != `{"base_url":"https://relay.example/team-a"}` {
				t.Fatalf("custom target = %s, %v", resolved.TargetConfig, err)
			}
			if _, err := registry.ResolveExecutionTarget(test.id, resolved.TargetConfig); err != nil {
				t.Fatal(err)
			}
			for _, raw := range []string{`{"base_url":"http://relay.example"}`, `{"base_url":"https://relay.example?key=value"}`, `{"base_url":"https://user:pass@relay.example"}`} {
				if _, err := registry.Resolve(test.id, json.RawMessage(raw)); err == nil {
					t.Errorf("invalid root accepted: %s", raw)
				}
			}
		})
	}
}
