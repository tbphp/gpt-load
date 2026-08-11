// Package parametertrace extracts bounded, non-content request parameter
// observations for protocol-conversion diagnostics.
package parametertrace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"gpt-load/internal/platform/redact"
)

const (
	SchemaVersion     = 1
	MaxSourceBytes    = 256 << 10
	MaxSnapshotBytes  = 4 << 10
	MaxTraceBytes     = 16 << 10
	MaxEventBytes     = 16 << 10
	maxEntries        = 128
	maxTraversalDepth = 8
	maxValueBytes     = 256
	truncatedMarker   = "...[truncated]"
)

type CaptureState string

const (
	CaptureCaptured         CaptureState = "captured"
	CapturePartial          CaptureState = "partial"
	CaptureUnavailable      CaptureState = "unavailable"
	CaptureSkippedOversize  CaptureState = "skipped_oversize"
	CapturePreflightBlocked CaptureState = "preflight_blocked"
)

type ValueKind string

const (
	ValueBoolean ValueKind = "boolean"
	ValueNumber  ValueKind = "number"
	ValueEnum    ValueKind = "enum"
	ValueSummary ValueKind = "summary"
)

type Entry struct {
	Path  string    `json:"path"`
	Kind  ValueKind `json:"kind"`
	Value string    `json:"value"`
}

type Snapshot struct {
	SchemaVersion int          `json:"schema_version"`
	State         CaptureState `json:"state"`
	Entries       []Entry      `json:"entries"`
	Truncated     bool         `json:"truncated"`
}

type Disposition string

const (
	DispositionPreserved  Disposition = "preserved"
	DispositionMapped     Disposition = "mapped"
	DispositionNormalized Disposition = "normalized"
	DispositionDropped    Disposition = "dropped"
	DispositionAdded      Disposition = "added"
)

type Change struct {
	Disposition Disposition `json:"disposition"`
	SourcePath  string      `json:"source_path,omitempty"`
	TargetPath  string      `json:"target_path,omitempty"`
}

type Trace struct {
	SchemaVersion int          `json:"schema_version"`
	State         CaptureState `json:"state"`
	Target        Snapshot     `json:"target"`
	Changes       []Change     `json:"changes"`
}

