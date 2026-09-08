package subscription

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestCredentialManagerRechecksTargetAfterWaitingToForceRefresh(t *testing.T) {
	manager, db, registry, keyService, row := newCredentialManagerFixture(t,
		credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	entries, err := registry.SnapshotGroupCredentialEntriesExact(row.GroupID, []uint{row.ID})
	if err != nil || len(entries) != 1 {
		t.Fatalf("read credential entries: %v", err)
	}
	plaintext, err := keyService.Decrypt(row.Data)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := execution.NewCredentialSnapshot(row.ID, row.SecretVersion, entries[0].IdentityGeneration, []byte(plaintext))
	refreshCalls := 0
	manager.refresh = adaptCodexRefresh(refreshedCredential)
	originalRefresh := manager.refresh
	manager.refresh = func(ctx context.Context, driver subscriptionruntime.Driver, credential subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
		refreshCalls++
		return originalRefresh(ctx, driver, credential)
	}
	completed := make(chan *execution.ErrorEvidence, 1)
	manager.mutations.Do(row.ID, func() {
		started := make(chan struct{})
		go func() {
			close(started)
			_, evidence := manager.PrepareForControl(t.Context(), channel.Codex, snapshot, true)
			completed <- evidence
		}()
		<-started
		select {
		case <-completed:
			t.Fatal("force refresh bypassed the in-flight target mutation")
		case <-time.After(100 * time.Millisecond):
		}
		// force refresh 已在等待同一凭据的锁；拿到锁后必须重新验证目标身份。
		params := models.JSON(`{"base_url":"https://relay.example/current"}`)
		if err := db.Model(&models.Group{}).Where("id = ?", row.GroupID).Update("params", params).Error; err != nil {
			t.Fatal(err)
		}
		entries[0].IdentityGeneration = stateloader.CredentialIdentityGeneration(
			row.IdentityFingerprint, "codex", string(models.ConnectionTypeSubscription), json.RawMessage(params))
		if _, err := registry.ReconcileGroup(row.GroupID, entries); err != nil {
			t.Fatal(err)
		}
	})
	select {
	case evidence := <-completed:
		if evidence == nil || evidence.Code != "credential_target_mismatch" || refreshCalls != 0 {
			t.Fatalf("stale target refresh evidence=%#v refresh calls=%d", evidence, refreshCalls)
		}
	case <-time.After(time.Second):
		t.Fatal("force refresh did not finish after target mutation")
	}
	var stored models.Credential
	if err := db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Data != row.Data || stored.SecretVersion != row.SecretVersion || stored.AuthState != row.AuthState {
		t.Fatal("stale target refresh mutated the current durable credential")
	}
}
