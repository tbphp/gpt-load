package state

import (
	"testing"

	"gpt-load/internal/concurrency"
)

func TestConcurrencyOldRoutingSnapshotUsesLatestLimit(t *testing.T) {
	manager := NewManager()
	input := managerCompileInput(1)
	input.ConcurrencyPolicies = map[concurrency.Subject]int64{concurrency.DefaultCredential: 3}
	snapshot, err := manager.Publish(input)
	if err != nil {
		t.Fatal(err)
	}
	limits := snapshot.UpstreamConcurrencyLimits(CredentialRef{ID: 1, GroupID: 1})
	first, blocked, current := manager.AdmitConcurrency(snapshot, limits)
	if !current || blocked != "" {
		t.Fatalf("first admission: current=%v blocked=%s", current, blocked)
	}
	defer first.Release()
	second, blocked, current := manager.AdmitConcurrency(snapshot, limits)
	if !current || blocked != "" {
		t.Fatalf("second admission: current=%v blocked=%s", current, blocked)
	}
	defer second.Release()
	input.ConcurrencyPolicies[concurrency.DefaultCredential] = 1
	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	// 重试仍使用原路由快照，但降额后必须保留正在执行的计数并拒绝新尝试。
	for _, release := range []func(){func() {}, second.Release} {
		release()
		lease, blocked, current := manager.AdmitConcurrency(snapshot, limits)
		lease.Release()
		if !current || blocked != concurrency.Credential(1) {
			t.Fatalf("lowered limit: current=%v blocked=%s", current, blocked)
		}
	}
	input.ConcurrencyPolicies[concurrency.DefaultCredential] = 2
	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	lease, blocked, current := manager.AdmitConcurrency(snapshot, limits)
	defer lease.Release()
	if !current || blocked != "" {
		t.Fatalf("raised limit: current=%v blocked=%s", current, blocked)
	}
	if limits[1].Maximum != 3 || snapshot.ConcurrencyPolicies[concurrency.DefaultCredential] != 3 {
		t.Fatal("admission mutated the original snapshot or limits")
	}
}

func TestConcurrencyProbeRejectsStaleIdentityAndDeletedCredential(t *testing.T) {
	manager := NewManager()
	input := managerCompileInput(1)
	input.Credentials = []CredentialConfig{{ID: 1, GroupID: 1, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "original"}}
	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	original := CredentialRef{ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1}
	lease, admitted := manager.AdmitUpstreamConcurrency(original)
	if !admitted {
		t.Fatal("current credential rejected")
	}
	lease.Release()
	input.Credentials[0].Version = 2
	input.Credentials[0].IdentityGeneration = 2
	input.Credentials[0].Fingerprint = "replacement"
	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	lease, admitted = manager.AdmitUpstreamConcurrency(original)
	lease.Release()
	if admitted {
		t.Fatal("stale identity admitted against replacement counter")
	}
	replacement := CredentialRef{ID: 1, GroupID: 1, Version: 2, IdentityGeneration: 2}
	lease, admitted = manager.AdmitUpstreamConcurrency(replacement)
	if !admitted {
		t.Fatal("replacement credential rejected")
	}
	lease.Release()
	input.Credentials = nil
	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	lease, admitted = manager.AdmitUpstreamConcurrency(replacement)
	lease.Release()
	if admitted {
		t.Fatal("deleted credential admitted")
	}
	for subject, count := range manager.ObserveConcurrency([]concurrency.Subject{concurrency.Group(1), concurrency.Credential(1), concurrency.Upstream}) {
		if count != 0 {
			t.Fatalf("leaked %s=%d", subject, count)
		}
	}
}