func ProjectJSON(body []byte) Snapshot {
	if len(body) > MaxSourceBytes {
		return emptySnapshot(CaptureSkippedOversize)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if len(body) == 0 || decoder.Decode(&value) != nil {
		return emptySnapshot(CaptureUnavailable)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return emptySnapshot(CaptureUnavailable)
	}
	root, ok := value.(map[string]any)
	if !ok {
		return emptySnapshot(CaptureUnavailable)
	}
	builder := newProjectionBuilder()
	builder.projectScalars(root)
	builder.projectReasoning(root)
	builder.projectTools(root)
	builder.projectStructuredOutput(root)
	builder.projectModalities(root)
	return builder.snapshot()
}

func Compare(client Snapshot, target Snapshot) Trace {
	trace := Trace{
		SchemaVersion: SchemaVersion,
		State:         target.State,
		Target:        CloneSnapshot(target),
		Changes:       []Change{},
	}
	if client.State != CaptureCaptured && client.State != CapturePartial {
		trace.State = client.State
		return trace
	}
	if target.State != CaptureCaptured && target.State != CapturePartial {
		return trace
	}
	if client.State == CapturePartial || target.State == CapturePartial ||
		client.Truncated || target.Truncated {
		trace.State = CapturePartial
	}

	source := entriesByPath(client.Entries)
	destination := entriesByPath(target.Entries)
	for _, pair := range [][2]string{
		{"reasoning.budget_tokens", "reasoning.effort"},
		{"reasoning.effort", "reasoning.budget_tokens"},
	} {
		if _, sourceExists := source[pair[0]]; !sourceExists {
			continue
		}
		if _, targetExists := destination[pair[1]]; !targetExists {
			continue
		}
		trace.Changes = append(trace.Changes, Change{
			Disposition: DispositionMapped,
			SourcePath:  pair[0],
			TargetPath:  pair[1],
		})
		delete(source, pair[0])
		delete(destination, pair[1])
	}

	paths := sortedEntryPaths(source)
	for _, path := range paths {
		sourceEntry := source[path]
		if targetEntry, exists := destination[path]; exists {
			disposition := DispositionPreserved
			if sourceEntry.Kind != targetEntry.Kind || sourceEntry.Value != targetEntry.Value {
				disposition = DispositionNormalized
			}
			trace.Changes = append(trace.Changes, Change{
				Disposition: disposition,
				SourcePath:  path,
				TargetPath:  path,
			})
			delete(destination, path)
			continue
		}
		trace.Changes = append(trace.Changes, Change{
			Disposition: DispositionDropped,
			SourcePath:  path,
		})
	}
	for _, path := range sortedEntryPaths(destination) {
		trace.Changes = append(trace.Changes, Change{
			Disposition: DispositionAdded,
			TargetPath:  path,
		})
	}
	return boundTrace(trace)
}

func PreflightBlocked() Trace {
	return Trace{
		SchemaVersion: SchemaVersion,
		State:         CapturePreflightBlocked,
		Target: Snapshot{
			SchemaVersion: SchemaVersion,
			State:         CapturePreflightBlocked,
			Entries:       []Entry{},
		},
		Changes: []Change{},
	}
}

func CloneSnapshot(value Snapshot) Snapshot {
	clone := value
	clone.Entries = make([]Entry, len(value.Entries))
	copy(clone.Entries, value.Entries)
	return clone
}

func CloneTrace(value Trace) Trace {
	clone := value
	clone.Target = CloneSnapshot(value.Target)
	clone.Changes = make([]Change, len(value.Changes))
	copy(clone.Changes, value.Changes)
	return clone
}

func BoundTrace(value Trace, maxBytes int) Trace {
	trace := CloneTrace(value)
	if maxBytes <= 0 || maxBytes > MaxTraceBytes {
		maxBytes = MaxTraceBytes
	}
	sort.Slice(trace.Target.Entries, func(left, right int) bool {
		return trace.Target.Entries[left].Path < trace.Target.Entries[right].Path
	})
	for {
		encoded, err := json.Marshal(trace.Target)
		if err == nil && len(encoded) <= MaxSnapshotBytes {
			break
		}
		if len(trace.Target.Entries) == 0 {
			markTracePartial(&trace)
			break
		}
		trace.Target.Entries = trace.Target.Entries[:len(trace.Target.Entries)-1]
		markTracePartial(&trace)
	}
	if len(trace.Changes) > maxEntries {
		trace.Changes = trace.Changes[:maxEntries]
		markTracePartial(&trace)
	}
	for {
		encoded, err := json.Marshal(trace)
		if err == nil && len(encoded) <= maxBytes {
			return trace
		}
		if len(trace.Changes) > 0 {
			trace.Changes = trace.Changes[:len(trace.Changes)-1]
			markTracePartial(&trace)
			continue
		}
		if len(trace.Target.Entries) > 0 {
			trace.Target.Entries = trace.Target.Entries[:len(trace.Target.Entries)-1]
			markTracePartial(&trace)
			continue
		}
		return Trace{SchemaVersion: SchemaVersion, State: CapturePartial, Target: Snapshot{
			SchemaVersion: SchemaVersion,
			State:         CapturePartial,
			Entries:       []Entry{},
			Truncated:     true,
		}, Changes: []Change{}}
	}
}

func markTracePartial(trace *Trace) {
	trace.State = CapturePartial
	trace.Target.State = CapturePartial
	trace.Target.Truncated = true
}

func MarshalSnapshot(value Snapshot) ([]byte, error) { return json.Marshal(value) }
func MarshalTrace(value Trace) ([]byte, error)       { return json.Marshal(value) }

func (snapshot Snapshot) Validate() error {
	if snapshot.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema version %d", snapshot.SchemaVersion)
	}
	if !snapshot.State.valid() {
		return fmt.Errorf("unsupported capture state %q", snapshot.State)
	}
	if len(snapshot.Entries) > maxEntries {
		return fmt.Errorf("too many parameter entries")
	}
	if snapshot.State == CaptureCaptured && snapshot.Truncated {
		return fmt.Errorf("captured snapshot cannot be truncated")
	}
	if snapshot.State == CapturePartial && !snapshot.Truncated {
		return fmt.Errorf("partial snapshot must be truncated")
	}
	if snapshot.State != CaptureCaptured && snapshot.State != CapturePartial &&
		(len(snapshot.Entries) != 0 || snapshot.Truncated) {
		return fmt.Errorf("non-captured snapshot must be empty")
	}
	previous := ""
	for _, entry := range snapshot.Entries {
		if !safePath(entry.Path) || entry.Path <= previous {
			return fmt.Errorf("parameter paths must be safe, unique, and sorted")
		}
		previous = entry.Path
		if !entry.Kind.valid() || entry.Value == "" || len(entry.Value) > maxValueBytes ||
			!utf8.ValidString(entry.Value) || strings.ContainsAny(entry.Value, "\r\n\x00") {
			return fmt.Errorf("invalid parameter value for %q", entry.Path)
		}
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil || len(encoded) > MaxSnapshotBytes {
		return fmt.Errorf("parameter snapshot exceeds size limit")
	}
	return nil
}

func (trace Trace) Validate() error {
	if trace.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema version %d", trace.SchemaVersion)
	}
	if !trace.State.valid() {
		return fmt.Errorf("unsupported trace state %q", trace.State)
	}
	if err := trace.Target.Validate(); err != nil {
		return fmt.Errorf("invalid target snapshot: %w", err)
	}
	if len(trace.Changes) > maxEntries {
		return fmt.Errorf("too many parameter changes")
	}
	for _, change := range trace.Changes {
		if !change.Disposition.valid() {
			return fmt.Errorf("unsupported change disposition %q", change.Disposition)
		}
		sourceSafe := change.SourcePath == "" || safePath(change.SourcePath)
		targetSafe := change.TargetPath == "" || safePath(change.TargetPath)
		if !sourceSafe || !targetSafe {
			return fmt.Errorf("unsafe change path")
		}
		switch change.Disposition {
		case DispositionDropped:
			if change.SourcePath == "" || change.TargetPath != "" {
				return fmt.Errorf("dropped change has invalid paths")
			}
		case DispositionAdded:
			if change.SourcePath != "" || change.TargetPath == "" {
				return fmt.Errorf("added change has invalid paths")
			}
		default:
			if change.SourcePath == "" || change.TargetPath == "" {
				return fmt.Errorf("paired change has invalid paths")
			}
		}
	}
	encoded, err := json.Marshal(trace)
	if err != nil || len(encoded) > MaxTraceBytes {
		return fmt.Errorf("conversion trace exceeds size limit")
	}
	return nil
}

