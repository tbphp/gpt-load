package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"unicode"

	"gpt-load/internal/pricing"
)

const maxModelIDBytes = 255

var (
	providerCanonicalFields = []string{"id", "name", "api", "npm", "models"}
	modelCanonicalFields    = []string{
		"id", "name", "description", "family", "attachment", "reasoning",
		"tool_call", "structured_output", "temperature", "knowledge",
		"release_date", "last_updated", "modalities", "open_weights", "limit",
		"status", "cost",
	}
	modalityCanonicalFields = []string{"input", "output"}
	limitCanonicalFields    = []string{"context", "input", "output"}
	costCanonicalFields     = []string{"input", "output", "cache_read", "cache_write", "tiers"}
	tierCanonicalFields     = []string{"tier", "input", "output", "cache_read", "cache_write"}
	tierRuleCanonicalFields = []string{"type", "size"}
)

// Parse decodes, validates, and normalizes the retained Models.dev subset.
func Parse(reader io.Reader) (*Snapshot, error) {
	if reader == nil {
		return nil, fmt.Errorf("catalog reader is required")
	}
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	providerValues, err := decodeUniqueObject(decoder, "provider")
	if err != nil {
		return nil, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}

	snapshot := &Snapshot{Providers: make(map[string]Provider, len(providerValues))}
	for providerKey, rawProvider := range providerValues {
		provider, err := parseProvider(providerKey, rawProvider)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", providerKey, err)
		}
		snapshot.Providers[providerKey] = provider
	}
	if err := validateSnapshot(snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func parseProvider(key string, raw json.RawMessage) (Provider, error) {
	fields, err := decodeCanonicalObject(raw, "provider", providerCanonicalFields)
	if err != nil {
		return Provider{}, err
	}
	id, err := decodeStringField(fields, "id")
	if err != nil {
		return Provider{}, err
	}
	if id != key {
		return Provider{}, fmt.Errorf("map key and provider id must match")
	}
	if _, err := pricing.ProviderScopeKey(id); err != nil {
		return Provider{}, err
	}
	nameValue, err := decodeStringField(fields, "name")
	if err != nil {
		return Provider{}, err
	}
	name := strings.TrimSpace(nameValue)
	if name == "" {
		return Provider{}, fmt.Errorf("provider name is required")
	}
	modelsRaw, hasModels := fields["models"]
	if !hasModels || bytes.Equal(bytes.TrimSpace(modelsRaw), []byte("null")) {
		return Provider{}, fmt.Errorf("provider models object is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(modelsRaw))
	decoder.UseNumber()
	modelValues, err := decodeUniqueObject(decoder, "model")
	if err != nil {
		return Provider{}, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Provider{}, err
	}

	apiURL, err := decodeStringField(fields, "api")
	if err != nil {
		return Provider{}, err
	}
	npm, err := decodeStringField(fields, "npm")
	if err != nil {
		return Provider{}, err
	}
	provider := Provider{
		ID:     id,
		Name:   name,
		APIURL: strings.TrimSpace(apiURL),
		NPM:    strings.TrimSpace(npm),
		Models: make(map[string]Model, len(modelValues)),
	}
	for modelKey, rawModel := range modelValues {
		model, err := parseModel(modelKey, rawModel)
		if err != nil {
			return Provider{}, fmt.Errorf("model %q: %w", modelKey, err)
		}
		provider.Models[modelKey] = model
	}
	return provider, nil
}

func parseModel(key string, raw json.RawMessage) (Model, error) {
	fields, err := decodeCanonicalObject(raw, "model", modelCanonicalFields)
	if err != nil {
		return Model{}, err
	}
	id, err := decodeStringField(fields, "id")
	if err != nil {
		return Model{}, err
	}
	if id != key {
		return Model{}, fmt.Errorf("map key and model id must match")
	}
	if err := validateModelID(id); err != nil {
		return Model{}, err
	}
	nameValue, err := decodeStringField(fields, "name")
	if err != nil {
		return Model{}, err
	}
	name := strings.TrimSpace(nameValue)
	if name == "" {
		return Model{}, fmt.Errorf("model name is required")
	}
	metadata, err := parseModelMetadata(fields)
	if err != nil {
		return Model{}, err
	}
	model := Model{ID: id, Name: name, Metadata: metadata}
	costRaw, hasCost := fields["cost"]
	if !hasCost || bytes.Equal(bytes.TrimSpace(costRaw), []byte("null")) {
		return model, nil
	}
	cost, err := parseCost(costRaw)
	if err != nil {
		return Model{}, err
	}
	model.Cost = cost
	return model, nil
}

func parseModelMetadata(fields map[string]json.RawMessage) (ModelMetadata, error) {
	stringFields := [...]struct {
		name string
		set  func(*ModelMetadata, string)
	}{
		{name: "description", set: func(metadata *ModelMetadata, value string) { metadata.Description = value }},
		{name: "family", set: func(metadata *ModelMetadata, value string) { metadata.Family = value }},
		{name: "knowledge", set: func(metadata *ModelMetadata, value string) { metadata.Knowledge = value }},
		{name: "release_date", set: func(metadata *ModelMetadata, value string) { metadata.ReleaseDate = value }},
		{name: "last_updated", set: func(metadata *ModelMetadata, value string) { metadata.LastUpdated = value }},
		{name: "status", set: func(metadata *ModelMetadata, value string) { metadata.Status = value }},
	}
	var metadata ModelMetadata
	for _, field := range stringFields {
		value, err := decodeStringField(fields, field.name)
		if err != nil {
			return ModelMetadata{}, err
		}
		field.set(&metadata, strings.TrimSpace(value))
	}

	boolFields := [...]struct {
		name string
		set  func(*ModelMetadata, *bool)
	}{
		{name: "attachment", set: func(metadata *ModelMetadata, value *bool) { metadata.Capabilities.Attachment = value }},
		{name: "reasoning", set: func(metadata *ModelMetadata, value *bool) { metadata.Capabilities.Reasoning = value }},
		{name: "tool_call", set: func(metadata *ModelMetadata, value *bool) { metadata.Capabilities.ToolCall = value }},
		{name: "structured_output", set: func(metadata *ModelMetadata, value *bool) { metadata.Capabilities.StructuredOutput = value }},
		{name: "temperature", set: func(metadata *ModelMetadata, value *bool) { metadata.Capabilities.Temperature = value }},
		{name: "open_weights", set: func(metadata *ModelMetadata, value *bool) { metadata.OpenWeights = value }},
	}
	for _, field := range boolFields {
		value, err := decodeBoolField(fields, field.name)
		if err != nil {
			return ModelMetadata{}, err
		}
		field.set(&metadata, value)
	}

	modalities, err := parseModelModalities(fields["modalities"])
	if err != nil {
		return ModelMetadata{}, err
	}
	limits, err := parseModelLimits(fields["limit"])
	if err != nil {
		return ModelMetadata{}, err
	}
	metadata.Modalities = modalities
	metadata.Limits = limits
	return metadata, nil
}

func parseModelModalities(raw json.RawMessage) (ModelModalities, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ModelModalities{}, nil
	}
	fields, err := decodeCanonicalObject(raw, "model modalities", modalityCanonicalFields)
	if err != nil {
		return ModelModalities{}, err
	}
	input, err := decodeStringSliceField(fields, "input")
	if err != nil {
		return ModelModalities{}, err
	}
	output, err := decodeStringSliceField(fields, "output")
	if err != nil {
		return ModelModalities{}, err
	}
	return ModelModalities{Input: input, Output: output}, nil
}

func parseModelLimits(raw json.RawMessage) (ModelLimits, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ModelLimits{}, nil
	}
	fields, err := decodeCanonicalObject(raw, "model limits", limitCanonicalFields)
	if err != nil {
		return ModelLimits{}, err
	}
	contextLimit, err := decodeNonNegativeInt64Field(fields, "context")
	if err != nil {
		return ModelLimits{}, err
	}
	inputLimit, err := decodeNonNegativeInt64Field(fields, "input")
	if err != nil {
		return ModelLimits{}, err
	}
	outputLimit, err := decodeNonNegativeInt64Field(fields, "output")
	if err != nil {
		return ModelLimits{}, err
	}
	return ModelLimits{Context: contextLimit, Input: inputLimit, Output: outputLimit}, nil
}

func parseCost(raw json.RawMessage) (*ModelCost, error) {
	fields, err := decodeCanonicalObject(raw, "cost", costCanonicalFields)
	if err != nil {
		return nil, err
	}
	input, output, cacheRead, cacheWrite, err := decodePriceFields(fields)
	if err != nil {
		return nil, err
	}
	prices, err := parsePrices(input, output, cacheRead, cacheWrite)
	if err != nil {
		return nil, err
	}

	var rawTiers []json.RawMessage
	if raw, exists := fields["tiers"]; exists && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := decodeOneJSON(raw, &rawTiers); err != nil {
			return nil, fmt.Errorf("decode cost tiers: %w", err)
		}
	}
	tiers := make([]pricing.ContextTier, 0, len(rawTiers))
	previousThreshold := int64(-1)
	for index, rawTier := range rawTiers {
		threshold, tierPrices, err := parseContextTier(rawTier)
		if err != nil {
			return nil, fmt.Errorf("tier %d: %w", index, err)
		}
		if threshold <= previousThreshold {
			return nil, fmt.Errorf("context tier thresholds must be strictly increasing")
		}
		if !hasSetPrice(tierPrices) {
			return nil, fmt.Errorf("tier %d must set at least one supported price", index)
		}
		tiers = append(tiers, pricing.ContextTier{InputThresholdTokens: threshold, Prices: tierPrices})
		previousThreshold = threshold
	}
	if !hasSetPrice(prices) && len(tiers) == 0 {
		return nil, nil
	}
	return &ModelCost{Prices: prices, ContextTiers: tiers}, nil
}

