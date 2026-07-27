package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/pricing"
)

type modelPriceValuesResponse struct {
	UncachedInput *float64 `json:"uncached_input"`
	CacheRead     *float64 `json:"cache_read"`
	CacheWrite5M  *float64 `json:"cache_write_5m"`
	CacheWrite1H  *float64 `json:"cache_write_1h"`
	Output        *float64 `json:"output"`
}

type modelPriceRuleResponse struct {
	Pattern   string                   `json:"pattern"`
	Source    pricing.Source           `json:"source"`
	Prices    modelPriceValuesResponse `json:"prices"`
	SourceURL *string                  `json:"source_url"`
	UpdatedAt time.Time                `json:"updated_at"`
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

func decodeModelPriceValues(data json.RawMessage) (modelPriceValuesResponse, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '{' {
		return modelPriceValuesResponse{}, fmt.Errorf("model price prices must be an object")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return modelPriceValuesResponse{}, err
	}
	if len(object) != 5 {
		return modelPriceValuesResponse{}, fmt.Errorf("model price prices must contain five values")
	}
	keys := []string{
		"uncached_input", "cache_read", "cache_write_5m", "cache_write_1h", "output",
	}
	values := make([]*float64, len(keys))
	for index, key := range keys {
		raw, exists := object[key]
		if !exists {
			return modelPriceValuesResponse{}, fmt.Errorf("model price prices must contain %s", key)
		}
		value, err := decodeNullableModelPrice(raw)
		if err != nil {
			return modelPriceValuesResponse{}, fmt.Errorf("decode model price %s: %w", key, err)
		}
		values[index] = value
	}
	return modelPriceValuesResponse{
		UncachedInput: values[0], CacheRead: values[1], CacheWrite5M: values[2],
		CacheWrite1H: values[3], Output: values[4],
	}, nil
}

func decodeNullableModelPrice(raw json.RawMessage) (*float64, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return &value, nil
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

func newModelPriceRuleResponse(rule pricing.Rule) modelPriceRuleResponse {
	result := modelPriceRuleResponse{
		Pattern: rule.Pattern, Source: rule.Source, UpdatedAt: rule.UpdatedAt,
		Prices: modelPriceValuesResponse{
			UncachedInput: modelPriceValuePointer(rule.Prices.UncachedInput),
			CacheRead:     modelPriceValuePointer(rule.Prices.CacheRead),
			CacheWrite5M:  modelPriceValuePointer(rule.Prices.CacheWrite5M),
			CacheWrite1H:  modelPriceValuePointer(rule.Prices.CacheWrite1H),
			Output:        modelPriceValuePointer(rule.Prices.Output),
		},
	}
	if rule.SourceURL != "" {
		sourceURL := rule.SourceURL
		result.SourceURL = &sourceURL
	}
	return result
}

func modelPriceValuePointer(price pricing.Price) *float64 {
	if !price.Set {
		return nil
	}
	value := price.Value
	return &value
}
