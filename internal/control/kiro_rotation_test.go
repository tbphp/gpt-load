package control

import (
	"encoding/json"
	"testing"
	"time"

	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestKiroPrimaryQuotaUsageUtilization(t *testing.T) {
	payload, err := json.Marshal(CredentialObservationSnapshot{
		QuotaWindows: []ObservationQuotaWindow{
			{ID: "credit", IsPrimary: true, Utilization: floatPtr(0.98)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	usage, ok := kiroPrimaryQuotaUsage(subscriptionruntime.Observation{Payload: payload})
	if !ok {
		t.Fatal("expected an observed usage reading")
	}
	if usage != 0.98 {
		t.Fatalf("usage = %v, want 0.98", usage)
	}
}

func TestKiroPrimaryQuotaUsageUsesFirstDecisionWindow(t *testing.T) {
	// The first window in iteration order that is primary or already exhausted
	// decides rotation eligibility. An exhausted window wins even when a later
	// primary window is provided.
	payload, err := json.Marshal(CredentialObservationSnapshot{
		QuotaWindows: []ObservationQuotaWindow{
			{ID: "tokens", Utilization: floatPtr(1.0)},
			{ID: "credit", IsPrimary: true, Utilization: floatPtr(0.3)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	usage, ok := kiroPrimaryQuotaUsage(subscriptionruntime.Observation{Payload: payload})
	if !ok {
		t.Fatal("expected an observed usage reading")
	}
	if usage != 1.0 {
		t.Fatalf("usage = %v, want the first exhausted window 1.0", usage)
	}
}

func TestKiroPrimaryQuotaUsageUsedOverLimit(t *testing.T) {
	payload, err := json.Marshal(CredentialObservationSnapshot{
		QuotaWindows: []ObservationQuotaWindow{
			{ID: "credit", IsPrimary: true, Used: floatPtr(90), Limit: floatPtr(100)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	usage, ok := kiroPrimaryQuotaUsage(subscriptionruntime.Observation{Payload: payload})
	if !ok {
		t.Fatal("expected an observed usage reading")
	}
	if usage != 0.9 {
		t.Fatalf("usage = %v, want 0.9", usage)
	}
}

func TestKiroPrimaryQuotaUsageMatchedExhaustedWindow(t *testing.T) {
	// A non-primary window must still flip rotation once it passes the threshold,
	// even when it is not the nominal primary window.
	payload, err := json.Marshal(CredentialObservationSnapshot{
		QuotaWindows: []ObservationQuotaWindow{
			{ID: "credit", Utilization: floatPtr(1.0)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	usage, ok := kiroPrimaryQuotaUsage(subscriptionruntime.Observation{Payload: payload})
	if !ok || usage != 1.0 {
		t.Fatalf("usage = %v, ok = %v; want 1.0, true", usage, ok)
	}
}

func TestKiroPrimaryQuotaUsageNoWindows(t *testing.T) {
	payload, err := json.Marshal(CredentialObservationSnapshot{QuotaWindows: []ObservationQuotaWindow{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := kiroPrimaryQuotaUsage(subscriptionruntime.Observation{Payload: payload}); ok {
		t.Fatal("expected no observed usage reading for empty windows")
	}
}

func TestKiroPrimaryQuotaUsageEmptyPayload(t *testing.T) {
	if _, ok := kiroPrimaryQuotaUsage(subscriptionruntime.Observation{}); ok {
		t.Fatal("expected no observed usage reading for empty payload")
	}
}

func TestKiroRotationMonitorCooldown(t *testing.T) {
	monitor := newKiroRotationMonitor(nil)
	now := time.Now()
	if !monitor.shouldAttempt(7, kiroRotationCooldown, now) {
		t.Fatal("first attempt should not be subject to a cooldown")
	}
	monitor.noteAttempt(7, now)
	if monitor.shouldAttempt(7, kiroRotationCooldown, now.Add(time.Minute)) {
		t.Fatal("attempt within cooldown window should be skipped")
	}
	if !monitor.shouldAttempt(7, kiroRotationCooldown, now.Add(kiroRotationCooldown+time.Minute)) {
		t.Fatal("attempt after cooldown window should be allowed")
	}
}

func TestKiroRotationMonitorCooldownIsPerCredential(t *testing.T) {
	monitor := newKiroRotationMonitor(nil)
	now := time.Now()
	monitor.noteAttempt(1, now)
	if !monitor.shouldAttempt(2, kiroRotationCooldown, now.Add(time.Second)) {
		t.Fatal("cooldown for one credential must not affect another")
	}
}

func floatPtr(value float64) *float64 {
	return &value
}
