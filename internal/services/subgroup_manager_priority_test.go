package services

import (
	"testing"

	"gpt-load/internal/models"
	"gpt-load/internal/store"

	"gorm.io/datatypes"
)

func TestSelectSubGroup_PriorityModeUsesStableFallbackOrder(t *testing.T) {
	memStore := store.NewMemoryStore()
	manager := NewSubGroupManager(memStore)

	group := &models.Group{
		ID:        200,
		Name:      "aggregate-priority",
		GroupType: "aggregate",
		Config: datatypes.JSONMap{
			"sub_group_selection_mode": "priority",
		},
		SubGroups: []models.GroupSubGroup{
			{ID: 10, GroupID: 200, SubGroupID: 101, SubGroupName: "group-a", Weight: 99},
			{ID: 11, GroupID: 200, SubGroupID: 102, SubGroupName: "group-b", Weight: 99},
			{ID: 12, GroupID: 200, SubGroupID: 103, SubGroupName: "group-c", Weight: 98},
		},
	}

	if err := memStore.LPush("group:101:active_keys", 1); err != nil {
		t.Fatalf("seed active keys: %v", err)
	}
	if err := memStore.LPush("group:102:active_keys", 2); err != nil {
		t.Fatalf("seed active keys: %v", err)
	}
	if err := memStore.LPush("group:103:active_keys", 3); err != nil {
		t.Fatalf("seed active keys: %v", err)
	}

	selected, err := manager.SelectSubGroup(group)
	if err != nil {
		t.Fatalf("SelectSubGroup returned error: %v", err)
	}
	if selected != "group-a" {
		t.Fatalf("expected highest priority stable winner group-a, got %s", selected)
	}

	if err := memStore.LRem("group:101:active_keys", 0, 1); err != nil {
		t.Fatalf("remove active key: %v", err)
	}

	fallback, err := manager.SelectSubGroup(group)
	if err != nil {
		t.Fatalf("SelectSubGroup fallback returned error: %v", err)
	}
	if fallback != "group-b" {
		t.Fatalf("expected fallback group-b, got %s", fallback)
	}
}
