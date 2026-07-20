package state

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
)

func TestCompileIndexesExternalModelsAndPreservesUpstreamIDs(t *testing.T) {
	input := CompileInput{Groups: []GroupConfig{
		{
			ID: 1, Name: "one", UpstreamURL: "https://one.example.com",
			Protocols: []protocol.Protocol{protocol.OpenAI},
			Models: []ModelConfig{
				{ID: "provider-a", Alias: "public"},
				{ID: "provider-a", Alias: "secondary"},
				{ID: "plain"},
			},
			Enabled: true,
		},
		{
			ID: 2, Name: "two", UpstreamURL: "https://two.example.com",
			Protocols: []protocol.Protocol{protocol.OpenAI},
			Models:    []ModelConfig{{ID: "provider-b", Alias: "public"}}, Enabled: true,
		},
	}}

	snapshot, err := Compile(input)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	wantPublic := []RouteTarget{
		{GroupID: 1, UpstreamModelID: "provider-a"},
		{GroupID: 2, UpstreamModelID: "provider-b"},
	}
	if got := snapshot.Candidates[protocol.OpenAI]["public"]; !reflect.DeepEqual(got, wantPublic) {
		t.Fatalf("public targets = %#v, want %#v", got, wantPublic)
	}
	if got := snapshot.Candidates[protocol.OpenAI]["secondary"]; len(got) != 1 || got[0].UpstreamModelID != "provider-a" {
		t.Fatalf("secondary targets = %#v", got)
	}
	if got := snapshot.Candidates[protocol.OpenAI]["plain"]; len(got) != 1 || got[0].UpstreamModelID != "plain" {
		t.Fatalf("plain targets = %#v", got)
	}
	if _, exists := snapshot.Candidates[protocol.OpenAI]["provider-a"]; exists {
		t.Fatal("aliased upstream id entered external index")
	}
}

