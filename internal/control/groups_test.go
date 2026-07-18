package control

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
)

func TestListGroupsReturnsAllGroupsAndTotalKeyCountsInTwoQueries(t *testing.T) {
	fixture := newServiceFixture(t)
	enabled := validControlGroup("enabled")
	enabled.Models = models.JSON(`[{"id":"gpt-4o","alias":"Primary"}]`)
	if err := fixture.db.Create(enabled).Error; err != nil {
		t.Fatalf("create enabled group: %v", err)
	}
	disabled := validControlGroup("disabled")
	if err := fixture.db.Create(disabled).Error; err != nil {
		t.Fatalf("create disabled group: %v", err)
	}
	if err := fixture.db.Model(disabled).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable group: %v", err)
	}
	for _, key := range []models.UpstreamKey{
		{GroupID: enabled.ID, KeyValue: "cipher-active", KeyHash: "hash-active", Status: models.UpstreamKeyStatusActive},
		{GroupID: enabled.ID, KeyValue: "cipher-disabled", KeyHash: "hash-disabled", Status: models.UpstreamKeyStatusDisabled},
	} {
		key := key
		if err := fixture.db.Create(&key).Error; err != nil {
			t.Fatalf("create upstream key: %v", err)
		}
	}

	queryCount := 0
	const callbackName = "test:list_groups_query_count"
	if err := fixture.db.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.DB) {
		queryCount++
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}

	got, err := fixture.service.ListGroups(context.Background())
	if err != nil {
		t.Fatalf("ListGroups() error = %v", err)
	}
	if queryCount != 2 {
		t.Fatalf("ListGroups() query count = %d, want 2", queryCount)
	}
	if len(got) != 2 {
		t.Fatalf("ListGroups() = %#v, want two groups", got)
	}
	if got[0].ID != enabled.ID || !got[0].Enabled || got[0].KeyCount != 2 {
		t.Fatalf("enabled response = %#v, want enabled with two total keys", got[0])
	}
	if len(got[0].Protocols) != 1 || len(got[0].Models) != 1 || got[0].Models[0].Alias != "Primary" {
		t.Fatalf("enabled protocols/models = %#v/%#v", got[0].Protocols, got[0].Models)
	}
	if got[1].ID != disabled.ID || got[1].Enabled || got[1].KeyCount != 0 {
		t.Fatalf("disabled response = %#v, want disabled with zero keys", got[1])
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal(groups) error = %v", err)
	}
	for _, forbidden := range []string{
		"key_value", "key_hash", "cipher-active", "cipher-disabled", "hash-active", "hash-disabled",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("groups response exposes %q: %s", forbidden, encoded)
		}
	}
}
