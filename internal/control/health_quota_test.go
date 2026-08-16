package control

import (
	"encoding/json"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/state"
)

// 订阅账号额度耗尽时调度器已经在跳过该凭据（registry 的 QuotaExhausted 分支），
// 但 classifyHealthKey 不看额度，健康页仍把它算作 available。
// 首页要能说出「某账号额度快用完了」，健康响应必须单独暴露这批凭据。
func TestRuntimeHealthReportsLowQuotaCredentials(t *testing.T) {
	fixture := newServiceFixture(t)
	now := healthNow()
	fixture.service.now = func() time.Time { return now }

	if _, err := fixture.manager.Publish(state.CompileInput{
		ChannelRegistry: fixture.channelRegistry,
		Groups: []state.GroupConfig{
			{
				ConnectionType: "api_key", ID: 1, Name: "codex", ChannelID: channel.OpenAI,
				Params: json.RawMessage(`{}`),
				Models: []state.ModelConfig{{ID: "model"}}, Enabled: true,
			},
		},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{
		{ID: 11, GroupID: 1, Version: 1, IdentityGeneration: 11, Fingerprint: "test-11", Status: state.CredentialStatusActive, EncryptedValue: "low"},
		{ID: 12, GroupID: 1, Version: 1, IdentityGeneration: 12, Fingerprint: "test-12", Status: state.CredentialStatusActive, EncryptedValue: "healthy"},
		{ID: 13, GroupID: 1, Version: 1, IdentityGeneration: 13, Fingerprint: "test-13", Status: state.CredentialStatusActive, EncryptedValue: "stale"},
	}); err != nil {
		t.Fatalf("ReplaceCredentials() error = %v", err)
	}

	resetAt := now.Add(3 * time.Hour)
	freshUntil := now.Add(30 * time.Minute)
	low := 0.12
	healthy := 0.85
	staleRemaining := 0.05

	if !fixture.registry.SetCredentialQuotaObservation(11, &low, resetAt, freshUntil) {
		t.Fatal("SetCredentialQuotaObservation(11) = false")
	}
	if !fixture.registry.SetCredentialQuotaObservation(12, &healthy, resetAt, freshUntil) {
		t.Fatal("SetCredentialQuotaObservation(12) = false")
	}
	// 13 号观测已过期：FreshQuotaRemaining 会返回 nil，绝不能拿过期数字报警。
	if !fixture.registry.SetCredentialQuotaObservation(
		13, &staleRemaining, resetAt, now.Add(-time.Minute),
	) {
		t.Fatal("SetCredentialQuotaObservation(13) = false")
	}

	result, err := fixture.service.RuntimeHealth()
	if err != nil {
		t.Fatalf("RuntimeHealth() error = %v", err)
	}

	if len(result.LowQuotaCredentials) != 1 {
		t.Fatalf(
			"LowQuotaCredentials = %#v, want exactly the credential below the threshold",
			result.LowQuotaCredentials,
		)
	}
	got := result.LowQuotaCredentials[0]
	if got.CredentialID != 11 {
		t.Fatalf("CredentialID = %d, want 11", got.CredentialID)
	}
	if got.GroupID != 1 || got.GroupName != "codex" {
		t.Fatalf("group = %d/%q, want 1/\"codex\"", got.GroupID, got.GroupName)
	}
	if got.Remaining != low {
		t.Fatalf("Remaining = %v, want %v", got.Remaining, low)
	}
	if got.ResetAtMS != resetAt.UnixMilli() {
		t.Fatalf("ResetAtMS = %d, want %d", got.ResetAtMS, resetAt.UnixMilli())
	}
}
