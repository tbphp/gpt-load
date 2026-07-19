package dialect

import (
	"fmt"
	"testing"
)

func TestModelListCollectorPreservesFirstOccurrenceOrder(t *testing.T) {
	collector := newModelListCollector()
	if err := collector.Add([]string{"claude-a", "Shared", "claude-a"}); err != nil {
		t.Fatalf("first Add() error = %v", err)
	}
	if err := collector.Add([]string{"shared", "Shared", "claude-b"}); err != nil {
		t.Fatalf("second Add() error = %v", err)
	}

	result := collector.Result()
	if got, want := fmt.Sprint(result), "[claude-a Shared shared claude-b]"; got != want {
		t.Fatalf("Result() = %s, want %s", got, want)
	}
	result[0] = "mutated"
	if got := collector.Result()[0]; got != "claude-a" {
		t.Fatalf("Result() exposed collector storage: first value = %q", got)
	}
}

func TestModelListCollectorRejectsMoreThanMaximumUniqueModels(t *testing.T) {
	collector := newModelListCollector()
	values := make([]string, maxUniqueModelListEntries)
	for index := range values {
		values[index] = fmt.Sprintf("model-%06d", index)
	}
	if err := collector.Add(values); err != nil {
		t.Fatalf("Add(exact maximum) error = %v", err)
	}
	if !collector.Full() {
		t.Fatal("Full() = false at exact maximum")
	}
	if err := collector.Add([]string{values[0]}); err != nil {
		t.Fatalf("Add(duplicate at maximum) error = %v", err)
	}
	if err := collector.Add([]string{"one-too-many"}); err == nil {
		t.Fatal("Add(100001st unique model) error = nil")
	}
	if got := len(collector.Result()); got != maxUniqueModelListEntries {
		t.Fatalf("Result() length = %d, want %d", got, maxUniqueModelListEntries)
	}
}
