package gateway

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDataPlaneDependencyGraph(t *testing.T) {
	roots := []string{
		"gpt-load/internal/gateway",
		"gpt-load/internal/scheduler",
		"gpt-load/internal/health",
	}
	forbidden := []string{
		"gpt-load/internal/storage",
		"gpt-load/internal/control",
		"gorm.io/gorm",
	}

	for _, root := range roots {
		output, err := exec.Command("go", "list", "-deps", root).CombinedOutput()
		if err != nil {
			t.Fatalf("go list -deps %s: %v\n%s", root, err, output)
		}
		for _, dependency := range strings.Fields(string(output)) {
			for _, denied := range forbidden {
				if dependency == denied || strings.HasPrefix(dependency, denied+"/") {
					t.Errorf("%s transitively depends on forbidden package %s", root, dependency)
				}
			}
		}
	}
}
