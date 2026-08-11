package parametertrace

import (
	"strings"
	"testing"
)

func TestProjectJSONCapturesOnlyBoundedSafeParameters(t *testing.T) {
	body := []byte(`{
		"model":"secret-model-value",
		"messages":[{"role":"user","content":"top secret prompt","metadata":{"tenant":"private"}}],
		"stream":true,
		"max_tokens":2048,
		"temperature":0.2,
		"thinking":{"type":"enabled","budget_tokens":4096},
		"tools":[
			{"name":"lookup_weather","description":"secret description","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}},
			{"type":"web_search_20250305","name":"web_search"}
		],
		"output_config":{"effort":"high","format":{"type":"json_schema","name":"weather_result","strict":true,"schema":{"type":"object","description":"secret schema"}}},
		"content":[
			{"type":"text","text":"secret text"},
			{"type":"image","source":{"type":"base64","data":"secret binary"}},
			{"type":"document","source":{"type":"url","url":"https://secret.invalid/file"}}
		],
		"metadata":{"user_id":"secret-user"}
	}`)

	got := ProjectJSON(body)
	if got.SchemaVersion != SchemaVersion || got.State != CaptureCaptured || got.Truncated {
		t.Fatalf("ProjectJSON() version/state/truncated = %d/%q/%v", got.SchemaVersion, got.State, got.Truncated)
	}
	want := map[string]string{
		"stream":                           "true",
		"limits.output_tokens":             "2048",
		"sampling.temperature":             "0.2",
		"reasoning.mode":                   "enabled",
		"reasoning.budget_tokens":          "4096",
		"reasoning.effort":                 "high",
		"tools.count":                      "2",
		"tools.types":                      "function,web_search",
		"tools.type.function":              "true",
		"tools.type.web_search":            "true",
		"tools.names":                      "lookup_weather,web_search",
		"structured_output.type":           "json_schema",
		"structured_output.name":           "weather_result",
		"structured_output.strict":         "true",
		"structured_output.schema_present": "true",
		"modalities.text_count":            "1",
		"modalities.image_count":           "1",
		"modalities.file_count":            "1",
	}
	if len(got.Entries) != len(want) {
		t.Fatalf("entries = %#v, want %d entries", got.Entries, len(want))
	}
	for _, entry := range got.Entries {
		if want[entry.Path] != entry.Value {
			t.Errorf("entry %q = %q, want %q", entry.Path, entry.Value, want[entry.Path])
		}
	}
	encoded, err := MarshalSnapshot(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"top secret prompt", "secret description", "secret schema", "secret binary",
		"secret.invalid", "secret-user", "secret-model-value",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("snapshot leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestProjectJSONSupportsOpenAIAndGeminiParameterShapes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want map[string]string
	}{
		{
			name: "openai responses",
			body: `{"max_output_tokens":1024,"parallel_tool_calls":false,"reasoning":{"effort":"high"},"text":{"format":{"type":"json_schema","name":"answer","strict":false,"schema":{"type":"object"}}},"tools":[{"type":"function","name":"lookup"}]}`,
			want: map[string]string{
				"limits.output_tokens": "1024", "tools.parallel_calls": "false",
				"reasoning.effort": "high", "tools.count": "1", "tools.types": "function",
				"tools.type.function": "true",
				"tools.names":         "lookup", "structured_output.type": "json_schema",
				"structured_output.name": "answer", "structured_output.strict": "false",
				"structured_output.schema_present": "true",
			},
		},
		{
			name: "gemini",
			body: `{"generationConfig":{"maxOutputTokens":512,"topP":0.8,"thinkingConfig":{"thinkingBudget":256},"responseMimeType":"application/json","responseSchema":{"type":"OBJECT"}},"tools":[{"functionDeclarations":[{"name":"lookup"}]},{"googleSearch":{}}]}`,
			want: map[string]string{
				"limits.output_tokens": "512", "sampling.top_p": "0.8",
				"reasoning.budget_tokens": "256", "tools.count": "2",
				"tools.types": "function,web_search", "tools.names": "lookup",
				"tools.type.function": "true", "tools.type.web_search": "true",
				"structured_output.type": "json_schema", "structured_output.schema_present": "true",
			},
		},
		{
			name: "bedrock additional model fields",
			body: `{"additionalModelRequestFields":{"thinking":{"type":"enabled","budget_tokens":8192},"output_config":{"effort":"medium"}}}`,
			want: map[string]string{
				"reasoning.mode": "enabled", "reasoning.budget_tokens": "8192", "reasoning.effort": "medium",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ProjectJSON([]byte(test.body))
			for path, want := range test.want {
				found := false
				for _, entry := range got.Entries {
					if entry.Path == path {
						found = true
						if entry.Value != want {
							t.Errorf("%s = %q, want %q", path, entry.Value, want)
						}
					}
				}
				if !found {
					t.Errorf("missing %s in %#v", path, got.Entries)
				}
			}
		})
	}
}

func TestProjectJSONDoesNotTreatMessageOrToolContentAsReasoningConfiguration(t *testing.T) {
	body := []byte(`{
		"contents":[{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{"reasoning":{"effort":"customer_case_123"},"thinking":{"type":"private_case"}}}}]}],
		"messages":[{"role":"user","content":{"thinking":{"budget_tokens":987654}}}]
	}`)

	got := ProjectJSON(body)
	for _, entry := range got.Entries {
		if strings.HasPrefix(entry.Path, "reasoning.") {
			t.Fatalf("content value was projected as reasoning parameter: %#v", got.Entries)
		}
	}
	encoded, err := MarshalSnapshot(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"customer_case_123", "private_case", "987654"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("snapshot leaked content value %q: %s", forbidden, encoded)
		}
	}
}

