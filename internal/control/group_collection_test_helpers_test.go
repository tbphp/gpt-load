package control

import (
	"encoding/json"
	"fmt"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

func createGroupCollectionGroup(
	t *testing.T,
	fixture serviceFixture,
	name string,
	enabled bool,
	weight *int,
) *models.Group {
	t.Helper()
	group := validControlGroup(name)
	group.Params = models.JSON(fmt.Sprintf(
		`{"base_url":"https://group-%d.example/v1"}`,
		testIdempotencySequence.Add(1),
	))
	group.Enabled = enabled
	group.WeightManual = weight
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatalf("create group %q: %v", name, err)
	}
	if !enabled {
		if err := fixture.db.Model(group).Update("enabled", false).Error; err != nil {
			t.Fatalf("disable group %q: %v", name, err)
		}
		group.Enabled = false
	}
	return group
}

func setGroupCollectionChannel(
	t *testing.T,
	fixture serviceFixture,
	group *models.Group,
	channelID channel.ID,
	params models.JSON,
) {
	t.Helper()
	group.ChannelID = string(channelID)
	group.Params = append(models.JSON(nil), params...)
	if err := fixture.db.Model(group).Updates(map[string]any{
		"channel_id": group.ChannelID,
		"params":     group.Params,
	}).Error; err != nil {
		t.Fatalf("update group %q channel: %v", group.Name, err)
	}
}

func setGroupCollectionRoute(
	t *testing.T,
	fixture serviceFixture,
	group *models.Group,
	_ string,
	rawModels string,
) {
	t.Helper()
	group.Models = models.JSON(rawModels)
	if err := fixture.db.Model(group).Update("models", group.Models).Error; err != nil {
		t.Fatalf("update group %q route: %v", group.Name, err)
	}
}

func createGroupCollectionKey(
	t *testing.T,
	fixture serviceFixture,
	groupID uint,
	status models.CredentialStatus,
	weight *int,
) state.CredentialEntry {
	t.Helper()
	sequence := testIdempotencySequence.Add(1)
	plain, err := json.Marshal(map[string]any{"api_key": fmt.Sprintf("sk-%d", sequence)})
	if err != nil {
		t.Fatalf("encode credential: %v", err)
	}
	encrypted, err := fixture.encryption.Encrypt(string(plain))
	if err != nil {
		t.Fatalf("encrypt credential: %v", err)
	}
	row := models.Credential{
		GroupID: groupID, Data: encrypted, Fingerprint: fixture.encryption.Hash(string(plain)),
		Status: status, WeightManual: weight,
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatalf("create credential for group %d: %v", groupID, err)
	}
	var group models.Group
	if err := fixture.db.Where("id = ?", groupID).Take(&group).Error; err != nil {
		t.Fatalf("load group %d: %v", groupID, err)
	}
	runtimeStatus := state.CredentialStatusActive
	if status == models.CredentialStatusDisabled {
		runtimeStatus = state.CredentialStatusDisabled
	}
	return state.CredentialEntry{
		ID: row.ID, GroupID: groupID, Status: runtimeStatus, WeightManual: weight,
		Version:            groupCollectionCredentialVersion(row.UpdatedAtMS),
		IdentityGeneration: groupCollectionCredentialIdentity(row.Fingerprint, group),
		Fingerprint:        row.Fingerprint,
		EncryptedValue:     row.Data,
	}
}

func publishGroupCollectionRuntime(
	t *testing.T,
	fixture serviceFixture,
	entries []state.CredentialEntry,
) {
	t.Helper()
	input, err := stateloader.BuildCompileInput(t.Context(), fixture.db, fixture.service.channelRegistry)
	if err != nil {
		t.Fatalf("BuildCompileInput() error = %v", err)
	}
	if _, err := fixture.manager.Publish(input); err != nil {
		t.Fatalf("manager.Publish() error = %v", err)
	}
	if err := fixture.registry.ReplaceCredentials(entries); err != nil {
		t.Fatalf("registry.ReplaceCredentials() error = %v", err)
	}
}
