package storage

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/automodel"
	"gpt-load/internal/state"
)

func TestAutoResponseSelectionIsSharedAcrossInstances(t *testing.T) {
	db := openInternalMigrationTestDatabase(t)
	if err := AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	one, two := NewResponseBindings(db), NewResponseBindings(db)
	selection := &automodel.Selection{EntryID: "auto", EntryName: "auto", PresetID: "high", TargetModel: "strong", ParameterOverrides: json.RawMessage(`[]`)}
	if !one.Record(1, "response", state.CredentialRef{ID: 1, GroupID: 1, IdentityGeneration: 1}, selection) {
		t.Fatal("cannot save shared automatic selection")
	}
	value, found := two.Lookup(1, "response")
	if !found || value.AutoSelection == nil || value.AutoSelection.TargetModel != "strong" {
		t.Fatal("second instance cannot resume automatic selection")
	}
	selection.TargetModel = "different"
	if two.Record(1, "response", state.CredentialRef{ID: 1, GroupID: 1, IdentityGeneration: 1}, selection) {
		t.Fatal("conflicting shared selection accepted")
	}
}
