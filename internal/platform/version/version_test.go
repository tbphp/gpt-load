package version

import "testing"

func TestDefaultVersionIdentifiesV2TestBuild(t *testing.T) {
	if Version != "2.0.0-dev-test" {
		t.Fatalf("Version = %q, want 2.0.0-dev-test", Version)
	}
}
