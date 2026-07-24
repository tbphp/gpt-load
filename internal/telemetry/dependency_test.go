package telemetry

import (
	"os/exec"
	"strings"
	"testing"
)

func TestTelemetryDependencyGraph(t *testing.T) {
	output, err := exec.Command("go", "list", "-deps", "gpt-load/internal/telemetry").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps telemetry: %v\n%s", err, output)
	}
	for _, dependency := range strings.Fields(string(output)) {
		for _, denied := range []string{
			"gpt-load/internal/storage",
			"gpt-load/internal/control",
			"gorm.io/gorm",
			"github.com/gin-gonic/gin",
		} {
			if dependency == denied || strings.HasPrefix(dependency, denied+"/") {
				t.Fatalf("telemetry depends on forbidden package %s", dependency)
			}
		}
	}
}
