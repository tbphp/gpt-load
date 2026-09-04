// Package parameteroverride owns Group-scoped request-parameter override rules.
package parameteroverride

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const (
	maxRules       = 100
	maxConfigBytes = 256 << 10
	maxRemovePaths = 256
	maxJSONDepth   = 64
	maxApplyBytes  = 8 << 20
	maxSafeInteger = int64(1<<53 - 1)
)

var forbiddenRootFields = []string{"model", "stream", "store"}

type rule struct {
	clientProtocol protocol.Protocol
	model          string
	modelPrefix    bool
	set            map[string]any
	remove         [][]string
}

// Rules is an immutable compiled ordered rule set.
type Rules struct {
	entries []rule
}

// Compile validates and compiles one JSON-decoded parameter_overrides value.
func Compile(value any) (Rules, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return Rules{}, fmt.Errorf("encode parameter overrides: %w", err)
	}
	if len(encoded) > maxConfigBytes {
		return Rules{}, fmt.Errorf("parameter overrides exceed %d bytes", maxConfigBytes)
	}
	var rawRules []map[string]json.RawMessage
	if err := decodeJSON(encoded, &rawRules); err != nil {
		return Rules{}, fmt.Errorf("parameter overrides must be an array: %w", err)
	}
	if rawRules == nil && !bytes.Equal(bytes.TrimSpace(encoded), []byte("[]")) {
		return Rules{}, fmt.Errorf("parameter overrides must be an array")
	}
	if len(rawRules) > maxRules {
		return Rules{}, fmt.Errorf("parameter overrides exceed %d rules", maxRules)
	}

	compiled := Rules{entries: make([]rule, 0, len(rawRules))}
	totalRemovePaths := 0
	for index, raw := range rawRules {
		entry, removeCount, err := compileRule(raw)
		if err != nil {
			return Rules{}, fmt.Errorf("parameter override rule %d: %w", index+1, err)
		}
		totalRemovePaths += removeCount
		if totalRemovePaths > maxRemovePaths {
			return Rules{}, fmt.Errorf("parameter overrides exceed %d remove paths", maxRemovePaths)
		}
		compiled.entries = append(compiled.entries, entry)
	}
	return compiled, nil
}

func compileRule(raw map[string]json.RawMessage) (rule, int, error) {
	for key := range raw {
		switch key {
		case "match", "set", "remove":
		default:
			return rule{}, 0, fmt.Errorf("unknown field %q", key)
		}
	}
	entry := rule{}
	if rawMatch, exists := raw["match"]; exists {
		match, err := compileMatch(rawMatch)
		if err != nil {
			return rule{}, 0, err
		}
		entry.clientProtocol = match.clientProtocol
		entry.model = match.model
		entry.modelPrefix = match.modelPrefix
	}
	if rawSet, exists := raw["set"]; exists {
		trimmed := bytes.TrimSpace(rawSet)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return rule{}, 0, fmt.Errorf("set must be a JSON object")
		}
		if err := decodeJSON(rawSet, &entry.set); err != nil || entry.set == nil {
			return rule{}, 0, fmt.Errorf("set must be a JSON object")
		}
		if err := validateSetValue(entry.set, 1); err != nil {
			return rule{}, 0, err
		}
		for key := range entry.set {
			if forbiddenRootField(key) {
				return rule{}, 0, fmt.Errorf("set cannot change root field %q", key)
			}
		}
	}
	var removePaths []string
	if rawRemove, exists := raw["remove"]; exists {
		trimmed := bytes.TrimSpace(rawRemove)
		if len(trimmed) == 0 || trimmed[0] != '[' || decodeJSON(rawRemove, &removePaths) != nil || removePaths == nil {
			return rule{}, 0, fmt.Errorf("remove must be an array of JSON Pointer paths")
		}
	}
	seenRemovePaths := make(map[string]struct{}, len(removePaths))
	for _, path := range removePaths {
		if _, duplicate := seenRemovePaths[path]; duplicate {
			return rule{}, 0, fmt.Errorf("remove path %q is duplicated", path)
		}
		seenRemovePaths[path] = struct{}{}
		segments, err := parsePointer(path)
		if err != nil {
			return rule{}, 0, fmt.Errorf("remove path %q: %w", path, err)
		}
		if forbiddenRootField(segments[0]) {
			return rule{}, 0, fmt.Errorf("remove cannot change root field %q", segments[0])
		}
		entry.remove = append(entry.remove, segments)
	}
	if len(entry.set) == 0 && len(entry.remove) == 0 {
		return rule{}, 0, fmt.Errorf("at least one set field or remove path is required")
	}
	return entry, len(removePaths), nil
}