func parseContextTier(raw json.RawMessage) (int64, pricing.Prices, error) {
	fields, err := decodeCanonicalObject(raw, "context tier", tierCanonicalFields)
	if err != nil {
		return 0, pricing.Prices{}, err
	}
	tierRaw, exists := fields["tier"]
	if !exists || bytes.Equal(bytes.TrimSpace(tierRaw), []byte("null")) {
		return 0, pricing.Prices{}, fmt.Errorf("tier rule is required")
	}
	ruleFields, err := decodeCanonicalObject(tierRaw, "context tier rule", tierRuleCanonicalFields)
	if err != nil {
		return 0, pricing.Prices{}, err
	}
	tierType, err := decodeStringField(ruleFields, "type")
	if err != nil {
		return 0, pricing.Prices{}, err
	}
	if tierType != "context" {
		return 0, pricing.Prices{}, fmt.Errorf("unsupported type %q", tierType)
	}
	size, err := decodeNumberField(ruleFields, "size")
	if err != nil {
		return 0, pricing.Prices{}, err
	}
	if size == nil {
		return 0, pricing.Prices{}, fmt.Errorf("context size is required")
	}
	threshold, err := strconv.ParseInt(size.String(), 10, 64)
	if err != nil || threshold < 0 {
		return 0, pricing.Prices{}, fmt.Errorf("context size must be a non-negative integer")
	}
	input, output, cacheRead, cacheWrite, err := decodePriceFields(fields)
	if err != nil {
		return 0, pricing.Prices{}, err
	}
	prices, err := parsePrices(input, output, cacheRead, cacheWrite)
	if err != nil {
		return 0, pricing.Prices{}, err
	}
	return threshold, prices, nil
}

