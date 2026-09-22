package state

import "testing"

func TestConfiguredPriorityDefaultsAndPreserves(t *testing.T) {
	t.Parallel()
	if got := ConfiguredPriority(nil); got != DefaultPriority {
		t.Fatalf("ConfiguredPriority(nil) = %d, want %d", got, DefaultPriority)
	}
	value := 80
	if got := ConfiguredPriority(&value); got != 80 {
		t.Fatalf("ConfiguredPriority(80) = %d, want 80", got)
	}
}

func TestValidateManualPriority(t *testing.T) {
	t.Parallel()
	if err := validateManualPriority("group", nil); err != nil {
		t.Fatalf("nil priority error = %v", err)
	}
	valid := MinPriority
	if err := validateManualPriority("group", &valid); err != nil {
		t.Fatalf("min priority error = %v", err)
	}
	valid = MaxPriority
	if err := validateManualPriority("group", &valid); err != nil {
		t.Fatalf("max priority error = %v", err)
	}
	zero := 0
	if err := validateManualPriority("group", &zero); err == nil {
		t.Fatal("zero priority accepted")
	}
	tooHigh := MaxPriority + 1
	if err := validateManualPriority("group", &tooHigh); err == nil {
		t.Fatal("oversized priority accepted")
	}
}