func (state CaptureState) valid() bool {
	switch state {
	case CaptureCaptured, CapturePartial, CaptureUnavailable, CaptureSkippedOversize, CapturePreflightBlocked:
		return true
	default:
		return false
	}
}

func (kind ValueKind) valid() bool {
	return kind == ValueBoolean || kind == ValueNumber || kind == ValueEnum || kind == ValueSummary
}

func (disposition Disposition) valid() bool {
	switch disposition {
	case DispositionPreserved, DispositionMapped, DispositionNormalized, DispositionDropped, DispositionAdded:
		return true
	default:
		return false
	}
}

func safePath(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' ||
			character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func emptySnapshot(state CaptureState) Snapshot {
	return Snapshot{SchemaVersion: SchemaVersion, State: state, Entries: []Entry{}}
}

type projectionBuilder struct {
	entries   map[string]Entry
	truncated bool
}

func newProjectionBuilder() *projectionBuilder {
	return &projectionBuilder{entries: make(map[string]Entry)}
}

func (builder *projectionBuilder) add(path string, kind ValueKind, value string) {
	if path == "" || value == "" {
		return
	}
	if len(builder.entries) >= maxEntries {
		if _, exists := builder.entries[path]; !exists {
			builder.truncated = true
			return
		}
	}
	if len(value) > maxValueBytes {
		value = truncateUTF8(value, maxValueBytes-len(truncatedMarker)) + truncatedMarker
		builder.truncated = true
	}
	builder.entries[path] = Entry{Path: path, Kind: kind, Value: value}
}

func (builder *projectionBuilder) addBool(path string, value any) {
	if typed, ok := value.(bool); ok {
		builder.add(path, ValueBoolean, strconv.FormatBool(typed))
	}
}

func (builder *projectionBuilder) addNumber(path string, value any) {
	switch typed := value.(type) {
	case json.Number:
		if _, err := typed.Float64(); err == nil {
			builder.add(path, ValueNumber, typed.String())
		}
	case float64:
		builder.add(path, ValueNumber, strconv.FormatFloat(typed, 'g', -1, 64))
	}
}

func (builder *projectionBuilder) addEnum(path string, value any) {
	if typed, ok := value.(string); ok {
		normalized := normalizeEnum(typed)
		if normalized != "" {
			builder.add(path, ValueEnum, normalized)
		}
	}
}

func (builder *projectionBuilder) projectScalars(root map[string]any) {
	builder.addBool("stream", lookup(root, "stream"))
	for _, key := range []string{"max_output_tokens", "max_completion_tokens", "max_tokens"} {
		if value, exists := root[key]; exists {
			builder.addNumber("limits.output_tokens", value)
			break
		}
	}
	for key, path := range map[string]string{
		"temperature":       "sampling.temperature",
		"top_p":             "sampling.top_p",
		"top_k":             "sampling.top_k",
		"n":                 "sampling.candidate_count",
		"candidate_count":   "sampling.candidate_count",
		"seed":              "sampling.seed",
		"presence_penalty":  "sampling.presence_penalty",
		"frequency_penalty": "sampling.frequency_penalty",
	} {
		builder.addNumber(path, lookup(root, key))
	}
	builder.addBool("tools.parallel_calls", lookup(root, "parallel_tool_calls"))
	builder.addEnum("tools.choice", lookup(root, "tool_choice"))

	if config := objectAt(root, "generationConfig", "generation_config"); config != nil {
		builder.addNumber("limits.output_tokens", lookup(config, "maxOutputTokens", "max_output_tokens"))
		builder.addNumber("sampling.temperature", lookup(config, "temperature"))
		builder.addNumber("sampling.top_p", lookup(config, "topP", "top_p"))
		builder.addNumber("sampling.top_k", lookup(config, "topK", "top_k"))
		builder.addNumber("sampling.candidate_count", lookup(config, "candidateCount", "candidate_count"))
		builder.addNumber("sampling.seed", lookup(config, "seed"))
	}
}

func (builder *projectionBuilder) projectReasoning(root map[string]any) {
	builder.addEnum("reasoning.effort", lookup(root, "reasoning_effort"))

	for _, object := range []map[string]any{
		objectAt(root, "reasoning"),
		objectAt(root, "thinking"),
		objectAt(root, "output_config", "outputConfig"),
	} {
		builder.projectReasoningObject(object)
	}
	if generationConfig := objectAt(root, "generationConfig", "generation_config"); generationConfig != nil {
		builder.projectReasoningObject(objectAt(generationConfig, "thinkingConfig", "thinking_config"))
	}
	if additional := objectAt(root, "additionalModelRequestFields", "additional_model_request_fields"); additional != nil {
		for _, object := range []map[string]any{
			objectAt(additional, "thinking"),
			objectAt(additional, "output_config", "outputConfig"),
			objectAt(additional, "reasoningConfig", "reasoning_config"),
		} {
			builder.projectReasoningObject(object)
		}
	}
}

func (builder *projectionBuilder) projectReasoningObject(object map[string]any) {
	if object == nil {
		return
	}
	builder.addEnum("reasoning.mode", lookup(object, "type", "mode"))
	builder.addEnum("reasoning.effort", lookup(
		object, "effort", "thinkingLevel", "thinking_level", "maxReasoningEffort",
	))
	builder.addNumber("reasoning.budget_tokens", lookup(
		object,
		"budget_tokens", "max_tokens", "thinkingBudget", "thinking_budget", "maxReasoningEffort",
	))
	builder.addBool("reasoning.include", lookup(object, "includeThoughts", "include_thoughts"))
}

func (builder *projectionBuilder) projectTools(root map[string]any) {
	tools, ok := lookup(root, "tools").([]any)
	if !ok || len(tools) == 0 {
		return
	}
	types := make(map[string]struct{})
	names := make(map[string]struct{})
	count := 0
	for index, value := range tools {
		if index >= maxEntries {
			builder.truncated = true
			break
		}
		tool, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if declarations, ok := lookup(tool, "functionDeclarations", "function_declarations").([]any); ok {
			for declarationIndex, declarationValue := range declarations {
				if count >= maxEntries || declarationIndex >= maxEntries {
					builder.truncated = true
					break
				}
				declaration, ok := declarationValue.(map[string]any)
				if !ok {
					continue
				}
				count++
				types["function"] = struct{}{}
				addSafeName(names, lookup(declaration, "name"))
			}
		}
		hadSpecialTool := false
		for key := range tool {
			if toolType := canonicalToolType(key); toolType != "" && toolType != "function" {
				count++
				types[toolType] = struct{}{}
				hadSpecialTool = true
			}
		}
		if _, hasDeclarations := lookup(tool, "functionDeclarations", "function_declarations").([]any); hasDeclarations {
			continue
		}
		if hadSpecialTool {
			continue
		}
		count++
		typeValue, _ := lookup(tool, "type").(string)
		toolType := canonicalToolType(typeValue)
		if toolType == "" {
			toolType = "function"
		}
		types[toolType] = struct{}{}
		if function := objectAt(tool, "function"); function != nil {
			addSafeName(names, lookup(function, "name"))
		} else {
			addSafeName(names, lookup(tool, "name"))
		}
	}
	if count == 0 {
		return
	}
	builder.add("tools.count", ValueNumber, strconv.Itoa(count))
	sortedTypes := sortedKeys(types)
	builder.add("tools.types", ValueSummary, strings.Join(sortedTypes, ","))
	for _, toolType := range sortedTypes {
		builder.add("tools.type."+toolType, ValueBoolean, "true")
	}
	if len(names) > 0 {
		builder.add("tools.names", ValueSummary, strings.Join(sortedKeys(names), ","))
	}
}

func (builder *projectionBuilder) projectStructuredOutput(root map[string]any) {
	var format map[string]any
	if value := objectAt(root, "response_format"); value != nil {
		format = value
		if nested := objectAt(value, "json_schema"); nested != nil {
			builder.addEnum("structured_output.type", lookup(value, "type"))
			format = nested
		}
	}
	if text := objectAt(root, "text"); text != nil {
		if nested := objectAt(text, "format"); nested != nil {
			format = nested
		}
	}
	if outputConfig := objectAt(root, "output_config", "outputConfig"); outputConfig != nil {
		if nested := objectAt(outputConfig, "format"); nested != nil {
			format = nested
		}
	}
	if format != nil {
		builder.addEnum("structured_output.type", lookup(format, "type"))
		if value, ok := lookup(format, "name").(string); ok {
			builder.add("structured_output.name", ValueSummary, safeIdentifier(value))
		}
		builder.addBool("structured_output.strict", lookup(format, "strict"))
		if _, exists := format["schema"]; exists {
			builder.add("structured_output.schema_present", ValueBoolean, "true")
		}
	}
	if config := objectAt(root, "generationConfig", "generation_config"); config != nil {
		mime, _ := lookup(config, "responseMimeType", "response_mime_type").(string)
		_, schema := firstExisting(config, "responseSchema", "response_schema", "responseJsonSchema", "response_json_schema")
		if schema || strings.EqualFold(mime, "application/json") {
			builder.add("structured_output.type", ValueEnum, "json_schema")
		}
		if schema {
			builder.add("structured_output.schema_present", ValueBoolean, "true")
		}
	}
}

func (builder *projectionBuilder) projectModalities(root map[string]any) {
	counts := make(map[string]int)
	visited := 0
	var walk func(any, int)
	walk = func(value any, depth int) {
		if visited >= maxEntries {
			builder.truncated = true
			return
		}
		if depth > maxTraversalDepth {
			builder.truncated = true
			return
		}
		visited++
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				walk(item, depth+1)
			}
		case map[string]any:
			if rawType, ok := lookup(typed, "type").(string); ok {
				if modality := canonicalModality(rawType); modality != "" {
					counts[modality]++
				}
			}
			for key, item := range typed {
				normalized := normalizeKey(key)
				if normalized == "inlinedata" || normalized == "filedata" {
					if object, ok := item.(map[string]any); ok {
						mime, _ := lookup(object, "mimeType", "mime_type").(string)
						if modality := mimeModality(mime); modality != "" {
							counts[modality]++
						}
					}
				}
				walk(item, depth+1)
			}
		}
	}
	walk(root, 0)
	for _, modality := range []string{"text", "image", "audio", "file", "video"} {
		if counts[modality] > 0 {
			builder.add("modalities."+modality+"_count", ValueNumber, strconv.Itoa(counts[modality]))
		}
	}
}