func TestCompileRejectsDuplicateExternalModelWithinGroup(t *testing.T) {
	tests := []struct {
		name   string
		models []ModelConfig
	}{
		{name: "two ids share alias", models: []ModelConfig{{ID: "a", Alias: "public"}, {ID: "b", Alias: "public"}}},
		{name: "alias collides with plain id", models: []ModelConfig{{ID: "a"}, {ID: "b", Alias: "a"}}},
		{name: "duplicate entry", models: []ModelConfig{{ID: "a"}, {ID: "a"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile(CompileInput{Groups: []GroupConfig{{
				ID: 1, Name: "group", UpstreamURL: "https://example.com",
				Protocols: []protocol.Protocol{protocol.OpenAI}, Models: test.models, Enabled: true,
			}}})
			if err == nil || !strings.Contains(err.Error(), "duplicate external model") {
				t.Fatalf("Compile() error = %v", err)
			}
		})
	}
}

func TestCompileSkipsDisabledGroupValidation(t *testing.T) {
	snapshot, err := Compile(CompileInput{Groups: []GroupConfig{{
		ID: 1, Name: "disabled", Enabled: false,
		Models: []ModelConfig{{ID: " ", Alias: "public"}, {ID: "other", Alias: "public"}},
	}}})
	if err != nil {
		t.Fatalf("Compile() error = %v, want disabled group skipped", err)
	}
	if len(snapshot.Groups) != 0 || len(snapshot.Candidates) != 0 {
		t.Fatalf("snapshot includes disabled group: %#v", snapshot)
	}
}

func TestCompileMergesTimeoutsAndHeaderRules(t *testing.T) {
	systemSettings := config.Settings{
		"connect_timeout":    10.0,
		"first_byte_timeout": 90.0,
		"header_rules": map[string]any{
			"set":    map[string]any{"X-System": "system"},
			"remove": []any{"X-System-Remove"},
		},
	}
	groupSettings := config.Settings{
		"connect_timeout":     20.0,
		"request_timeout":     500.0,
		"stream_idle_timeout": 250.0,
		"header_rules": map[string]any{
			"set":    map[string]any{"X-Group": "${API_KEY}"},
			"remove": []any{"X-Group-Remove"},
		},
	}

	snapshot, err := Compile(runtimeSettingsInput(systemSettings, groupSettings))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	group := snapshot.Groups[1]
	wantTimeouts := TimeoutConfig{
		Connect:    20 * time.Second,
		FirstByte:  90 * time.Second,
		Request:    500 * time.Second,
		StreamIdle: 250 * time.Second,
	}
	if group.Timeouts != wantTimeouts {
		t.Errorf("GroupView.Timeouts = %#v, want %#v", group.Timeouts, wantTimeouts)
	}
	wantRules := HeaderRules{
		Set:    map[string]string{"X-Group": "${API_KEY}"},
		Remove: []string{"X-Group-Remove"},
	}
	if !reflect.DeepEqual(group.HeaderRules, wantRules) {
		t.Errorf("GroupView.HeaderRules = %#v, want %#v", group.HeaderRules, wantRules)
	}
}

func TestCompileUsesDefaultRuntimeSettings(t *testing.T) {
	snapshot, err := Compile(runtimeSettingsInput(nil, nil))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	group := snapshot.Groups[1]
	wantTimeouts := TimeoutConfig{
		Connect:    15 * time.Second,
		FirstByte:  120 * time.Second,
		Request:    600 * time.Second,
		StreamIdle: 300 * time.Second,
	}
	if group.Timeouts != wantTimeouts {
		t.Errorf("GroupView.Timeouts = %#v, want %#v", group.Timeouts, wantTimeouts)
	}
	wantRules := HeaderRules{Set: map[string]string{}}
	if !reflect.DeepEqual(group.HeaderRules, wantRules) {
		t.Errorf("GroupView.HeaderRules = %#v, want %#v", group.HeaderRules, wantRules)
	}
}

func TestCompileCanonicalizesHeaderRuleNames(t *testing.T) {
	snapshot, err := Compile(runtimeSettingsInput(nil, config.Settings{
		"header_rules": map[string]any{
			"set":    map[string]any{"x-custom-key": "value"},
			"remove": []any{"x-remove-me"},
		},
	}))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	want := HeaderRules{
		Set:    map[string]string{"X-Custom-Key": "value"},
		Remove: []string{"X-Remove-Me"},
	}
	if got := snapshot.Groups[1].HeaderRules; !reflect.DeepEqual(got, want) {
		t.Fatalf("GroupView.HeaderRules = %#v, want %#v", got, want)
	}
}

func TestCompileAcceptsJSONNumberWholeSecondTimeouts(t *testing.T) {
	tests := []struct {
		literal string
		want    time.Duration
	}{
		{literal: "20.0", want: 20 * time.Second},
		{literal: "2e1", want: 20 * time.Second},
		{literal: "9223372036.0", want: 9223372036 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.literal, func(t *testing.T) {
			snapshot, err := Compile(runtimeSettingsInput(nil, config.Settings{
				"connect_timeout": json.Number(test.literal),
			}))
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			if got := snapshot.Groups[1].Timeouts.Connect; got != test.want {
				t.Fatalf("Connect timeout = %s, want %s", got, test.want)
			}
		})
	}
}

func TestCompileRejectsMalformedRuntimeSettings(t *testing.T) {
	tests := []struct {
		name          string
		groupSettings config.Settings
		wantErr       string
	}{
		{
			name:          "zero timeout",
			groupSettings: config.Settings{"connect_timeout": 0},
			wantErr:       "connect_timeout must be a positive whole number",
		},
		{
			name:          "negative timeout",
			groupSettings: config.Settings{"first_byte_timeout": int64(-1)},
			wantErr:       "first_byte_timeout must be a positive whole number",
		},
		{
			name:          "fractional timeout",
			groupSettings: config.Settings{"request_timeout": 1.5},
			wantErr:       "request_timeout must be a positive whole number",
		},
		{
			name:          "precise fractional JSON timeout",
			groupSettings: config.Settings{"request_timeout": json.Number("20.0000000000000001")},
			wantErr:       "request_timeout must be a positive whole number",
		},
		{
			name:          "precise fractional JSON timeout at limit",
			groupSettings: config.Settings{"request_timeout": json.Number("9223372036.0000001")},
			wantErr:       "request_timeout must be a positive whole number",
		},
		{
			name:          "overflow timeout",
			groupSettings: config.Settings{"stream_idle_timeout": json.Number("9223372037")},
			wantErr:       "stream_idle_timeout must be a positive whole number",
		},
		{
			name:          "non-object header rules",
			groupSettings: config.Settings{"header_rules": []any{}},
			wantErr:       "header_rules must be an object",
		},
		{
			name: "non-object header set",
			groupSettings: config.Settings{"header_rules": map[string]any{
				"set": []any{},
			}},
			wantErr: "header_rules.set must be an object",
		},
		{
			name: "non-string header set value",
			groupSettings: config.Settings{"header_rules": map[string]any{
				"set": map[string]any{"X-Invalid": 1.0},
			}},
			wantErr: "header_rules.set.X-Invalid must be a string",
		},
		{
			name: "empty header set name",
			groupSettings: config.Settings{"header_rules": map[string]any{
				"set": map[string]any{"": "value"},
			}},
			wantErr: "header_rules.set contains invalid header name \"\"",
		},
		{
			name: "header set name with space",
			groupSettings: config.Settings{"header_rules": map[string]any{
				"set": map[string]any{"Bad Header": "value"},
			}},
			wantErr: "header_rules.set contains invalid header name \"Bad Header\"",
		},
		{
			name: "header set value with newline",
			groupSettings: config.Settings{"header_rules": map[string]any{
				"set": map[string]any{"X-Credential": "prefix\r\nInjected: value"},
			}},
			wantErr: "header_rules.set.X-Credential contains invalid header value",
		},
		{
			name: "case-insensitive duplicate header set",
			groupSettings: config.Settings{"header_rules": map[string]any{
				"set": map[string]any{
					"Authorization": "Bearer one",
					"authorization": "Bearer two",
				},
			}},
			wantErr: "header_rules.set contains duplicate header \"Authorization\"",
		},
		{
			name: "non-array header remove",
			groupSettings: config.Settings{"header_rules": map[string]any{
				"remove": "X-Invalid",
			}},
			wantErr: "header_rules.remove must be an array",
		},
		{
			name: "non-string header remove entry",
			groupSettings: config.Settings{"header_rules": map[string]any{
				"remove": []any{"X-Valid", 1.0},
			}},
			wantErr: "header_rules.remove[1] must be a string",
		},
		{
			name: "invalid header remove name",
			groupSettings: config.Settings{"header_rules": map[string]any{
				"remove": []any{"Bad Header"},
			}},
			wantErr: "header_rules.remove[0] contains invalid header name \"Bad Header\"",
		},
		{
			name: "unknown header rules field",
			groupSettings: config.Settings{"header_rules": map[string]any{
				"append": map[string]any{"X-Invalid": "value"},
			}},
			wantErr: "unknown header_rules field \"append\"",
		},
		{
			name:          "unknown group setting",
			groupSettings: config.Settings{"retry_count": 3.0},
			wantErr:       "unknown group setting \"retry_count\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Compile(runtimeSettingsInput(nil, tt.groupSettings))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Compile() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestCompileRuntimeSettingsOwnsIndependentCopies(t *testing.T) {
	set := map[string]any{"X-Original": "original"}
	remove := []any{"X-Original-Remove"}
	headerRules := map[string]any{"set": set, "remove": remove}
	groupSettings := config.Settings{"header_rules": headerRules}

	snapshot, err := Compile(runtimeSettingsInput(nil, groupSettings))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	set["X-Original"] = "mutated"
	set["X-Added"] = "added"
	remove[0] = "X-Mutated-Remove"
	headerRules["set"] = map[string]any{"X-Replaced": "replaced"}
	headerRules["remove"] = []any{"X-Replaced-Remove"}
	groupSettings["header_rules"] = map[string]any{}

	wantRules := HeaderRules{
		Set:    map[string]string{"X-Original": "original"},
		Remove: []string{"X-Original-Remove"},
	}
	if got := snapshot.Groups[1].HeaderRules; !reflect.DeepEqual(got, wantRules) {
		t.Errorf("GroupView.HeaderRules = %#v after input mutation, want %#v", got, wantRules)
	}
}

func runtimeSettingsInput(systemSettings, groupSettings config.Settings) CompileInput {
	return CompileInput{
		SystemSettings: systemSettings,
		Groups: []GroupConfig{{
			ID:        1,
			Protocols: []protocol.Protocol{protocol.OpenAI},
			Models:    []ModelConfig{{ID: "model"}},
			Settings:  groupSettings,
			Enabled:   true,
		}},
	}
}

func TestCompileRejectsInvalidCandidateConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		input   CompileInput
		wantErr string
	}{
		{
			name: "zero group id",
			input: CompileInput{Groups: []GroupConfig{{
				Protocols: []protocol.Protocol{protocol.OpenAI},
				Models:    []ModelConfig{{ID: "model"}},
				Enabled:   true,
			}}},
			wantErr: "group id is required",
		},
		{
			name: "duplicate enabled group id",
			input: CompileInput{Groups: []GroupConfig{
				{
					ID:        1,
					Protocols: []protocol.Protocol{protocol.OpenAI},
					Models:    []ModelConfig{{ID: "model-one"}},
					Enabled:   true,
				},
				{
					ID:        1,
					Protocols: []protocol.Protocol{protocol.Anthropic},
					Models:    []ModelConfig{{ID: "model-two"}},
					Enabled:   true,
				},
			}},
			wantErr: "duplicate group id",
		},
		{
			name: "empty protocol list",
			input: CompileInput{Groups: []GroupConfig{{
				ID:      1,
				Models:  []ModelConfig{{ID: "model"}},
				Enabled: true,
			}}},
			wantErr: "protocols are required",
		},
		{
			name: "unknown protocol",
			input: CompileInput{Groups: []GroupConfig{{
				ID:        1,
				Protocols: []protocol.Protocol{"unknown"},
				Models:    []ModelConfig{{ID: "model"}},
				Enabled:   true,
			}}},
			wantErr: "invalid protocol",
		},
		{
			name: "duplicate protocol",
			input: CompileInput{Groups: []GroupConfig{{
				ID:        1,
				Protocols: []protocol.Protocol{protocol.OpenAI, protocol.OpenAI},
				Models:    []ModelConfig{{ID: "model"}},
				Enabled:   true,
			}}},
			wantErr: "duplicate protocol",
		},
		{
			name: "whitespace model id",
			input: CompileInput{Groups: []GroupConfig{{
				ID:        1,
				Protocols: []protocol.Protocol{protocol.OpenAI},
				Models:    []ModelConfig{{ID: " \t"}},
				Enabled:   true,
			}}},
			wantErr: "model id is required",
		},
		{
			name: "active access key without hash",
			input: CompileInput{AccessKeys: []AccessKeyConfig{{
				ID:     10,
				Status: AccessKeyStatusActive,
			}}},
			wantErr: "key hash is required",
		},
		{
			name: "invalid access key status",
			input: CompileInput{AccessKeys: []AccessKeyConfig{{
				ID:      10,
				KeyHash: "hash",
				Status:  AccessKeyStatus("invalid"),
			}}},
			wantErr: "invalid status",
		},
		{
			name: "duplicate active access key hash",
			input: CompileInput{AccessKeys: []AccessKeyConfig{
				{ID: 10, KeyHash: "duplicate", Status: AccessKeyStatusActive},
				{ID: 11, KeyHash: "duplicate", Status: AccessKeyStatusActive},
			}},
			wantErr: "duplicate access key hash",
		},
		{
			name: "invalid filter protocol",
			input: CompileInput{AccessKeys: []AccessKeyConfig{{
				ID:      10,
				KeyHash: "hash",
				Status:  AccessKeyStatusActive,
				Filters: FilterSet{Protocols: map[protocol.Protocol]struct{}{"invalid": {}}},
			}}},
			wantErr: "invalid protocol",
		},
		{
			name: "blank filter model",
			input: CompileInput{AccessKeys: []AccessKeyConfig{{
				ID:      10,
				KeyHash: "hash",
				Status:  AccessKeyStatusActive,
				Filters: FilterSet{Models: map[string]struct{}{" ": {}}},
			}}},
			wantErr: "filter model is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Compile(tt.input)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Compile() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestCompileAllowsDanglingFilterGroupIDs(t *testing.T) {
	input := CompileInput{AccessKeys: []AccessKeyConfig{{
		ID:      10,
		KeyHash: "hash",
		Status:  AccessKeyStatusActive,
		Filters: FilterSet{Groups: map[uint]struct{}{999: {}}},
	}}}

	snapshot, err := Compile(input)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	accessKey, ok := snapshot.AccessKeysByHash["hash"]
	if !ok {
		t.Fatal("active access key missing")
	}
	if _, ok := accessKey.Filters.Groups[999]; !ok {
		t.Fatal("dangling group filter id was not preserved")
	}
}

func TestCompileOwnsIndependentCopiesOfInput(t *testing.T) {
	protocols := []protocol.Protocol{protocol.OpenAI}
	models := []ModelConfig{{ID: "model-real", Alias: "model-alias"}}
	filterGroups := map[uint]struct{}{1: {}}
	filterProtocols := map[protocol.Protocol]struct{}{protocol.OpenAI: {}}
	filterModels := map[string]struct{}{"model-real": {}}
	input := CompileInput{
		Groups: []GroupConfig{{
			ID:        1,
			Protocols: protocols,
			Models:    models,
			Enabled:   true,
		}},
		AccessKeys: []AccessKeyConfig{{
			ID:      10,
			KeyHash: "hash",
			Status:  AccessKeyStatusActive,
			Filters: FilterSet{
				Groups:    filterGroups,
				Protocols: filterProtocols,
				Models:    filterModels,
			},
		}},
	}

	snapshot, err := Compile(input)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	protocols[0] = protocol.Anthropic
	models[0] = ModelConfig{ID: "changed", Alias: "changed"}
	delete(filterGroups, 1)
	filterGroups[2] = struct{}{}
	delete(filterProtocols, protocol.OpenAI)
	filterProtocols[protocol.Gemini] = struct{}{}
	delete(filterModels, "model-real")
	filterModels["changed"] = struct{}{}

	group := snapshot.Groups[1]
	if got := group.Protocols[0]; got != protocol.OpenAI {
		t.Errorf("GroupView.Protocols[0] = %q, want %q", got, protocol.OpenAI)
	}
	if got := group.Models[0]; got != (ModelConfig{ID: "model-real", Alias: "model-alias"}) {
		t.Errorf("GroupView.Models[0] = %#v, want original model", got)
	}
	filters := snapshot.AccessKeysByHash["hash"].Filters
	if _, ok := filters.Groups[1]; !ok {
		t.Error("AccessKeyView.Filters.Groups lost original group")
	}
	if _, ok := filters.Groups[2]; ok {
		t.Error("AccessKeyView.Filters.Groups contains caller mutation")
	}
	if _, ok := filters.Protocols[protocol.OpenAI]; !ok {
		t.Error("AccessKeyView.Filters.Protocols lost original protocol")
	}
	if _, ok := filters.Protocols[protocol.Gemini]; ok {
		t.Error("AccessKeyView.Filters.Protocols contains caller mutation")
	}
	if _, ok := filters.Models["model-real"]; !ok {
		t.Error("AccessKeyView.Filters.Models lost original model")
	}
	if _, ok := filters.Models["changed"]; ok {
		t.Error("AccessKeyView.Filters.Models contains caller mutation")
	}
}