func TestCompareClassifiesLossyProtocolConversion(t *testing.T) {
	client := Snapshot{SchemaVersion: SchemaVersion, State: CaptureCaptured, Entries: []Entry{
		{Path: "stream", Kind: ValueBoolean, Value: "true"},
		{Path: "limits.output_tokens", Kind: ValueNumber, Value: "4096"},
		{Path: "reasoning.budget_tokens", Kind: ValueNumber, Value: "4096"},
		{Path: "tools.types", Kind: ValueSummary, Value: "function,web_search"},
		{Path: "structured_output.strict", Kind: ValueBoolean, Value: "true"},
	}}
	target := Snapshot{SchemaVersion: SchemaVersion, State: CaptureCaptured, Entries: []Entry{
		{Path: "stream", Kind: ValueBoolean, Value: "true"},
		{Path: "limits.output_tokens", Kind: ValueNumber, Value: "2048"},
		{Path: "reasoning.effort", Kind: ValueEnum, Value: "high"},
		{Path: "tools.types", Kind: ValueSummary, Value: "function"},
		{Path: "tools.parallel_calls", Kind: ValueBoolean, Value: "true"},
	}}

	got := Compare(client, target)
	if got.SchemaVersion != SchemaVersion || got.Target.SchemaVersion != SchemaVersion {
		t.Fatalf("trace schema versions = %d/%d", got.SchemaVersion, got.Target.SchemaVersion)
	}
	want := map[string]Disposition{
		"stream->stream": DispositionPreserved,
		"limits.output_tokens->limits.output_tokens": DispositionNormalized,
		"reasoning.budget_tokens->reasoning.effort":  DispositionMapped,
		"tools.types->tools.types":                   DispositionNormalized,
		"structured_output.strict->":                 DispositionDropped,
		"->tools.parallel_calls":                     DispositionAdded,
	}
	if len(got.Changes) != len(want) {
		t.Fatalf("changes = %#v, want %d", got.Changes, len(want))
	}
	for _, change := range got.Changes {
		key := change.SourcePath + "->" + change.TargetPath
		if want[key] != change.Disposition {
			t.Errorf("change %s = %q, want %q", key, change.Disposition, want[key])
		}
	}
}

func TestCompareMarksFilteredToolSubtypeAsDropped(t *testing.T) {
	client := ProjectJSON([]byte(`{"tools":[{"type":"function","function":{"name":"lookup"}},{"type":"web_search"}]}`))
	target := ProjectJSON([]byte(`{"tools":[{"type":"function","function":{"name":"lookup"}}]}`))
	trace := Compare(client, target)
	for _, change := range trace.Changes {
		if change.Disposition == DispositionDropped &&
			change.SourcePath == "tools.type.web_search" && change.TargetPath == "" {
			return
		}
	}
	t.Fatalf("trace did not report filtered web_search tool: %#v", trace)
}

func TestProjectJSONRedactsCredentialLikeToolNamesBeforeLeavingExecution(t *testing.T) {
	snapshot := ProjectJSON([]byte(`{"tools":[{"name":"sk-supersecret99"}]}`))
	for _, entry := range snapshot.Entries {
		if entry.Path == "tools.names" {
			if entry.Value != "[REDACTED]" {
				t.Fatalf("tool name = %q, want redacted", entry.Value)
			}
			return
		}
	}
	t.Fatalf("tool names entry missing: %#v", snapshot)
}