type compiledMatch struct {
	clientProtocol protocol.Protocol
	model          string
	modelPrefix    bool
}

func compileMatch(raw json.RawMessage) (compiledMatch, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return compiledMatch{}, fmt.Errorf("match must be a JSON object")
	}
	var values map[string]json.RawMessage
	if err := decodeJSON(raw, &values); err != nil || values == nil {
		return compiledMatch{}, fmt.Errorf("match must be a JSON object")
	}
	for key := range values {
		if key != "protocol" && key != "model" {
			return compiledMatch{}, fmt.Errorf("match has unknown field %q", key)
		}
	}
	result := compiledMatch{}
	if rawProtocol, exists := values["protocol"]; exists {
		var value string
		if err := decodeJSON(rawProtocol, &value); err != nil {
			return compiledMatch{}, fmt.Errorf("match.protocol must be a string")
		}
		if value == "" || strings.TrimSpace(value) != value {
			return compiledMatch{}, fmt.Errorf("match.protocol must be omitted or contain a canonical protocol")
		}
		result.clientProtocol = protocol.Protocol(value)
		if !result.clientProtocol.Valid() {
			return compiledMatch{}, fmt.Errorf("match.protocol %q is invalid", value)
		}
	}
	if rawModel, exists := values["model"]; exists {
		var value string
		if err := decodeJSON(rawModel, &value); err != nil {
			return compiledMatch{}, fmt.Errorf("match.model must be a string")
		}
		if value == "" || strings.TrimSpace(value) != value {
			return compiledMatch{}, fmt.Errorf("match.model must be omitted or contain a non-empty model")
		}
		wildcards := strings.Count(value, "*")
		switch {
		case wildcards == 0:
			result.model = value
		case wildcards == 1 && strings.HasSuffix(value, "*") && len(value) > 1:
			result.model = strings.TrimSuffix(value, "*")
			result.modelPrefix = true
		default:
			return compiledMatch{}, fmt.Errorf("match.model supports only one trailing prefix wildcard")
		}
	}
	return result, nil
}

// Empty reports whether no rule can be applied.
func (rules Rules) Empty() bool { return len(rules.entries) == 0 }

// Matches reports whether at least one rule matches a supported request identity.
func (rules Rules) Matches(
	clientProtocol protocol.Protocol,
	operation execution.Operation,
	clientModel string,
) bool {
	if rules.Empty() || !supports(clientProtocol, operation) {
		return false
	}
	for _, entry := range rules.entries {
		if entry.matches(clientProtocol, clientModel) {
			return true
		}
	}
	return false
}

// Clone returns an independently owned rule set.
func (rules Rules) Clone() Rules {
	cloned := Rules{entries: make([]rule, len(rules.entries))}
	for index, entry := range rules.entries {
		cloned.entries[index] = rule{
			clientProtocol: entry.clientProtocol,
			model:          entry.model,
			modelPrefix:    entry.modelPrefix,
			set:            cloneObject(entry.set),
			remove:         clonePointers(entry.remove),
		}
	}
	return cloned
}

// Apply applies all matching rules to one supported client request body.
func (rules Rules) Apply(
	clientProtocol protocol.Protocol,
	operation execution.Operation,
	clientModel string,
	body []byte,
) ([]byte, bool, error) {
	if rules.Empty() || !supports(clientProtocol, operation) {
		return body, false, nil
	}
	matched := make([]rule, 0, len(rules.entries))
	for _, entry := range rules.entries {
		if entry.matches(clientProtocol, clientModel) {
			matched = append(matched, entry)
		}
	}
	if len(matched) == 0 {
		return body, false, nil
	}
	if len(body) > maxApplyBytes {
		return nil, false, fmt.Errorf("request body exceeds parameter override limit of %d bytes", maxApplyBytes)
	}
	var object map[string]any
	if err := decodeJSON(body, &object); err != nil || object == nil {
		return nil, false, fmt.Errorf("request body must be a JSON object")
	}
	for _, entry := range matched {
		for _, pointer := range entry.remove {
			removeObjectField(object, pointer)
		}
		mergeObject(object, entry.set)
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, false, fmt.Errorf("encode overridden request body: %w", err)
	}
	return encoded, true, nil
}

func supports(clientProtocol protocol.Protocol, operation execution.Operation) bool {
	switch clientProtocol {
	case protocol.OpenAICompletions, protocol.Anthropic, protocol.Gemini:
		return operation == execution.OperationChatCompletion
	case protocol.OpenAIResponses:
		return operation == execution.OperationResponsesCreate
	case protocol.OpenAIImages:
		return operation == execution.OperationImagesGenerate
	case protocol.OpenAIEmbeddings:
		return operation == execution.OperationEmbeddingsCreate
	default:
		return false
	}
}

