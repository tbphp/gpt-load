package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/pricing"
)

type modelPriceValuesResponse struct {
	InputPrice        *string `json:"input_price_usd_per_million_tokens"`
	OutputPrice       *string `json:"output_price_usd_per_million_tokens"`
	CacheReadPrice    *string `json:"cache_read_price_usd_per_million_tokens"`
	CacheWrite5MPrice *string `json:"cache_write_5m_price_usd_per_million_tokens"`
	CacheWrite1HPrice *string `json:"cache_write_1h_price_usd_per_million_tokens"`
}

type modelPriceValuesInput struct {
	UncachedInput *pricing.NanoUSD
	CacheRead     *pricing.NanoUSD
	CacheWrite5M  *pricing.NanoUSD
	CacheWrite1H  *pricing.NanoUSD
	Output        *pricing.NanoUSD
}

type modelPricePolicyResponse struct {
	InputThresholdTokens int64   `json:"input_threshold_tokens"`
	InputMultiplier      float64 `json:"input_multiplier"`
	OutputMultiplier     float64 `json:"output_multiplier"`
}

type modelPriceRuleResponse struct {
	Pattern       string                    `json:"pattern"`
	Source        pricing.Source            `json:"source"`
	Prices        modelPriceValuesResponse  `json:"prices"`
	SourceURL     *string                   `json:"source_url"`
	UpdatedAtMS   int64                     `json:"updated_at_ms"`
	PricingPolicy *modelPricePolicyResponse `json:"pricing_policy"`
}

type modelPriceListResponse struct {
	PriceUnit string                   `json:"price_unit"`
	Builtin   []modelPriceRuleResponse `json:"builtin"`
	Overrides []modelPriceRuleResponse `json:"overrides"`
}

type modelPriceUpsertRequest struct {
	input ModelPriceInput
}

func (request *modelPriceUpsertRequest) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONFields(data); err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	if len(object) != 2 {
		return fmt.Errorf("model price request must contain pattern and prices")
	}
	patternRaw, hasPattern := object["pattern"]
	pricesRaw, hasPrices := object["prices"]
	if !hasPattern || !hasPrices {
		return fmt.Errorf("model price request must contain pattern and prices")
	}
	var pattern string
	if err := json.Unmarshal(patternRaw, &pattern); err != nil {
		return fmt.Errorf("decode model price pattern: %w", err)
	}
	prices, err := decodeModelPriceValues(pricesRaw)
	if err != nil {
		return err
	}
	request.input = ModelPriceInput{
		Pattern:       pattern,
		UncachedInput: prices.UncachedInput,
		CacheRead:     prices.CacheRead,
		CacheWrite5M:  prices.CacheWrite5M,
		CacheWrite1H:  prices.CacheWrite1H,
		Output:        prices.Output,
	}
	return nil
}

func decodeModelPriceValues(data json.RawMessage) (modelPriceValuesInput, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '{' {
		return modelPriceValuesInput{}, fmt.Errorf("model price prices must be an object")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return modelPriceValuesInput{}, err
	}
	if len(object) != 5 {
		return modelPriceValuesInput{}, fmt.Errorf("model price prices must contain five values")
	}
	keys := []string{
		"input_price_usd_per_million_tokens",
		"output_price_usd_per_million_tokens",
		"cache_read_price_usd_per_million_tokens",
		"cache_write_5m_price_usd_per_million_tokens",
		"cache_write_1h_price_usd_per_million_tokens",
	}
	values := make([]*pricing.NanoUSD, len(keys))
	for index, key := range keys {
		raw, exists := object[key]
		if !exists {
			return modelPriceValuesInput{}, fmt.Errorf("model price prices must contain %s", key)
		}
		value, err := decodeNullableModelPrice(raw)
		if err != nil {
			return modelPriceValuesInput{}, fmt.Errorf("decode model price %s: %w", key, err)
		}
		values[index] = value
	}
	return modelPriceValuesInput{
		UncachedInput: values[0],
		Output:        values[1],
		CacheRead:     values[2],
		CacheWrite5M:  values[3],
		CacheWrite1H:  values[4],
	}, nil
}

