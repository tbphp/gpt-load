package channel

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/channel/modules"
)

func TestCompilerRejectsExtensionImplementedByAnotherChannelModule(t *testing.T) {
	t.Parallel()

	all := modules.All()
	vertex := findModule(t, all, modules.GoogleVertex)
	openAI := findModule(t, all, modules.OpenAI)
	openAI.Extensions = vertex.Extensions
	vertex.Extensions = modules.Extensions{}

	if _, err := compileBuiltInModules([]modules.Module{openAI, vertex}); err == nil {
		t.Fatal("compileBuiltInModules() accepted a cross-channel extension")
	}
}

func TestCompilerRejectsUnreferencedChannelExtension(t *testing.T) {
	t.Parallel()

	openAI := findModule(t, modules.All(), modules.OpenAI)
	openAI.Extensions.ParamsNormalizers = map[modules.ParamsNormalizerID]modules.ParamsNormalizer{
		"unused": func(json.RawMessage) (map[string]string, error) {
			return map[string]string{}, nil
		},
	}

	if _, err := compileBuiltInModules([]modules.Module{openAI}); err == nil {
		t.Fatal("compileBuiltInModules() accepted an unreferenced channel extension")
	}
}

func findModule(t *testing.T, all []modules.Module, id modules.ID) modules.Module {
	t.Helper()
	for _, candidate := range all {
		if candidate.Definition.ID == id {
			return candidate
		}
	}
	t.Fatalf("channel module %q not found", id)
	return modules.Module{}
}
