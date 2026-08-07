package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/pricing"
)

const (
	defaultModelPriceListPage     int64 = 1
	defaultModelPriceListPageSize int64 = 20
	maxModelPriceListPageSize     int64 = 100
	maxModelPriceSearchRunes            = 200
)

type ModelPriceUsage string

const (
	ModelPriceUsageInUse        ModelPriceUsage = "in_use"
	ModelPriceUsageUnreferenced ModelPriceUsage = "unreferenced"
	ModelPriceUsageAll          ModelPriceUsage = "all"
)

type ModelPriceListStatus string

const (
	ModelPriceStatusPending    ModelPriceListStatus = "pending"
	ModelPriceStatusConfigured ModelPriceListStatus = "configured"
	ModelPriceStatusAll        ModelPriceListStatus = "all"
)

type ModelPriceListQuery struct {
	Usage    ModelPriceUsage
	Status   ModelPriceListStatus
	Search   string
	Page     int64
	PageSize int64
}

type nullableDecimal struct {
	present bool
	nanoUSD *int64
}

type ModelPriceUpdateRequest struct {
	Input           nullableDecimal         `json:"input"`
	Output          nullableDecimal         `json:"output"`
	CacheRead       nullableDecimal         `json:"cache_read"`
	CacheWrite      nullableDecimal         `json:"cache_write"`
	ContextTiers    modelPriceTierListField `json:"context_tiers"`
	ConfirmUnpriced bool                    `json:"confirm_unpriced"`
}

// ModelPriceContextTierRequest is one submitted input-quantity price tier.
// Unlike the top-level request, prices use flat fields rather than a nested
// "prices" object, mirroring the existing request/response shape asymmetry.
type ModelPriceContextTierRequest struct {
	ThresholdTokens modelPriceTierThreshold `json:"threshold_tokens"`
	Input           nullableDecimal         `json:"input"`
	Output          nullableDecimal         `json:"output"`
	CacheRead       nullableDecimal         `json:"cache_read"`
	CacheWrite      nullableDecimal         `json:"cache_write"`
}

func (request ModelPriceUpdateRequest) validate() error {
	if !request.Input.present ||
		!request.Output.present ||
		!request.CacheRead.present ||
		!request.CacheWrite.present ||
		!request.ContextTiers.present {
		return fmt.Errorf("all model price slots are required: %w", app_errors.ErrValidation)
	}
	for _, tier := range request.ContextTiers.tiers {
		if !tier.ThresholdTokens.present {
			return fmt.Errorf("context tier threshold_tokens is required: %w", app_errors.ErrValidation)
		}
	}
	return nil
}

func (value *nullableDecimal) UnmarshalJSON(data []byte) error {
	value.present = true
	value.nanoUSD = nil
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}

	var decimal string
	if err := json.Unmarshal(data, &decimal); err != nil {
		return err
	}
	parsed, err := pricing.ParseUSD(decimal)
	if err != nil {
		return fmt.Errorf("parse model price decimal: %v: %w", err, app_errors.ErrValidation)
	}
	nanoUSD := int64(parsed)
	value.nanoUSD = &nanoUSD
	return nil
}

// modelPriceTierThreshold decodes a required canonical non-negative safe
// integer, matching the JS-safe-integer bound used across the control API.
type modelPriceTierThreshold struct {
	present bool
	tokens  int64
}

func (value *modelPriceTierThreshold) UnmarshalJSON(data []byte) error {
	value.present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("context tier threshold_tokens must not be null: %w", app_errors.ErrValidation)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("parse context tier threshold_tokens: %w", app_errors.ErrValidation)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("parse context tier threshold_tokens: trailing data: %w", app_errors.ErrValidation)
	}
	// json.Number decoding otherwise accepts a quoted string equally, which
	// would silently diverge from the bare-integer wire contract.
	number, ok := decoded.(json.Number)
	if !ok {
		return fmt.Errorf("context tier threshold_tokens must be a JSON number: %w", app_errors.ErrValidation)
	}
	parsed, err := parseCanonicalSafeUint(number.String())
	if err != nil {
		return fmt.Errorf(
			"context tier threshold_tokens must be a canonical non-negative safe integer: %w",
			app_errors.ErrValidation,
		)
	}
	value.tokens = int64(parsed)
	return nil
}

// modelPriceTierListField tracks whether "context_tiers" was present in the
// request so a PUT always states its full tier list, matching the existing
// full-replacement semantics of the four base price slots.
type modelPriceTierListField struct {
	present bool
	tiers   []ModelPriceContextTierRequest
}

func (field *modelPriceTierListField) UnmarshalJSON(data []byte) error {
	field.present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("context_tiers must not be null: %w", app_errors.ErrValidation)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var tiers []ModelPriceContextTierRequest
	if err := decoder.Decode(&tiers); err != nil {
		return fmt.Errorf("parse context_tiers: %w", app_errors.ErrValidation)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("parse context_tiers: trailing data: %w", app_errors.ErrValidation)
	}
	field.tiers = tiers
	return nil
}

