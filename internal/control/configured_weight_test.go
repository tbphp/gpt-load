package control

import (
	"encoding/json"
	"testing"
	"time"

	"gpt-load/internal/health"
	"gpt-load/internal/state"
)

func TestCredentialItemReportsConfiguredWeightInEveryState(t *testing.T) {
	now := time.Now().UTC()
	configured := 80
	zero := 0
	for _, test := range []struct {
		name   string
		weight *int
		bucket healthBucket
		want   float64
	}{
		{name: "default", bucket: healthBucketAvailable, want: 50},
		{name: "configured", weight: &configured, bucket: healthBucketAvailable, want: 80},
		{name: "cooldown", weight: &configured, bucket: healthBucketCooldown, want: 80},
		{name: "blacklisted", weight: &configured, bucket: healthBucketBlacklisted, want: 80},
		{name: "disabled", weight: &configured, bucket: healthBucketDisabled, want: 80},
		{name: "legacy zero", weight: &zero, bucket: healthBucketDisabled, want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			view := state.CredentialRuntimeView{
				Status: state.CredentialStatusActive, WeightManual: test.weight,

				CooldownUntil: now.Add(time.Minute),
			}
			item, err := mapCredentialRuntimeItem("masked", 1, view, test.bucket, health.CredentialStats{}, now)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(item)
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(payload, &body); err != nil {
				t.Fatal(err)
			}
			if body["weight"] != test.want {
				t.Fatalf("weight = %v, want configured weight %v", body["weight"], test.want)
			}
			if _, exists := body["weight_mode"]; exists {
				t.Fatal("credential response still exposes an automatic/manual weight mode")
			}
		})
	}
}