func decodeNullableModelPrice(raw json.RawMessage) (*pricing.NanoUSD, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	parsed, err := pricing.ParseUSD(value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func (s *Server) handleListModelPrices(c *gin.Context) {
	result, err := s.service.ListModelPrices(c.Request.Context())
	if err != nil {
		writeServiceError(c, "list_model_prices", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleUpsertModelPrice(c *gin.Context) {
	var request modelPriceUpsertRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "upsert_model_price", mapControlJSONError(err))
		return
	}
	if pricing.ValidatePattern(request.input.Pattern) == nil {
		setMutationResourceLocator(
			c,
			"model-price:"+request.input.Pattern,
		)
	}
	if err := s.service.UpsertModelPrice(c.Request.Context(), request.input); err != nil {
		writeServiceError(c, "upsert_model_price", err)
		return
	}
	response.SuccessI18n(c, "common.success", nil)
}

func (s *Server) handleResetModelPrice(c *gin.Context) {
	pattern, apiErr := parseModelPricePattern(c.Request.URL.RawQuery)
	if apiErr != nil {
		writeServiceError(c, "reset_model_price", apiErr)
		return
	}
	setMutationResourceLocator(c, "model-price:"+pattern)
	if err := s.service.ResetModelPrice(c.Request.Context(), pattern); err != nil {
		writeServiceError(c, "reset_model_price", err)
		return
	}
	response.SuccessI18n(c, "common.success", nil)
}

func parseModelPricePattern(rawQuery string) (string, *app_errors.APIError) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", app_errors.ErrBadRequest
	}
	patternValues, ok := values["pattern"]
	if len(values) != 1 || !ok || len(patternValues) != 1 {
		return "", app_errors.ErrBadRequest
	}
	if err := pricing.ValidatePattern(patternValues[0]); err != nil {
		return "", app_errors.ErrValidation
	}
	return patternValues[0], nil
}

func newModelPriceRuleResponse(rule pricing.Rule) (modelPriceRuleResponse, error) {
	updatedAtMS, err := safeEpochMilliseconds(rule.UpdatedAt)
	if err != nil {
		return modelPriceRuleResponse{}, app_errors.ErrInternalServer
	}
	result := modelPriceRuleResponse{
		Pattern: rule.Pattern, Source: rule.Source, UpdatedAtMS: updatedAtMS,
		Prices: modelPriceValuesResponse{
			InputPrice:        modelPriceValuePointer(rule.Prices.UncachedInput),
			OutputPrice:       modelPriceValuePointer(rule.Prices.Output),
			CacheReadPrice:    modelPriceValuePointer(rule.Prices.CacheRead),
			CacheWrite5MPrice: modelPriceValuePointer(rule.Prices.CacheWrite5M),
			CacheWrite1HPrice: modelPriceValuePointer(rule.Prices.CacheWrite1H),
		},
	}
	if rule.SourceURL != "" {
		sourceURL := rule.SourceURL
		result.SourceURL = &sourceURL
	}
	if rule.LongContextPolicy != nil {
		result.PricingPolicy = &modelPricePolicyResponse{
			InputThresholdTokens: rule.LongContextPolicy.InputThresholdTokens,
			InputMultiplier:      modelPriceMultiplier(rule.LongContextPolicy.InputMultiplier),
			OutputMultiplier:     modelPriceMultiplier(rule.LongContextPolicy.OutputMultiplier),
		}
	}
	return result, nil
}

func modelPriceValuePointer(price pricing.Price) *string {
	if !price.Set {
		return nil
	}
	value := pricing.FormatUSD(price.NanoUSDPerMillion)
	return &value
}

func modelPriceMultiplier(value pricing.Multiplier) float64 {
	return float64(value.Numerator) / float64(value.Denominator)
}