func (entry rule) matches(clientProtocol protocol.Protocol, clientModel string) bool {
	if entry.clientProtocol != "" && entry.clientProtocol != clientProtocol {
		return false
	}
	if entry.model == "" {
		return true
	}
	if entry.modelPrefix {
		return strings.HasPrefix(clientModel, entry.model)
	}
	return clientModel == entry.model
}

func mergeObject(target, source map[string]any) {
	for key, value := range source {
		sourceObject, sourceIsObject := value.(map[string]any)
		targetObject, targetIsObject := target[key].(map[string]any)
		if sourceIsObject && targetIsObject {
			mergeObject(targetObject, sourceObject)
			continue
		}
		target[key] = cloneJSON(value)
	}
}

func removeObjectField(target map[string]any, pointer []string) {
	current := target
	for _, segment := range pointer[:len(pointer)-1] {
		nested, ok := current[segment].(map[string]any)
		if !ok {
			return
		}
		current = nested
	}
	delete(current, pointer[len(pointer)-1])
}

func parsePointer(value string) ([]string, error) {
	if value == "" || value[0] != '/' {
		return nil, fmt.Errorf("must start with / and cannot target the document root")
	}
	if strings.Count(value, "/") > maxJSONDepth {
		return nil, fmt.Errorf("exceeds maximum path depth %d", maxJSONDepth)
	}
	rawSegments := strings.Split(value[1:], "/")
	segments := make([]string, len(rawSegments))
	for index, raw := range rawSegments {
		decoded, err := decodePointerSegment(raw)
		if err != nil {
			return nil, err
		}
		if decoded == "" {
			return nil, fmt.Errorf("contains an empty path segment")
		}
		segments[index] = decoded
		if decoded == "-" {
			return nil, fmt.Errorf("array element paths are not supported")
		}
		if _, err := strconv.ParseUint(decoded, 10, 64); err == nil {
			return nil, fmt.Errorf("array element paths are not supported")
		}
	}
	return segments, nil
}

func decodePointerSegment(value string) (string, error) {
	var result strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '~' {
			result.WriteByte(value[index])
			continue
		}
		if index+1 >= len(value) {
			return "", fmt.Errorf("contains an invalid ~ escape")
		}
		index++
		switch value[index] {
		case '0':
			result.WriteByte('~')
		case '1':
			result.WriteByte('/')
		default:
			return "", fmt.Errorf("contains an invalid ~ escape")
		}
	}
	return result.String(), nil
}

func forbiddenRootField(value string) bool {
	for _, field := range forbiddenRootFields {
		if strings.EqualFold(value, field) {
			return true
		}
	}
	return false
}

func validateSetValue(value any, depth int) error {
	if depth > maxJSONDepth {
		return fmt.Errorf("set exceeds maximum JSON depth %d", maxJSONDepth)
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if key == "" {
				return fmt.Errorf("set contains an empty object field name")
			}
			if err := validateSetValue(nested, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, nested := range typed {
			if err := validateSetValue(nested, depth+1); err != nil {
				return err
			}
		}
	case json.Number:
		if err := validateJSONNumber(typed); err != nil {
			return err
		}
	}
	return nil
}

func validateJSONNumber(value json.Number) error {
	literal := value.String()
	parsed, err := strconv.ParseFloat(literal, 64)
	if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
		return fmt.Errorf("set contains a number outside the supported range")
	}
	if parsed == 0 && math.Signbit(parsed) {
		return fmt.Errorf("set contains a number that cannot round-trip through the management UI")
	}
	exact, ok := new(big.Rat).SetString(literal)
	if !ok {
		return fmt.Errorf("set contains an invalid number")
	}
	if exact.IsInt() {
		absolute := new(big.Int).Abs(exact.Num())
		if absolute.Cmp(big.NewInt(maxSafeInteger)) > 0 {
			return fmt.Errorf("set contains an integer outside the JSON safe range")
		}
	}
	roundTrip, ok := new(big.Rat).SetString(strconv.FormatFloat(parsed, 'g', -1, 64))
	if !ok || exact.Cmp(roundTrip) != 0 {
		return fmt.Errorf("set contains a number that cannot round-trip through the management UI")
	}
	return nil
}

func clonePointers(source [][]string) [][]string {
	cloned := make([][]string, len(source))
	for index, pointer := range source {
		cloned[index] = append([]string(nil), pointer...)
	}
	return cloned
}

func cloneObject(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = cloneJSON(value)
	}
	return cloned
}

func cloneJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneObject(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index, nested := range typed {
			cloned[index] = cloneJSON(nested)
		}
		return cloned
	default:
		return value
	}
}

func decodeJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}