func decodePriceFields(fields map[string]json.RawMessage) (*json.Number, *json.Number, *json.Number, *json.Number, error) {
	input, err := decodeNumberField(fields, "input")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	output, err := decodeNumberField(fields, "output")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	cacheRead, err := decodeNumberField(fields, "cache_read")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	cacheWrite, err := decodeNumberField(fields, "cache_write")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return input, output, cacheRead, cacheWrite, nil
}

func parsePrices(input, output, cacheRead, cacheWrite *json.Number) (pricing.Prices, error) {
	values := [...]struct {
		name   string
		number *json.Number
		set    func(*pricing.Prices, pricing.Price)
	}{
		{name: "input", number: input, set: func(prices *pricing.Prices, price pricing.Price) { prices.Input = price }},
		{name: "output", number: output, set: func(prices *pricing.Prices, price pricing.Price) { prices.Output = price }},
		{name: "cache_read", number: cacheRead, set: func(prices *pricing.Prices, price pricing.Price) { prices.CacheRead = price }},
		{name: "cache_write", number: cacheWrite, set: func(prices *pricing.Prices, price pricing.Price) { prices.CacheWrite = price }},
	}
	var prices pricing.Prices
	for _, value := range values {
		if value.number == nil {
			continue
		}
		amount, err := parseJSONUSD(value.number.String())
		if err != nil {
			return pricing.Prices{}, fmt.Errorf("%s price: %w", value.name, err)
		}
		value.set(&prices, pricing.Price{NanoUSDPerMillion: amount, Set: true})
	}
	return prices, nil
}

