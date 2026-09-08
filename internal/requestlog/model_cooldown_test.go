package requestlog

import (
	"testing"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/telemetry"
)

func TestModelCooldownAttemptPreservesTargetAndDeadline(t *testing.T) {
	event := testEvent("model-cooldown-attempt")
	deadline := event.CompletedAt.Add(48 * time.Hour).Truncate(time.Millisecond)
	event.Attempts[0].FailureScope = execution.ErrorScopeModel
	event.Attempts[0].Effect = telemetry.EffectCooldownModel
	event.Attempts[0].CooldownUntil = deadline
	row, err := mapEvent(redact.New(), event)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeAttemptRows(row.AttemptRows)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 || decoded[0].Effect != telemetry.EffectCooldownModel || decoded[0].UpstreamModel != event.Attempts[0].UpstreamModel || decoded[0].CooldownUntilMS == nil || *decoded[0].CooldownUntilMS != deadline.UnixMilli() {
		t.Fatalf("decoded = %#v", decoded)
	}
	event.Attempts[0].CooldownUntil = time.Time{}
	if _, err := mapEvent(redact.New(), event); err == nil {
		t.Fatal("accepted model cooldown without deadline")
	}
}
