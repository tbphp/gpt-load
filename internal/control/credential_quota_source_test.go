package control

import (
	"encoding/json"
	"reflect"
	"testing"

	"gpt-load/internal/subscription/providers/codex"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestCredentialQuotaSourceSurvivesSnapshotRoundTrip(t *testing.T) {
	raw, err := codex.NormalizeQuota([]byte(`{
		"rate_limit":{"primary_window":{"used_percent":80,"limit_window_seconds":604800}},
		"additional_rate_limits":[{"metered_feature":"codex_bengalfox","limit_name":"Spark",
			"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000}}}]
	}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot CredentialObservationSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var stored providerobservation.Snapshot
	if err := json.Unmarshal(encoded, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.QuotaWindows) != 2 || stored.QuotaWindows[0].SourceID != "codex" ||
		stored.QuotaWindows[1].SourceID != "codex_bengalfox" {
		t.Fatalf("control-plane serialization lost quota identity: %s", encoded)
	}
	if projected := providerQuotaWindows(snapshot.QuotaWindows); !reflect.DeepEqual(projected, stored.QuotaWindows) {
		t.Fatalf("quota projection lost fields: %#v", projected)
	}
}
