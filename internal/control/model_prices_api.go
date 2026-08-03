package control

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	Input           nullableDecimal `json:"input"`
	Output          nullableDecimal `json:"output"`
	CacheRead       nullableDecimal `json:"cache_read"`
	CacheWrite      nullableDecimal `json:"cache_write"`
	ConfirmUnpriced bool            `json:"confirm_unpriced"`
}

func (request ModelPriceUpdateRequest) validate() error {
	if !request.Input.present ||
		!request.Output.present ||
		!request.CacheRead.present ||
		!request.CacheWrite.present {
		return fmt.Errorf("all four model price slots are required: %w", app_errors.ErrValidation)
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
