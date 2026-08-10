package models

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ContextPriceTier overrides all four price slots at and above ThresholdTokens.
// A nil slot means that slot is unavailable within the selected tier.
type ContextPriceTier struct {
	ThresholdTokens                        int64  `json:"threshold_tokens"`
	InputPriceNanoUSDPerMillionTokens      *int64 `json:"input_price_nano_usd_per_million_tokens"`
	OutputPriceNanoUSDPerMillionTokens     *int64 `json:"output_price_nano_usd_per_million_tokens"`
	CacheReadPriceNanoUSDPerMillionTokens  *int64 `json:"cache_read_price_nano_usd_per_million_tokens"`
	CacheWritePriceNanoUSDPerMillionTokens *int64 `json:"cache_write_price_nano_usd_per_million_tokens"`
}

type contextPriceTierJSON struct {
	ThresholdTokens                        *int64 `json:"threshold_tokens"`
	InputPriceNanoUSDPerMillionTokens      *int64 `json:"input_price_nano_usd_per_million_tokens"`
	OutputPriceNanoUSDPerMillionTokens     *int64 `json:"output_price_nano_usd_per_million_tokens"`
	CacheReadPriceNanoUSDPerMillionTokens  *int64 `json:"cache_read_price_nano_usd_per_million_tokens"`
	CacheWritePriceNanoUSDPerMillionTokens *int64 `json:"cache_write_price_nano_usd_per_million_tokens"`
}

// ModelPrice stores one catalog or manual price for an exact channel/model pair.
type ModelPrice struct {
	ID                                     uint   `gorm:"primaryKey;autoIncrement"`
	ChannelID                              string `gorm:"type:varchar(64);not null;uniqueIndex:idx_model_prices_channel_model,priority:1"`
	ModelID                                string `gorm:"type:varchar(255);not null;uniqueIndex:idx_model_prices_channel_model,priority:2"`
	InputPriceNanoUSDPerMillionTokens      *int64 `gorm:"column:input_price_nano_usd_per_million_tokens;check:chk_model_price_input_nano,input_price_nano_usd_per_million_tokens IS NULL OR input_price_nano_usd_per_million_tokens >= 0"`
	OutputPriceNanoUSDPerMillionTokens     *int64 `gorm:"column:output_price_nano_usd_per_million_tokens;check:chk_model_price_output_nano,output_price_nano_usd_per_million_tokens IS NULL OR output_price_nano_usd_per_million_tokens >= 0"`
	CacheReadPriceNanoUSDPerMillionTokens  *int64 `gorm:"column:cache_read_price_nano_usd_per_million_tokens;check:chk_model_price_cache_read_nano,cache_read_price_nano_usd_per_million_tokens IS NULL OR cache_read_price_nano_usd_per_million_tokens >= 0"`
	CacheWritePriceNanoUSDPerMillionTokens *int64 `gorm:"column:cache_write_price_nano_usd_per_million_tokens;check:chk_model_price_cache_write_nano,cache_write_price_nano_usd_per_million_tokens IS NULL OR cache_write_price_nano_usd_per_million_tokens >= 0"`
	ContextPriceTiers                      JSON   `gorm:"type:json"`
	IsManual                               bool   `gorm:"not null;default:false"`
	CreatedAtMS                            int64  `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_model_price_created_at,created_at_ms >= 0"`
	UpdatedAtMS                            int64  `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_model_price_updated_at,updated_at_ms >= 0"`
}

// BeforeSave rejects malformed tier contracts and normalizes an empty tier list
// to SQL NULL before either an insert or update reaches SQLite.
func (price *ModelPrice) BeforeSave(_ *gorm.DB) error {
	normalized, err := NormalizeContextPriceTiers(price.ContextPriceTiers)
	if err != nil {
		return err
	}
	price.ContextPriceTiers = normalized
	return nil
}

// BeforeCreate validates explicit ON CONFLICT assignments. AssignmentColumns
// referring to excluded.context_price_tiers are safe because BeforeSave has
// already normalized the inserted row.
func (price *ModelPrice) BeforeCreate(tx *gorm.DB) error {
	return normalizeContextPriceTierOnConflict(tx)
}

// BeforeUpdate validates and normalizes the actual update destination. GORM
// keeps map, single-column, and separate struct assignments in Statement.Dest
// rather than on the hook receiver.
func (price *ModelPrice) BeforeUpdate(tx *gorm.DB) error {
	return normalizeContextPriceTierUpdate(tx)
}

