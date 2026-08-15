package channel

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/channel/spec"
)

func TestCompilerRejectsExtensionImplementedByAnotherChannelModule(t *testing.T) {
	t.Parallel()

	all := builtInModules()
	vertex := findModule(t, all, GoogleVertex)
	openAI := findModule(t, all, OpenAI)
	openAI.Extensions = vertex.Extensions
	vertex.Extensions = spec.Extensions{}

	if _, err := compileBuiltInModules([]spec.Module{openAI, vertex}); err == nil {
		t.Fatal("compileBuiltInModules() accepted a cross-channel extension")
	}
}

func TestCompilerRejectsUnreferencedChannelExtension(t *testing.T) {
	t.Parallel()

	openAI := findModule(t, builtInModules(), OpenAI)
	openAI.Extensions.ParamsNormalizers = map[spec.ParamsNormalizerID]spec.ParamsNormalizer{
		"unused": func(json.RawMessage) (map[string]string, error) {
			return map[string]string{}, nil
		},
	}

	if _, err := compileBuiltInModules([]spec.Module{openAI}); err == nil {
		t.Fatal("compileBuiltInModules() accepted an unreferenced channel extension")
	}
}

func findModule(t *testing.T, all []spec.Module, id spec.ID) spec.Module {
	t.Helper()
	for _, candidate := range all {
		if candidate.Definition.ID == id {
			return candidate
		}
	}
	t.Fatalf("channel module %q not found", id)
	return spec.Module{}
}
