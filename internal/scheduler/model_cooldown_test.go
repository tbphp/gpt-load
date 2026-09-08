package scheduler

import (
	"testing"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestModelCooldownFiltersSelectionInspectionAndRefreshReplay(t *testing.T) {
	r := state.NewCredentialRegistry()
	if err := r.ReplaceCredentials([]state.CredentialEntry{
		{ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Status: state.CredentialStatusActive, Fingerprint: "one", EncryptedValue: "one"},
		{ID: 2, GroupID: 2, Version: 1, IdentityGeneration: 1, Status: state.CredentialStatusActive, Fingerprint: "two", EncryptedValue: "two"},
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	snapshot := schedulerSnapshot()
	query := Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o"), PreferredCredentialID: 1}
	iterator := New(snapshot, r, query)
	first, err := iterator.Next()
	if err != nil || first.CredentialID != 1 {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	ref, _ := r.CredentialRef(1)
	r.SetModelCooldown(ref, "gpt-4o", now.Add(time.Hour), now)
	if iterator.ChargeReplay(first, ref) {
		t.Fatal("refresh replay bypassed model cooldown")
	}
	second, err := New(snapshot, r, query).Next()
	if err != nil || second.CredentialID != 2 {
		t.Fatalf("selection=%#v err=%v", second, err)
	}
	inspection, err := Inspect(snapshot, r.Snapshot(), query, now)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Groups[0].Credentials[0].Available {
		t.Fatal("inspection ignored model cooldown")
	}
	// 别名请求使用各分组实际发送的模型名；共享客户端别名不共享冷却键。
	secondRef, _ := r.CredentialRef(2)
	r.SetModelCooldown(secondRef, "gpt-4o", now.Add(2*time.Hour), now)
	if selection, err := New(snapshot, r, query).Next(); err != nil || selection.CredentialID != 2 {
		t.Fatal("client alias incorrectly cooled the different upstream model")
	}
	r.SetModelCooldown(secondRef, "provider-gpt-4o", now.Add(3*time.Hour), now)
	if until, limited := New(snapshot, r, query).CooldownUntil(); !limited || !until.Equal(now.Add(time.Hour)) {
		t.Fatalf("earliest applicable recovery = %v, %t", until, limited)
	}
	// 计数请求不读取推理冷却，但仍通过原有的路由与凭据资格检查。
	countQuery := query
	countQuery.ClientProtocol = protocol.OpenAIResponses
	countQuery.Operation = execution.OperationResponsesInputTokens
	if selection, err := New(snapshot, r, countQuery).Next(); err != nil || selection.CredentialID != 1 {
		t.Fatalf("count request blocked by model limit: %#v, %v", selection, err)
	}
	r.ClearModelCooldowns(1)
	after, err := New(snapshot, r, query).Next()
	if err != nil || after.CredentialID != 1 {
		t.Fatalf("restored=%#v err=%v", after, err)
	}
}

func TestCooldownRecoveryIgnoresCredentialsThatCannotBecomeCandidates(t *testing.T) {
	r := state.NewCredentialRegistry()
	if err := r.ReplaceCredentials([]state.CredentialEntry{
		{ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Status: state.CredentialStatusActive, Fingerprint: "one", EncryptedValue: "one"},
		{ID: 2, GroupID: 2, Version: 1, IdentityGeneration: 1, Status: state.CredentialStatusActive, Fingerprint: "two", EncryptedValue: "two"},
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ref, _ := r.CredentialRef(1)
	r.SetModelCooldown(ref, "gpt-4o", now.Add(time.Hour), now)
	r.SetBlacklisted(2)
	query := Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o")}
	iterator := New(schedulerSnapshot(), r, query)
	if _, err := iterator.Next(); err != ErrExhausted {
		t.Fatalf("unexpected candidate: %v", err)
	}
	if until, limited := iterator.CooldownUntil(); !limited || !until.Equal(now.Add(time.Hour)) {
		t.Fatalf("blacklisted credential hid recoverable cooldown: %v, %t", until, limited)
	}
	if err := r.SetCredentialStatus(1, state.CredentialStatusDisabled); err != nil {
		t.Fatal(err)
	}
	if until, limited := iterator.CooldownUntil(); limited || !until.IsZero() {
		t.Fatalf("disabled/blacklisted credentials were reported as recoverable: %v, %t", until, limited)
	}
}