// parseJSONUSD expands JSON exponent notation with string arithmetic before
// handing the plain fixed-point decimal to pricing.ParseUSD. No binary float is
// created. Long fixed-point values emitted by Models.dev are rounded to the
// nearest nano-USD only when they remain above zero after normalization.
func parseJSONUSD(value string) (pricing.NanoUSD, error) {
	if strings.HasPrefix(value, "-") {
		return 0, fmt.Errorf("USD price must be non-negative")
	}
	if normalized, ok := parseModelsDevFloatArtifact(value); ok {
		return normalized, nil
	}
	mantissa, exponentText, hasExponent := strings.Cut(value, "e")
	if !hasExponent {
		mantissa, exponentText, hasExponent = strings.Cut(value, "E")
	}
	if !hasExponent {
		return pricing.ParseUSD(value)
	}
	exponent, err := strconv.ParseInt(exponentText, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid USD exponent")
	}
	integer, fraction, hasFraction := strings.Cut(mantissa, ".")
	if integer == "" || (hasFraction && fraction == "") {
		return 0, fmt.Errorf("invalid USD decimal")
	}
	digits := integer + fraction
	leadingZeros := len(digits) - len(strings.TrimLeft(digits, "0"))
	if leadingZeros == len(digits) {
		return pricing.ParseUSD("0")
	}
	digits = digits[leadingZeros:]
	decimalPosition := int64(len(integer)-leadingZeros) + exponent
	digits = strings.TrimRight(digits, "0")
	if digits == "" {
		return pricing.ParseUSD("0")
	}

	var plain string
	switch {
	case decimalPosition <= 0:
		fractionalDigits := -decimalPosition + int64(len(digits))
		if fractionalDigits > 9 {
			return 0, fmt.Errorf("USD price has more than nine decimal places")
		}
		plain = "0." + strings.Repeat("0", int(-decimalPosition)) + digits
	case decimalPosition >= int64(len(digits)):
		integerDigits := decimalPosition
		if integerDigits > 32 {
			return 0, fmt.Errorf("USD price exceeds int64 nano USD")
		}
		plain = digits + strings.Repeat("0", int(decimalPosition)-len(digits))
	default:
		fractionalDigits := int64(len(digits)) - decimalPosition
		if fractionalDigits > 9 {
			return 0, fmt.Errorf("USD price has more than nine decimal places")
		}
		plain = digits[:decimalPosition] + "." + digits[decimalPosition:]
	}
	return pricing.ParseUSD(plain)
}

const modelsDevArtifactMinFractionDigits = 15

func parseModelsDevFloatArtifact(value string) (pricing.NanoUSD, bool) {
	_, fraction, hasFraction := strings.Cut(value, ".")
	if !hasFraction || strings.ContainsAny(fraction, "eE") ||
		len(fraction) < modelsDevArtifactMinFractionDigits {
		return 0, false
	}

	exact, ok := new(big.Rat).SetString(value)
	if !ok || exact.Sign() < 0 {
		return 0, false
	}
	scaled := new(big.Rat).Mul(exact, big.NewRat(1_000_000_000, 1))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(scaled.Num(), scaled.Denom(), remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(scaled.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if quotient.Sign() == 0 && exact.Sign() != 0 || !quotient.IsInt64() {
		return 0, false
	}
	return pricing.NanoUSD(quotient.Int64()), true
}

func decodeUniqueObject(decoder *json.Decoder, kind string) (map[string]json.RawMessage, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode %s object: %w", kind, err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return nil, fmt.Errorf("%s catalog value must be an object", kind)
	}
	values := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("decode %s key: %w", kind, err)
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("%s object key must be a string", kind)
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("duplicate %s key %q", kind, key)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, fmt.Errorf("decode %s %q: %w", kind, key, err)
		}
		values[key] = append(json.RawMessage(nil), raw...)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("close %s object: %w", kind, err)
	}
	return values, nil
}

