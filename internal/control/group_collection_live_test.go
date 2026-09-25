package control

import (
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/state"
)

func TestCodexGroupWithoutConfiguredModelsIsAvailableForLive(t *testing.T) {
	t.Parallel()
	counts := GroupCollectionCredentialCounts{Total: 1, Available: 1}
	status, reason := groupCollectionStatusAndReason(state.GroupCatalogView{
		ChannelID: channel.Codex, Enabled: true,
	}, counts, 0)
	if status != GroupCollectionStatusAvailable || reason != nil {
		t.Fatalf("Codex group status = %s, reason = %v", status, reason)
	}
	availability := modernGroupAvailability(groupCollectionRecord{
		GroupCollectionItem: GroupCollectionItem{ChannelID: channel.Codex, Status: status, CredentialCounts: counts},
	})
	if availability != "ready" {
		t.Fatalf("modern Codex availability = %s", availability)
	}
}
