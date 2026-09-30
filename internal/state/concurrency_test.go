package state

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/platform/config"
)

func TestConcurrencySettingsInheritance(t *testing.T) {
	settings, err := ResolveRuntimeSettings(config.Settings{
		"global_concurrency_limit":             json.Number("100"),
		"default_access_key_concurrency_limit": json.Number("10"),
		"default_group_concurrency_limit":      json.Number("5"),
	})
	if err != nil {
		t.Fatalf("valid concurrency settings rejected: %v", err)
	}
	inherited, err := ResolveGroupRuntimeSettings(settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	if inherited.ConcurrencyLimit != 5 {
		t.Fatalf("group does not inherit concurrency: %d", inherited.ConcurrencyLimit)
	}
	unlimited, err := ResolveGroupRuntimeSettings(settings, config.Settings{"concurrency_limit": json.Number("0")})
	if err != nil {
		t.Fatal(err)
	}
	if unlimited.ConcurrencyLimit != 0 {
		t.Fatalf("explicit unlimited lost: %d", unlimited.ConcurrencyLimit)
	}
	for _, value := range []any{json.Number("-1"), json.Number("0.5"), json.Number("9007199254740992"), nil, "1"} {
		if _, err := ResolveRuntimeSettings(config.Settings{"global_concurrency_limit": value}); err == nil {
			t.Errorf("accepted invalid limit %v", value)
		}
	}
}