func TestProjectJSONSkipsOversizeAndBoundsComplexInput(t *testing.T) {
	oversize := []byte(`{"messages":"` + strings.Repeat("x", MaxSourceBytes) + `"}`)
	if got := ProjectJSON(oversize); got.State != CaptureSkippedOversize || len(got.Entries) != 0 {
		t.Fatalf("oversize snapshot = %#v", got)
	}
	for _, invalid := range []string{`{"stream":true} {}`, `{"stream":true} trailing`} {
		if got := ProjectJSON([]byte(invalid)); got.State != CaptureUnavailable {
			t.Fatalf("invalid JSON snapshot = %#v", got)
		}
	}

	parts := make([]string, 0, 180)
	for index := 0; index < 180; index++ {
		parts = append(parts, `{"type":"function","name":"tool_`+strings.Repeat("x", 80)+`"}`)
	}
	got := ProjectJSON([]byte(`{"tools":[` + strings.Join(parts, ",") + `]}`))
	if got.State != CapturePartial || !got.Truncated || len(got.Entries) > 128 {
		t.Fatalf("bounded snapshot = %#v", got)
	}
	encoded, err := MarshalSnapshot(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 4<<10 {
		t.Fatalf("snapshot size = %d, want <= 4096", len(encoded))
	}
}

func TestClonePreservesEmptySlicesForJSONArrays(t *testing.T) {
	snapshot := Snapshot{
		SchemaVersion: SchemaVersion,
		State:         CaptureSkippedOversize,
		Entries:       []Entry{},
	}
	clonedSnapshot := CloneSnapshot(snapshot)
	if clonedSnapshot.Entries == nil {
		t.Fatal("CloneSnapshot() turned empty entries into nil")
	}
	storedNullSnapshot := CloneSnapshot(Snapshot{
		SchemaVersion: SchemaVersion,
		State:         CaptureSkippedOversize,
	})
	if storedNullSnapshot.Entries == nil {
		t.Fatal("CloneSnapshot() did not normalize nil entries")
	}
	encodedSnapshot, err := MarshalSnapshot(storedNullSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encodedSnapshot), `"entries":[]`) {
		t.Fatalf("snapshot JSON = %s, want empty entries array", encodedSnapshot)
	}

	trace := PreflightBlocked()
	clonedTrace := CloneTrace(trace)
	if clonedTrace.Target.Entries == nil || clonedTrace.Changes == nil {
		t.Fatal("CloneTrace() turned empty slices into nil")
	}
	encodedTrace, err := MarshalTrace(clonedTrace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encodedTrace), `"entries":[]`) ||
		!strings.Contains(string(encodedTrace), `"changes":[]`) {
		t.Fatalf("trace JSON = %s, want empty arrays", encodedTrace)
	}
}

func TestBoundTraceFitsRequestEventBudget(t *testing.T) {
	entries := make([]Entry, 0, 80)
	changes := make([]Change, 0, 80)
	for index := 0; index < 80; index++ {
		path := "tools.parameter_" + strings.Repeat("x", index%20) + string(rune('a'+index%26))
		entries = append(entries, Entry{Path: path, Kind: ValueSummary, Value: strings.Repeat("v", 180)})
		changes = append(changes, Change{Disposition: DispositionAdded, TargetPath: path})
	}
	trace := Trace{
		SchemaVersion: SchemaVersion,
		State:         CaptureCaptured,
		Target:        Snapshot{SchemaVersion: SchemaVersion, State: CaptureCaptured, Entries: entries},
		Changes:       changes,
	}
	bounded := BoundTrace(trace, 2048)
	encoded, err := MarshalTrace(bounded)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 2048 || bounded.State != CapturePartial || !bounded.Target.Truncated {
		t.Fatalf("bounded trace size/state = %d/%#v", len(encoded), bounded)
	}
	if err := bounded.Validate(); err != nil {
		t.Fatalf("bounded trace validation: %v", err)
	}
}

func TestValidateRejectsMissingOrUnknownSchemaVersion(t *testing.T) {
	for _, snapshot := range []Snapshot{
		{State: CaptureCaptured, Entries: []Entry{}},
		{SchemaVersion: SchemaVersion + 1, State: CaptureCaptured, Entries: []Entry{}},
	} {
		if err := snapshot.Validate(); err == nil {
			t.Fatalf("Snapshot.Validate() accepted schema version %d", snapshot.SchemaVersion)
		}
	}
	trace := PreflightBlocked()
	trace.SchemaVersion = 0
	if err := trace.Validate(); err == nil {
		t.Fatal("Trace.Validate() accepted missing schema version")
	}
}
