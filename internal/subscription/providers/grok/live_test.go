package grok

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestLiveGrokObservationContract(t *testing.T) {
	credentialFile := strings.TrimSpace(os.Getenv("CPA_LIVE_GROK_CREDENTIAL_FILE"))
	if credentialFile == "" {
		t.Skip("CPA_LIVE_GROK_CREDENTIAL_FILE is not set")
	}
	raw, err := os.ReadFile(credentialFile)
	if err != nil {
		t.Fatalf("read live Grok credential: %v", err)
	}
	defer clear(raw)
	if len(raw) > 64<<10 {
		t.Fatal("live Grok credential exceeds size limit")
	}
	credential, err := ParseCredentialJSON(raw)
	if err != nil {
		t.Fatalf("parse live Grok credential: %v", err)
	}
	observed, err := ObserveAccount(t.Context(), credential)
	if err != nil {
		t.Fatalf("observe live Grok account: %v", err)
	}
	if len(observed.IncompleteSources) > 0 {
		t.Fatalf("live Grok observation is incomplete: %v", observed.IncompleteSources)
	}
	normalized, err := NormalizeObservation(credential.Email, observed)
	if err != nil {
		t.Fatalf("normalize live Grok observation: %v", err)
	}
	var snapshot providerobservation.Snapshot
	if err := json.Unmarshal(normalized, &snapshot); err != nil {
		t.Fatalf("decode live Grok snapshot: %v", err)
	}
	if len(snapshot.QuotaWindows) == 0 {
		t.Fatal("live Grok snapshot has no quota windows")
	}
	hasAccountWindow := false
	hasCreditWindow := false
	for _, window := range snapshot.QuotaWindows {
		if window.ID == "" || window.Label == "" || window.Scope == "" || window.State == "" {
			t.Fatalf("live Grok snapshot contains incomplete quota window: %#v", window)
		}
		if window.Scope == quotaScopeAccount {
			hasAccountWindow = true
			if window.ResetAtMS == nil || window.Utilization == nil {
				t.Fatalf("live Grok account quota lacks utilization or reset: %#v", window)
			}
		}
		hasCreditWindow = hasCreditWindow || window.Scope == quotaScopeCredits
	}
	if !hasAccountWindow {
		t.Fatal("live Grok snapshot has no account quota window")
	}
	if !hasCreditWindow {
		t.Fatal("live Grok snapshot has no credit or billing window")
	}
	if snapshot.Plan.Name == "" {
		t.Fatal("live Grok snapshot has no plan name")
	}
}
