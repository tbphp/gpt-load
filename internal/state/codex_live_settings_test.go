package state

import (
	"testing"

	"gpt-load/internal/platform/config"
)

func TestCodexLiveModeSettings(t *testing.T) {
	for _, mode := range []string{"off", "direct", "relay"} {
		t.Run(mode, func(t *testing.T) {
			base, err := ResolveRuntimeSettings(config.Settings{"codex_live_mode": mode})
			if err != nil {
				t.Fatalf("resolve system voice mode: %v", err)
			}
			if base.CodexLiveMode != CodexLiveMode(mode) {
				t.Fatalf("system mode = %s", base.CodexLiveMode)
			}
			inherited, err := ResolveGroupRuntimeSettings(base, nil)
			if err != nil || inherited.CodexLiveMode != base.CodexLiveMode {
				t.Fatalf("inherit mode = %v, %v", inherited, err)
			}
			if _, err := ResolveGroupRuntimeSettings(base, config.Settings{"codex_live_mode": mode}); err != nil {
				t.Fatalf("resolve group voice mode: %v", err)
			}
		})
	}
	for _, value := range []any{"", "auto", true, 1, nil} {
		if err := ValidateRuntimeSetting("codex_live_mode", value); err == nil {
			t.Errorf("accepted invalid mode %v", value)
		}
	}
}
