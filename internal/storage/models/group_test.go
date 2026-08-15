package models

import "testing"

func TestGroupBeforeSaveDoesNotInventConnectionType(t *testing.T) {
	t.Parallel()

	group := Group{}
	if err := group.BeforeSave(nil); err != nil {
		t.Fatalf("BeforeSave() error = %v", err)
	}
	if group.ConnectionType != "" {
		t.Fatalf("connection type = %q, want empty for fail-closed validation", group.ConnectionType)
	}
	if string(group.Params) != `{}` {
		t.Fatalf("params = %s, want {}", group.Params)
	}
}