func parseModelPriceListQuery(
	rawQuery string,
	forceQuery bool,
) (ModelPriceListQuery, *app_errors.APIError) {
	query := ModelPriceListQuery{
		Usage: ModelPriceUsageInUse, Status: ModelPriceStatusAll,
		Page: defaultModelPriceListPage, PageSize: defaultModelPriceListPageSize,
	}
	if forceQuery && rawQuery == "" {
		return ModelPriceListQuery{}, app_errors.ErrBadRequest
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ModelPriceListQuery{}, app_errors.ErrBadRequest
	}
	for key, entries := range values {
		switch key {
		case "usage", "status", "search", "page", "page_size":
		default:
			return ModelPriceListQuery{}, app_errors.ErrBadRequest
		}
		if len(entries) != 1 {
			return ModelPriceListQuery{}, app_errors.ErrBadRequest
		}
	}

	if entries, exists := values["usage"]; exists {
		query.Usage = ModelPriceUsage(entries[0])
		switch query.Usage {
		case ModelPriceUsageInUse, ModelPriceUsageUnreferenced, ModelPriceUsageAll:
		default:
			return ModelPriceListQuery{}, app_errors.ErrBadRequest
		}
	}
	if entries, exists := values["status"]; exists {
		query.Status = ModelPriceListStatus(entries[0])
		switch query.Status {
		case ModelPriceStatusPending, ModelPriceStatusConfigured, ModelPriceStatusAll:
		default:
			return ModelPriceListQuery{}, app_errors.ErrBadRequest
		}
	}
	if entries, exists := values["search"]; exists {
		query.Search = strings.TrimSpace(entries[0])
		if utf8.RuneCountInString(query.Search) > maxModelPriceSearchRunes {
			return ModelPriceListQuery{}, app_errors.ErrBadRequest
		}
	}
	if entries, exists := values["page"]; exists {
		page, err := parseCanonicalSafeUint(entries[0])
		if err != nil || page == 0 {
			return ModelPriceListQuery{}, app_errors.ErrBadRequest
		}
		query.Page = int64(page)
	}
	if entries, exists := values["page_size"]; exists {
		pageSize, err := parseCanonicalSafeUint(entries[0])
		if err != nil || pageSize == 0 || pageSize > uint64(maxModelPriceListPageSize) {
			return ModelPriceListQuery{}, app_errors.ErrBadRequest
		}
		query.PageSize = int64(pageSize)
	}
	return query, nil
}

func parseModelPriceRowID(value string) (uint, error) {
	parsed, err := parseCanonicalSafeUint(value)
	if err != nil || parsed == 0 || parsed > uint64(^uint(0)) {
		return 0, fmt.Errorf("model price ID must be a canonical positive safe integer")
	}
	return uint(parsed), nil
}

func (s *Server) handleListModelPrices(c *gin.Context) {
	query, apiErr := parseModelPriceListQuery(
		c.Request.URL.RawQuery,
		c.Request.URL.ForceQuery,
	)
	if apiErr != nil {
		writeServiceError(c, "list_model_prices", apiErr)
		return
	}
	result, err := s.service.ListModelPrices(c.Request.Context(), query)
	if err != nil {
		writeServiceError(c, "list_model_prices", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleUpdateModelPrice(c *gin.Context) {
	id, ok := modelPriceID(c, "update_model_price")
	if !ok || !modelPriceMutationQueryIsEmpty(c, "update_model_price") {
		return
	}
	var request ModelPriceUpdateRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "update_model_price", mapControlJSONError(err))
		return
	}
	if err := request.validate(); err != nil {
		writeServiceError(c, "update_model_price", mapControlJSONError(err))
		return
	}
	result, err := s.service.UpdateModelPrice(c.Request.Context(), id, request)
	if err != nil {
		writeServiceError(c, "update_model_price", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleResetModelPrice(c *gin.Context) {
	id, ok := modelPriceID(c, "reset_model_price")
	if !ok || !modelPriceMutationQueryIsEmpty(c, "reset_model_price") {
		return
	}
	if err := bindOptionalEmptyJSONObject(c); err != nil {
		writeServiceError(c, "reset_model_price", mapControlJSONError(err))
		return
	}
	result, err := s.service.ResetModelPrice(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, "reset_model_price", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleDeleteModelPrice(c *gin.Context) {
	id, ok := modelPriceID(c, "delete_model_price")
	if !ok || !modelPriceMutationQueryIsEmpty(c, "delete_model_price") {
		return
	}
	if err := s.service.DeleteModelPrice(c.Request.Context(), id); err != nil {
		writeServiceError(c, "delete_model_price", err)
		return
	}
	response.SuccessI18n(c, "common.success", nil)
}

func modelPriceID(c *gin.Context, operation string) (uint, bool) {
	id, err := parseModelPriceRowID(c.Param("id"))
	if err != nil {
		writeServiceError(c, operation, app_errors.ErrBadRequest)
		return 0, false
	}
	setMutationResourceLocator(c, fmt.Sprintf("model-price:%d", id))
	return id, true
}

func modelPriceMutationQueryIsEmpty(c *gin.Context, operation string) bool {
	if c.Request.URL.RawQuery == "" && !c.Request.URL.ForceQuery {
		return true
	}
	writeServiceError(c, operation, app_errors.ErrBadRequest)
	return false
}
