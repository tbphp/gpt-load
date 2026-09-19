package state

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/automodel"
)

func TestAutoResponseBindingIsImmutableAndRejectsDifferentSelection(t *testing.T) {
	bindings := NewResponseBindings()
	selection := &automodel.Selection{EntryID: "entry", EntryName: "auto", PresetID: "high", TargetModel: "model", ParameterOverrides: json.RawMessage(`[]`), TaskFingerprint: "task-one"}
	ref := CredentialRef{ID: 1, GroupID: 1, IdentityGeneration: 1}
	if !bindings.Record(1, "response", ref, selection) {
		t.Fatal("cannot record")
	}
	selection.TargetModel = "changed"
	selection.TaskFingerprint = "changed"
	value, _ := bindings.Lookup(1, "response")
	value.AutoSelection.TargetModel = "mutated"
	again, _ := bindings.Lookup(1, "response")
	if again.AutoSelection.TargetModel != "model" || again.AutoSelection.TaskFingerprint != "task-one" {
		t.Fatal("lookup leaked mutable binding snapshot")
	}
	restored := NewResponseBindings()
	if err := restored.RestoreCheckpoint(bindings.CaptureCheckpoint()); err != nil {
		t.Fatal(err)
	}
	afterRestore, ok := restored.Lookup(1, "response")
	if !ok || afterRestore.AutoSelection == nil || afterRestore.AutoSelection.TaskFingerprint != "task-one" {
		t.Fatalf("restored automatic binding = %#v, %t", afterRestore, ok)
	}
	if bindings.Record(1, "response", ref, selection) {
		t.Fatal("different automatic selection accepted for existing response")
	}
}
