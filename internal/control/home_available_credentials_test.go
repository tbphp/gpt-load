package control

import (
	"context"
	"testing"
	"time"

	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

// 首页的「X/Y 个凭据可用」必须和健康页 classifyHealthKey 用同一套分桶：
// 只看 status/拉黑/冷却会把「待重新授权」和「权重手动置 0」的凭据算成可用，
// 而调度器根本不会选中它们，两页并排就会自相矛盾。
func TestReadHomeBaseAvailableCredentialsMatchHealthClassification(t *testing.T) {
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	zeroWeight := 0

	group := validControlGroup("home-available-parity")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}

	credentials := []models.Credential{
		{ID: 1, GroupID: group.ID, Data: "cipher-1", Fingerprint: "hash-1", Status: models.CredentialStatusActive},
		{ID: 2, GroupID: group.ID, Data: "cipher-2", Fingerprint: "hash-2", Status: models.CredentialStatusActive},
		{ID: 3, GroupID: group.ID, Data: "cipher-3", Fingerprint: "hash-3", Status: models.CredentialStatusActive},
	}
	if err := fixture.db.Create(&credentials).Error; err != nil {
		t.Fatalf("create credentials: %v", err)
	}

	entries := make([]state.CredentialEntry, 0, len(credentials))
	for index, credential := range credentials {
		entries = append(entries, state.CredentialEntry{
			ID: credential.ID, GroupID: group.ID, Status: state.CredentialStatusActive,
			Version:            groupCollectionCredentialVersion(credential.SecretVersion),
			IdentityGeneration: groupCollectionCredentialIdentity(credential.IdentityFingerprint, *group),
			Fingerprint:        credential.Fingerprint,
			EncryptedValue:     "cipher-" + string(rune('1'+index)),
			AuthState:          state.CredentialAuthStateReady,
		})
	}
	// 2 号待重新授权，3 号被手动停用（权重 0）；两者都不参与调度。
	entries[1].AuthState = state.CredentialAuthStateReauthorizationRequired
	entries[2].WeightManual = &zeroWeight
	if err := fixture.registry.ReplaceCredentials(entries); err != nil {
		t.Fatalf("registry.ReplaceCredentials() error = %v", err)
	}

	input, err := stateloader.BuildCompileInput(context.Background(), fixture.db, fixture.channelRegistry)
	if err != nil {
		t.Fatalf("BuildCompileInput() error = %v", err)
	}
	if _, err := fixture.manager.Publish(input); err != nil {
		t.Fatalf("manager.Publish() error = %v", err)
	}
	fixture.service.registrySnapshot = fixture.registry.Snapshot

	base, err := fixture.service.ReadHomeBase(context.Background(), now.UnixMilli())
	if err != nil {
		t.Fatalf("ReadHomeBase() error = %v", err)
	}

	if base.Inventory.CredentialCount != 3 {
		t.Fatalf("CredentialCount = %d, want 3", base.Inventory.CredentialCount)
	}
	if base.Inventory.AvailableCredentialCount != 1 {
		t.Fatalf(
			"AvailableCredentialCount = %d, want 1 (待重新授权与权重 0 的凭据不可用)",
			base.Inventory.AvailableCredentialCount,
		)
	}

	// 与健康页的分桶逐条比对，确保两处结论一致而不只是数字凑巧相等。
	snapshot := fixture.manager.Current()
	var healthAvailable int64
	for _, view := range fixture.registry.Snapshot() {
		catalog, ok := snapshot.GroupCatalog[view.GroupID]
		if !ok {
			t.Fatalf("group %d missing from catalog", view.GroupID)
		}
		if classifyHealthKey(catalog, view, now) == healthBucketAvailable {
			healthAvailable++
		}
	}
	if base.Inventory.AvailableCredentialCount != healthAvailable {
		t.Fatalf(
			"home available = %d, health available = %d; 两处口径必须一致",
			base.Inventory.AvailableCredentialCount,
			healthAvailable,
		)
	}
}