func (builder *projectionBuilder) snapshot() Snapshot {
	entries := make([]Entry, 0, len(builder.entries))
	for _, entry := range builder.entries {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Path < entries[right].Path })
	state := CaptureCaptured
	if builder.truncated {
		state = CapturePartial
	}
	snapshot := Snapshot{
		SchemaVersion: SchemaVersion,
		State:         state,
		Entries:       entries,
		Truncated:     builder.truncated,
	}
	for {
		encoded, err := json.Marshal(snapshot)
		if err == nil && len(encoded) <= MaxSnapshotBytes {
			return snapshot
		}
		if len(snapshot.Entries) == 0 {
			return emptySnapshot(CapturePartial)
		}
		snapshot.Entries = snapshot.Entries[:len(snapshot.Entries)-1]
		snapshot.State = CapturePartial
		snapshot.Truncated = true
	}
}

func entriesByPath(entries []Entry) map[string]Entry {
	result := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		result[entry.Path] = entry
	}
	return result
}

func sortedEntryPaths(entries map[string]Entry) []string {
	paths := make([]string, 0, len(entries))
	for path := range entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func boundTrace(trace Trace) Trace {
	return BoundTrace(trace, MaxTraceBytes)
}

func lookup(object map[string]any, names ...string) any {
	value, _ := firstExisting(object, names...)
	return value
}

func firstExisting(object map[string]any, names ...string) (any, bool) {
	for _, name := range names {
		if value, exists := object[name]; exists {
			return value, true
		}
	}
	for key, value := range object {
		for _, name := range names {
			if normalizeKey(key) == normalizeKey(name) {
				return value, true
			}
		}
	}
	return nil, false
}

func objectAt(object map[string]any, names ...string) map[string]any {
	value, _ := firstExisting(object, names...)
	result, _ := value.(map[string]any)
	return result
}

func canonicalToolType(value string) string {
	normalized := normalizeKey(value)
	switch {
	case normalized == "function", normalized == "functiondeclarations":
		return "function"
	case strings.Contains(normalized, "websearch"), normalized == "googlesearch":
		return "web_search"
	case strings.Contains(normalized, "filesearch"):
		return "file_search"
	case strings.Contains(normalized, "computer"):
		return "computer"
	case strings.Contains(normalized, "code"):
		return "code_interpreter"
	case strings.Contains(normalized, "mcp"):
		return "mcp"
	case strings.Contains(normalized, "imagegeneration"):
		return "image_generation"
	case strings.Contains(normalized, "urlcontext"):
		return "url_context"
	default:
		return ""
	}
}

func canonicalModality(value string) string {
	normalized := normalizeKey(value)
	switch {
	case normalized == "text", strings.Contains(normalized, "inputtext"), strings.Contains(normalized, "outputtext"):
		return "text"
	case strings.Contains(normalized, "image"):
		return "image"
	case strings.Contains(normalized, "audio"):
		return "audio"
	case strings.Contains(normalized, "video"):
		return "video"
	case strings.Contains(normalized, "file"), strings.Contains(normalized, "document"):
		return "file"
	default:
		return ""
	}
}

func mimeModality(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.HasPrefix(value, "image/"):
		return "image"
	case strings.HasPrefix(value, "audio/"):
		return "audio"
	case strings.HasPrefix(value, "video/"):
		return "video"
	case value != "":
		return "file"
	default:
		return ""
	}
}

func addSafeName(names map[string]struct{}, value any) {
	name, ok := value.(string)
	if !ok || name == "" {
		return
	}
	names[safeIdentifier(name)] = struct{}{}
}

func safeIdentifier(value string) string {
	if len(value) > 128 || !utf8.ValidString(value) {
		return "[redacted]"
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("_.:-", character) {
			continue
		}
		return "[redacted]"
	}
	redacted := redact.New().String(value)
	if redacted != value {
		return redact.Placeholder
	}
	return value
}

func normalizeEnum(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 || !utf8.ValidString(value) {
		return ""
	}
	if redact.New().String(value) != value {
		return ""
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("_.:-/", character) {
			continue
		}
		return ""
	}
	return strings.ToLower(value)
}

func normalizeKey(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, "-", "")
	return value
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func truncateUTF8(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) && len(value) > 0 {
		value = value[:len(value)-1]
	}
	return value
}
