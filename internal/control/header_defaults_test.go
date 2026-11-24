package control

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestControlHeaderDefaults(t *testing.T) {
	rules := state.HeaderRules{Default: map[string]string{"User-Agent": "Go-http-client/1.1", "X-Template": "${API_KEY}"}}
	headers := applyControlHeaderRules(rules, "fixture-token")
	if headers.Get("User-Agent") != "Go-http-client/1.1" || headers.Get("X-Template") != "fixture-token" {
		t.Fatal(headers)
	}
}

func TestHeaderDefaultsSettingsRoundTrip(t *testing.T) {
	fixture := newServiceFixture(t)
	updated, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{
		state.SettingHeaderRules: json.RawMessage(`{"default":{"user-agent":"Go-http-client/1.1"}}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Values.HeaderRules.Default["User-Agent"] != "Go-http-client/1.1" {
		t.Fatal(updated.Values.HeaderRules)
	}
	read, err := fixture.service.GetSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if read.Values.HeaderRules.Default["User-Agent"] != "Go-http-client/1.1" {
		t.Fatal(read.Values.HeaderRules)
	}
	group := validationSignatureGroup()
	before := computeGroupValidationSignature(group, protocol.OpenAICompletions, "fixture-model")
	group.HeaderRules.Default = map[string]string{"User-Agent": "fallback/1.0"}
	if after := computeGroupValidationSignature(group, protocol.OpenAICompletions, "fixture-model"); after == before {
		t.Fatal("defaults missing from probe signature")
	}
}