// NormalizeContextPriceTiers validates the internal tier JSON contract and
// returns canonical JSON. Nil, JSON null, and an empty array become nil.
func NormalizeContextPriceTiers(raw JSON) (JSON, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var decoded []contextPriceTierJSON
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("validate context price tiers: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	if decoded == nil || len(decoded) == 0 {
		return nil, nil
	}
	tiers := make([]ContextPriceTier, len(decoded))
	for index, tier := range decoded {
		if tier.ThresholdTokens == nil {
			return nil, fmt.Errorf("validate context price tiers: tier %d threshold is missing", index)
		}
		tiers[index] = ContextPriceTier{
			ThresholdTokens:                        *tier.ThresholdTokens,
			InputPriceNanoUSDPerMillionTokens:      tier.InputPriceNanoUSDPerMillionTokens,
			OutputPriceNanoUSDPerMillionTokens:     tier.OutputPriceNanoUSDPerMillionTokens,
			CacheReadPriceNanoUSDPerMillionTokens:  tier.CacheReadPriceNanoUSDPerMillionTokens,
			CacheWritePriceNanoUSDPerMillionTokens: tier.CacheWritePriceNanoUSDPerMillionTokens,
		}
	}

	for index := range tiers {
		tier := tiers[index]
		if tier.ThresholdTokens < 0 {
			return nil, fmt.Errorf("validate context price tiers: tier %d threshold is negative", index)
		}
		if index > 0 && tier.ThresholdTokens <= tiers[index-1].ThresholdTokens {
			return nil, fmt.Errorf("validate context price tiers: thresholds must be strictly increasing")
		}
		prices := []*int64{
			tier.InputPriceNanoUSDPerMillionTokens,
			tier.OutputPriceNanoUSDPerMillionTokens,
			tier.CacheReadPriceNanoUSDPerMillionTokens,
			tier.CacheWritePriceNanoUSDPerMillionTokens,
		}
		hasPrice := false
		for _, price := range prices {
			if price == nil {
				continue
			}
			hasPrice = true
			if *price < 0 {
				return nil, fmt.Errorf("validate context price tiers: tier %d price is negative", index)
			}
		}
		if !hasPrice {
			return nil, fmt.Errorf("validate context price tiers: tier %d has no price", index)
		}
	}

	normalized, err := json.Marshal(tiers)
	if err != nil {
		return nil, fmt.Errorf("normalize context price tiers: %w", err)
	}
	return JSON(normalized), nil
}

func normalizeContextPriceTierUpdate(tx *gorm.DB) error {
	if tx == nil || tx.Statement == nil {
		return nil
	}
	switch destination := tx.Statement.Dest.(type) {
	case map[string]any:
		for _, key := range []string{"context_price_tiers", "ContextPriceTiers"} {
			value, exists := destination[key]
			if !exists {
				continue
			}
			normalized, err := normalizeContextPriceTierAssignment(value)
			if err != nil {
				return err
			}
			destination[key] = normalized
		}
	case ModelPrice:
		if len(destination.ContextPriceTiers) == 0 {
			return nil
		}
		normalized, err := NormalizeContextPriceTiers(destination.ContextPriceTiers)
		if err != nil {
			return err
		}
		tx.Statement.SetColumn("ContextPriceTiers", normalized)
		ensureContextPriceTiersSelected(tx.Statement)
	case *ModelPrice:
		if destination == nil || len(destination.ContextPriceTiers) == 0 {
			return nil
		}
		normalized, err := NormalizeContextPriceTiers(destination.ContextPriceTiers)
		if err != nil {
			return err
		}
		destination.ContextPriceTiers = normalized
		tx.Statement.SetColumn("ContextPriceTiers", normalized)
		ensureContextPriceTiersSelected(tx.Statement)
	}
	return tx.Error
}

func normalizeContextPriceTierOnConflict(tx *gorm.DB) error {
	if tx == nil || tx.Statement == nil {
		return nil
	}
	entry, exists := tx.Statement.Clauses["ON CONFLICT"]
	if !exists {
		return nil
	}
	onConflict, ok := entry.Expression.(clause.OnConflict)
	if !ok {
		return fmt.Errorf("validate context price tiers: unsupported ON CONFLICT clause")
	}
	for index := range onConflict.DoUpdates {
		assignment := &onConflict.DoUpdates[index]
		if assignment.Column.Name != "context_price_tiers" {
			continue
		}
		if column, isColumn := assignment.Value.(clause.Column); isColumn {
			if column.Table == "excluded" && column.Name == "context_price_tiers" {
				continue
			}
			return fmt.Errorf("validate context price tiers: unsupported column assignment")
		}
		normalized, err := normalizeContextPriceTierAssignment(assignment.Value)
		if err != nil {
			return err
		}
		assignment.Value = normalized
	}
	entry.Expression = onConflict
	tx.Statement.Clauses["ON CONFLICT"] = entry
	return nil
}

func normalizeContextPriceTierAssignment(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	var raw JSON
	switch typed := value.(type) {
	case JSON:
		raw = typed
	case []byte:
		raw = JSON(typed)
	case string:
		raw = JSON(typed)
	default:
		return nil, fmt.Errorf("validate context price tiers: unsupported assignment type %T", value)
	}
	normalized, err := NormalizeContextPriceTiers(raw)
	if err != nil {
		return nil, err
	}
	if normalized == nil {
		return nil, nil
	}
	return normalized, nil
}

func ensureContextPriceTiersSelected(statement *gorm.Statement) {
	for _, omitted := range statement.Omits {
		if omitted == "ContextPriceTiers" || omitted == "context_price_tiers" {
			return
		}
	}
	for _, selected := range statement.Selects {
		if selected == "*" || selected == "ContextPriceTiers" || selected == "context_price_tiers" {
			return
		}
	}
	statement.Selects = append(statement.Selects, "ContextPriceTiers")
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("validate context price tiers: trailing JSON value")
		}
		return fmt.Errorf("validate context price tiers: trailing data: %w", err)
	}
	return nil
}