func decodeCanonicalObject(raw []byte, kind string, canonicalFields []string) (map[string]json.RawMessage, error) {
	if err := rejectDuplicateObjectFields(raw); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	fields, err := decodeUniqueObject(decoder, kind)
	if err != nil {
		return nil, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	for field := range fields {
		for _, canonical := range canonicalFields {
			if field != canonical && strings.EqualFold(field, canonical) {
				return nil, fmt.Errorf("%s field %q is a non-canonical alias of %q", kind, field, canonical)
			}
		}
	}
	return fields, nil
}

func decodeStringField(fields map[string]json.RawMessage, name string) (string, error) {
	raw, exists := fields[name]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	var value string
	if err := decodeOneJSON(raw, &value); err != nil {
		return "", fmt.Errorf("decode %s field: %w", name, err)
	}
	return value, nil
}

func decodeBoolField(fields map[string]json.RawMessage, name string) (*bool, error) {
	raw, exists := fields[name]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var value bool
	if err := decodeOneJSON(raw, &value); err != nil {
		return nil, fmt.Errorf("decode %s field: %w", name, err)
	}
	return &value, nil
}

func decodeStringSliceField(fields map[string]json.RawMessage, name string) ([]string, error) {
	raw, exists := fields[name]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var values []string
	if err := decodeOneJSON(raw, &values); err != nil {
		return nil, fmt.Errorf("decode %s field: %w", name, err)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		normalized := strings.TrimSpace(value)
		if normalized == "" {
			return nil, fmt.Errorf("%s modality must not be empty", name)
		}
		if _, duplicate := seen[normalized]; duplicate {
			return nil, fmt.Errorf("duplicate %s modality %q", name, normalized)
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result, nil
}

func decodeNonNegativeInt64Field(fields map[string]json.RawMessage, name string) (*int64, error) {
	value, err := decodeNumberField(fields, name)
	if err != nil || value == nil {
		return nil, err
	}
	parsed, err := strconv.ParseInt(value.String(), 10, 64)
	if err != nil || parsed < 0 {
		return nil, fmt.Errorf("%s limit must be a non-negative integer", name)
	}
	return &parsed, nil
}

func decodeNumberField(fields map[string]json.RawMessage, name string) (*json.Number, error) {
	raw, exists := fields[name]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var decoded any
	if err := decodeOneJSON(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode %s field: %w", name, err)
	}
	value, ok := decoded.(json.Number)
	if !ok {
		return nil, fmt.Errorf("decode %s field: JSON number is required", name)
	}
	return &value, nil
}

func decodeOneJSON(raw []byte, destination any) error {
	if err := rejectDuplicateObjectFields(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func rejectDuplicateObjectFields(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := scanUniqueJSONValue(decoder); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func scanUniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key must be a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := scanUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("trailing JSON value is not allowed")
		}
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return nil
}

func validateSnapshot(snapshot *Snapshot) error {
	if snapshot == nil {
		return fmt.Errorf("catalog snapshot is required")
	}
	for providerKey, provider := range snapshot.Providers {
		if providerKey != provider.ID {
			return fmt.Errorf("provider %q map key and id must match", providerKey)
		}
		if _, err := pricing.ProviderScopeKey(provider.ID); err != nil {
			return fmt.Errorf("provider %q: %w", providerKey, err)
		}
		if strings.TrimSpace(provider.Name) == "" {
			return fmt.Errorf("provider %q name is required", providerKey)
		}
		if provider.Models == nil {
			return fmt.Errorf("provider %q models map is required", providerKey)
		}
		for modelKey, model := range provider.Models {
			if modelKey != model.ID {
				return fmt.Errorf("provider %q model %q map key and id must match", providerKey, modelKey)
			}
			if err := validateModelID(model.ID); err != nil {
				return fmt.Errorf("provider %q model %q: %w", providerKey, modelKey, err)
			}
			if strings.TrimSpace(model.Name) == "" {
				return fmt.Errorf("provider %q model %q name is required", providerKey, modelKey)
			}
			if err := validateModelCost(model.Cost); err != nil {
				return fmt.Errorf("provider %q model %q: %w", providerKey, modelKey, err)
			}
		}
	}
	return nil
}

func validateModelID(id string) error {
	if len(id) == 0 || len(id) > maxModelIDBytes {
		return fmt.Errorf("model id must be 1 through %d bytes", maxModelIDBytes)
	}
	if strings.TrimSpace(id) != id {
		return fmt.Errorf("model id must not have surrounding whitespace")
	}
	for _, character := range id {
		if unicode.IsControl(character) {
			return fmt.Errorf("model id must not contain control characters")
		}
	}
	return nil
}

func validateModelCost(cost *ModelCost) error {
	if cost == nil {
		return nil
	}
	for _, price := range [...]pricing.Price{cost.Prices.Input, cost.Prices.Output, cost.Prices.CacheRead, cost.Prices.CacheWrite} {
		if price.NanoUSDPerMillion < 0 {
			return fmt.Errorf("price must be non-negative")
		}
	}
	previousThreshold := int64(-1)
	for _, tier := range cost.ContextTiers {
		if tier.InputThresholdTokens < 0 || tier.InputThresholdTokens <= previousThreshold {
			return fmt.Errorf("context tier thresholds must be non-negative and strictly increasing")
		}
		if !hasSetPrice(tier.Prices) {
			return fmt.Errorf("context tier must set at least one price")
		}
		for _, price := range [...]pricing.Price{tier.Prices.Input, tier.Prices.Output, tier.Prices.CacheRead, tier.Prices.CacheWrite} {
			if price.NanoUSDPerMillion < 0 {
				return fmt.Errorf("context tier price must be non-negative")
			}
		}
		previousThreshold = tier.InputThresholdTokens
	}
	return nil
}

func hasSetPrice(prices pricing.Prices) bool {
	return prices.Input.Set || prices.Output.Set || prices.CacheRead.Set || prices.CacheWrite.Set
}
