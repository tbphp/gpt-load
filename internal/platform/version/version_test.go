package version

import "testing"

func TestDefaultVersionIdentifiesV2DevelopmentBuild(t *testing.T) {
	if Version != "2.0.0-dev" {
		t.Fatalf("Version = %q, want 2.0.0-dev", Version)
	}
}
