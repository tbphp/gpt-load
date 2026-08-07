package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

type PriceSlotsDTO struct {
	Input      *string `json:"input"`
	Output     *string `json:"output"`
	CacheRead  *string `json:"cache_read"`
	CacheWrite *string `json:"cache_write"`
}

type ModelPriceDTO struct {
	ID                  uint                       `json:"id"`
	ModelID             string                     `json:"model_id"`
	Prices              PriceSlotsDTO              `json:"prices"`
	PricingStatus       PricingStatus              `json:"pricing_status"`
	Method              *string                    `json:"method"`
	MatchedProviderID   *string                    `json:"matched_provider_id"`
	Referenced          bool                       `json:"referenced"`
	ReferenceCount      int                        `json:"reference_count"`
	ReferenceGroupCount int                        `json:"reference_group_count"`
	ContextTiers        []ModelPriceContextTierDTO `json:"context_tiers"`
	Partial             bool                       `json:"partial"`
	UpdatedAtMS         int64                      `json:"updated_at_ms"`
	CanReset            bool                       `json:"can_reset"`
	CanDelete           bool                       `json:"can_delete"`
}

// ModelPriceContextTierDTO is one compiled input-quantity price tier. Unlike
// the request shape, prices nest under "prices" to mirror the top-level DTO.
type ModelPriceContextTierDTO struct {
	ThresholdTokens int64         `json:"threshold_tokens"`
	Prices          PriceSlotsDTO `json:"prices"`
}

type ModelPricePaginationDTO struct {
	Page       int64 `json:"page"`
	PageSize   int64 `json:"page_size"`
	TotalItems int64 `json:"total_items"`
	TotalPages int64 `json:"total_pages"`
}

type ModelPriceListResponse struct {
	Items      []ModelPriceDTO         `json:"items"`
	Pagination ModelPricePaginationDTO `json:"pagination"`
}

type modelPriceListRecord struct {
	dto ModelPriceDTO
}

type ModelPriceIDData struct {
	ID uint `json:"id"`
}

type ModelPriceReferenceData struct {
	ID                  uint `json:"id"`
	ReferenceCount      int  `json:"reference_count"`
	ReferenceGroupCount int  `json:"reference_group_count"`
}

func (s *Service) UpdateModelPrice(
	ctx context.Context,
	id uint,
	request ModelPriceUpdateRequest,
) (ModelPriceDTO, error) {
	if id == 0 || uint64(id) > uint64(maxSafeInteger) {
		return ModelPriceDTO{}, app_errors.ErrBadRequest
	}
	if err := request.validate(); err != nil {
		return ModelPriceDTO{}, err
	}
	contextTiers, err := buildContextPriceTiersJSON(request.ContextTiers.tiers)
	if err != nil {
		return ModelPriceDTO{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.enforceOperationRecoveryBarrierLocked(ctx, 0); err != nil {
		return ModelPriceDTO{}, err
	}
	var catalogSnapshot *catalog.Snapshot
	if s.catalogRuntime != nil {
		catalogSnapshot = s.catalogRuntime.Load()
	}

	var table *pricing.Table
	var result ModelPriceDTO
	err = s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		var row models.ModelPrice
		if err := tx.First(&row, id).Error; err != nil {
			return fmt.Errorf("load model price: %w", app_errors.ParseDBError(err))
		}
		references, err := loadPriceReferenceSnapshot(tx)
		if err != nil {
			return err
		}
		if modelPriceUpdateAllNull(request) && !request.ConfirmUnpriced {
			return app_errors.NewAPIErrorWithData(
				app_errors.ErrModelPriceUnpricedConfirmationRequired,
				ModelPriceIDData{ID: id},
			)
		}

		desired := row
		desired.InputPriceNanoUSDPerMillionTokens = cloneModelPriceValue(request.Input.nanoUSD)
		desired.OutputPriceNanoUSDPerMillionTokens = cloneModelPriceValue(request.Output.nanoUSD)
		desired.CacheReadPriceNanoUSDPerMillionTokens = cloneModelPriceValue(request.CacheRead.nanoUSD)
		desired.CacheWritePriceNanoUSDPerMillionTokens = cloneModelPriceValue(request.CacheWrite.nanoUSD)
		desired.ContextPriceTiers = contextTiers
		desired.IsManual = true
		if !modelPriceMutableValuesEqual(row, desired) {
			updatedAtMS, err := safeEpochMilliseconds(s.now())
			if err != nil {
				return fmt.Errorf("timestamp model price update: %w", app_errors.ErrInternalServer)
			}
			if err := tx.Model(&models.ModelPrice{}).
				Where("id = ?", id).
				Updates(map[string]any{
					"input_price_nano_usd_per_million_tokens":       desired.InputPriceNanoUSDPerMillionTokens,
					"output_price_nano_usd_per_million_tokens":      desired.OutputPriceNanoUSDPerMillionTokens,
					"cache_read_price_nano_usd_per_million_tokens":  desired.CacheReadPriceNanoUSDPerMillionTokens,
					"cache_write_price_nano_usd_per_million_tokens": desired.CacheWritePriceNanoUSDPerMillionTokens,
					"context_price_tiers":                           desired.ContextPriceTiers,
					"is_manual":                                     true,
					"updated_at_ms":                                 updatedAtMS,
				}).Error; err != nil {
				return fmt.Errorf("update model price: %w", app_errors.ParseDBError(err))
			}
			desired.UpdatedAtMS = updatedAtMS
			row = desired
		}

		table, err = loadPriceTable(ctx, tx)
		if err != nil {
			return err
		}
		record, err := projectModelPriceRow(row, references, catalogSnapshot)
		if err != nil {
			return err
		}
		result = record.dto
		return nil
	})
	if err != nil {
		return ModelPriceDTO{}, err
	}
	s.priceRuntime.Publish(table)
	return result, nil
}

func (s *Service) ResetModelPrice(
	ctx context.Context,
	id uint,
) (ModelPriceDTO, error) {
	if id == 0 || uint64(id) > uint64(maxSafeInteger) {
		return ModelPriceDTO{}, app_errors.ErrBadRequest
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.enforceOperationRecoveryBarrierLocked(ctx, 0); err != nil {
		return ModelPriceDTO{}, err
	}
	var catalogSnapshot *catalog.Snapshot
	if s.catalogRuntime != nil {
		catalogSnapshot = s.catalogRuntime.Load()
	}

	var table *pricing.Table
	var result ModelPriceDTO
	err := s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		var row models.ModelPrice
		if err := tx.First(&row, id).Error; err != nil {
			return fmt.Errorf("load model price: %w", app_errors.ParseDBError(err))
		}
		references, err := loadPriceReferenceSnapshot(tx)
		if err != nil {
			return err
		}
		desired, err := resetModelPriceValues(row.ModelID, catalogSnapshot)
		if err != nil {
			return fmt.Errorf("normalize catalog model price: %w", app_errors.ErrInternalServer)
		}
		if !modelPriceMutableValuesEqual(row, desired) {
			updatedAtMS, err := safeEpochMilliseconds(s.now())
			if err != nil {
				return fmt.Errorf("timestamp model price reset: %w", app_errors.ErrInternalServer)
			}
			if err := tx.Model(&models.ModelPrice{}).
				Where("id = ?", id).
				Updates(map[string]any{
					"input_price_nano_usd_per_million_tokens":       desired.InputPriceNanoUSDPerMillionTokens,
					"output_price_nano_usd_per_million_tokens":      desired.OutputPriceNanoUSDPerMillionTokens,
					"cache_read_price_nano_usd_per_million_tokens":  desired.CacheReadPriceNanoUSDPerMillionTokens,
					"cache_write_price_nano_usd_per_million_tokens": desired.CacheWritePriceNanoUSDPerMillionTokens,
					"context_price_tiers":                           desired.ContextPriceTiers,
					"is_manual":                                     false,
					"updated_at_ms":                                 updatedAtMS,
				}).Error; err != nil {
				return fmt.Errorf("reset model price: %w", app_errors.ParseDBError(err))
			}
			row.InputPriceNanoUSDPerMillionTokens = desired.InputPriceNanoUSDPerMillionTokens
			row.OutputPriceNanoUSDPerMillionTokens = desired.OutputPriceNanoUSDPerMillionTokens
			row.CacheReadPriceNanoUSDPerMillionTokens = desired.CacheReadPriceNanoUSDPerMillionTokens
			row.CacheWritePriceNanoUSDPerMillionTokens = desired.CacheWritePriceNanoUSDPerMillionTokens
			row.ContextPriceTiers = desired.ContextPriceTiers
			row.IsManual = false
			row.UpdatedAtMS = updatedAtMS
		}

		table, err = loadPriceTable(ctx, tx)
		if err != nil {
			return err
		}
		record, err := projectModelPriceRow(row, references, catalogSnapshot)
		if err != nil {
			return err
		}
		result = record.dto
		return nil
	})
	if err != nil {
		return ModelPriceDTO{}, err
	}
	s.priceRuntime.Publish(table)
	return result, nil
}

func resetModelPriceValues(modelID string, snapshot *catalog.Snapshot) (models.ModelPrice, error) {
	cost, _, ok := resolveAutomaticPrice(snapshot, modelID)
	if !ok {
		return models.ModelPrice{}, nil
	}
	return automaticCatalogValues(cost)
}

func (s *Service) DeleteModelPrice(ctx context.Context, id uint) error {
	if id == 0 || uint64(id) > uint64(maxSafeInteger) {
		return app_errors.ErrBadRequest
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.enforceOperationRecoveryBarrierLocked(ctx, 0); err != nil {
		return err
	}

	var table *pricing.Table
	err := s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		var row models.ModelPrice
		if err := tx.First(&row, id).Error; err != nil {
			return fmt.Errorf("load model price: %w", app_errors.ParseDBError(err))
		}
		identity := pricing.Identity{ModelID: row.ModelID}
		if _, err := pricing.NewTable([]pricing.Rule{{Identity: identity}}); err != nil {
			return fmt.Errorf("validate model price identity: %w", app_errors.ErrInternalServer)
		}
		references, err := loadPriceReferenceSnapshot(tx)
		if err != nil {
			return err
		}
		reference := references.references[identity]
		if reference.referenceCount > 0 {
			return app_errors.NewAPIErrorWithData(
				app_errors.ErrModelPriceReferenced,
				ModelPriceReferenceData{
					ID: id, ReferenceCount: reference.referenceCount,
					ReferenceGroupCount: reference.referenceGroupCount(),
				},
			)
		}
		if !row.IsManual {
			return app_errors.NewAPIErrorWithData(
				app_errors.ErrModelPriceAutomaticDeleteForbidden,
				ModelPriceIDData{ID: id},
			)
		}
		if err := tx.Where("id = ?", id).Delete(&models.ModelPrice{}).Error; err != nil {
			return fmt.Errorf("delete model price: %w", app_errors.ParseDBError(err))
		}
		table, err = loadPriceTable(ctx, tx)
		return err
	})
	if err != nil {
		return err
	}
	s.priceRuntime.Publish(table)
	return nil
}

func modelPriceUpdateAllNull(request ModelPriceUpdateRequest) bool {
	if request.Input.nanoUSD != nil ||
		request.Output.nanoUSD != nil ||
		request.CacheRead.nanoUSD != nil ||
		request.CacheWrite.nanoUSD != nil {
		return false
	}
	for _, tier := range request.ContextTiers.tiers {
		if tier.Input.nanoUSD != nil ||
			tier.Output.nanoUSD != nil ||
			tier.CacheRead.nanoUSD != nil ||
			tier.CacheWrite.nanoUSD != nil {
			return false
		}
	}
	return true
}

func cloneModelPriceValue(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// buildContextPriceTiersJSON converts the submitted tier list to the
// persisted JSON shape, validating it through the single authoritative rule
// set (models.NormalizeContextPriceTiers) before any transaction opens. This
// keeps a malformed submission a plain validation error instead of a GORM
// hook failure surfacing through ParseDBError.
func buildContextPriceTiersJSON(tiers []ModelPriceContextTierRequest) (models.JSON, error) {
	if len(tiers) == 0 {
		return nil, nil
	}
	encoded := make([]models.ContextPriceTier, 0, len(tiers))
	for _, tier := range tiers {
		encoded = append(encoded, models.ContextPriceTier{
			ThresholdTokens:                        tier.ThresholdTokens.tokens,
			InputPriceNanoUSDPerMillionTokens:      cloneModelPriceValue(tier.Input.nanoUSD),
			OutputPriceNanoUSDPerMillionTokens:     cloneModelPriceValue(tier.Output.nanoUSD),
			CacheReadPriceNanoUSDPerMillionTokens:  cloneModelPriceValue(tier.CacheRead.nanoUSD),
			CacheWritePriceNanoUSDPerMillionTokens: cloneModelPriceValue(tier.CacheWrite.nanoUSD),
		})
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		return nil, fmt.Errorf("encode context price tiers: %w", app_errors.ErrInternalServer)
	}
	normalized, err := models.NormalizeContextPriceTiers(models.JSON(raw))
	if err != nil {
		return nil, fmt.Errorf("validate context price tiers: %w", app_errors.ErrValidation)
	}
	return normalized, nil
}

func modelPriceMutableValuesEqual(left, right models.ModelPrice) bool {
	leftTiers, leftErr := models.NormalizeContextPriceTiers(left.ContextPriceTiers)
	rightTiers, rightErr := models.NormalizeContextPriceTiers(right.ContextPriceTiers)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return pricePointerEqual(left.InputPriceNanoUSDPerMillionTokens, right.InputPriceNanoUSDPerMillionTokens) &&
		pricePointerEqual(left.OutputPriceNanoUSDPerMillionTokens, right.OutputPriceNanoUSDPerMillionTokens) &&
		pricePointerEqual(left.CacheReadPriceNanoUSDPerMillionTokens, right.CacheReadPriceNanoUSDPerMillionTokens) &&
		pricePointerEqual(left.CacheWritePriceNanoUSDPerMillionTokens, right.CacheWritePriceNanoUSDPerMillionTokens) &&
		bytes.Equal(leftTiers, rightTiers) &&
		left.IsManual == right.IsManual
}

func (s *Service) ListModelPrices(
	ctx context.Context,
	query ModelPriceListQuery,
) (ModelPriceListResponse, error) {
	if err := validateModelPriceListQuery(query); err != nil {
		return ModelPriceListResponse{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	var catalogSnapshot *catalog.Snapshot
	if s.catalogRuntime != nil {
		catalogSnapshot = s.catalogRuntime.Load()
	}
	var rows []models.ModelPrice
	var references priceReferenceSnapshot
	if err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		var err error
		references, err = loadPriceReferenceSnapshot(tx)
		if err != nil {
			return err
		}
		if err := tx.Order("id ASC").Find(&rows).Error; err != nil {
			return fmt.Errorf("load model prices: %w", app_errors.ParseDBError(err))
		}
		return nil
	}); err != nil {
		return ModelPriceListResponse{}, err
	}

	records := make([]modelPriceListRecord, 0, len(rows))
	for _, row := range rows {
		record, err := projectModelPriceRow(row, references, catalogSnapshot)
		if err != nil {
			return ModelPriceListResponse{}, err
		}
		if !modelPriceRecordMatches(record, query) {
			continue
		}
		records = append(records, record)
	}
	sortModelPriceRecords(records)

	totalItems := int64(len(records))
	return ModelPriceListResponse{
		Items: modelPricePageItems(records, query.Page, query.PageSize),
		Pagination: ModelPricePaginationDTO{
			Page: query.Page, PageSize: query.PageSize, TotalItems: totalItems,
			TotalPages: modelPriceTotalPages(totalItems, query.PageSize),
		},
	}, nil
}

func validateModelPriceListQuery(query ModelPriceListQuery) error {
	switch query.Usage {
	case ModelPriceUsageInUse, ModelPriceUsageUnreferenced, ModelPriceUsageAll:
	default:
		return app_errors.ErrValidation
	}
	switch query.Status {
	case ModelPriceStatusPending, ModelPriceStatusConfigured, ModelPriceStatusAll:
	default:
		return app_errors.ErrValidation
	}
	if query.Page <= 0 || query.Page > maxSafeInteger ||
		query.PageSize <= 0 || query.PageSize > maxModelPriceListPageSize ||
		len([]rune(query.Search)) > maxModelPriceSearchRunes {
		return app_errors.ErrValidation
	}
	return nil
}

func projectModelPriceRow(
	row models.ModelPrice,
	references priceReferenceSnapshot,
	catalogSnapshot *catalog.Snapshot,
) (modelPriceListRecord, error) {
	if row.ID == 0 || uint64(row.ID) > uint64(maxSafeInteger) || validateSafeMilliseconds(row.UpdatedAtMS) != nil {
		return modelPriceListRecord{}, fmt.Errorf("invalid persisted model price wire identity: %w", app_errors.ErrInternalServer)
	}
	rule, err := persistedPriceRule(row)
	if err != nil {
		return modelPriceListRecord{}, fmt.Errorf("decode persisted model price: %w", app_errors.ErrInternalServer)
	}
	if _, err := pricing.NewTable([]pricing.Rule{rule}); err != nil {
		return modelPriceListRecord{}, fmt.Errorf("validate persisted model price: %w", app_errors.ErrInternalServer)
	}
	identity := pricing.Identity{ModelID: row.ModelID}
	reference := references.references[identity]
	status := resolvePricingStatus(&row)
	prices := PriceSlotsDTO{
		Input:      modelPriceWireDecimal(row.InputPriceNanoUSDPerMillionTokens),
		Output:     modelPriceWireDecimal(row.OutputPriceNanoUSDPerMillionTokens),
		CacheRead:  modelPriceWireDecimal(row.CacheReadPriceNanoUSDPerMillionTokens),
		CacheWrite: modelPriceWireDecimal(row.CacheWritePriceNanoUSDPerMillionTokens),
	}
	configured := modelPriceHasConfiguredValue(row)
	var matchedProviderID *string
	matchedAutomaticPrice := false
	matchedProvider := ""
	if !row.IsManual {
		_, matchedProvider, matchedAutomaticPrice = resolveAutomaticPrice(catalogSnapshot, row.ModelID)
		if configured && matchedAutomaticPrice {
			matchedProviderID = &matchedProvider
		} else {
			matchedAutomaticPrice = false
		}
	}
	dto := ModelPriceDTO{
		ID: row.ID, ModelID: row.ModelID,
		Prices:              prices,
		PricingStatus:       status,
		Method:              modelPriceMethod(row, configured, matchedAutomaticPrice),
		MatchedProviderID:   matchedProviderID,
		Referenced:          reference.referenceCount > 0,
		ReferenceCount:      reference.referenceCount,
		ReferenceGroupCount: reference.referenceGroupCount(),
		ContextTiers:        projectContextPriceTiers(rule.ContextTiers),
		Partial:             modelPriceRulePartial(row, rule),
		UpdatedAtMS:         row.UpdatedAtMS,
		CanReset:            row.IsManual,
		CanDelete:           row.IsManual && reference.referenceCount == 0,
	}
	return modelPriceListRecord{dto: dto}, nil
}

func modelPriceMethod(
	row models.ModelPrice,
	configured bool,
	matchedAutomaticPrice bool,
) *string {
	if !row.IsManual {
		if !configured || !matchedAutomaticPrice {
			return nil
		}
		method := "auto_sync"
		return &method
	}

	method := "user_set"
	if !configured {
		method = "user_marked_unpriced"
	}
	return &method
}

func modelPriceConfiguredSlotCount(row models.ModelPrice) int {
	count := 0
	for _, value := range []*int64{
		row.InputPriceNanoUSDPerMillionTokens,
		row.OutputPriceNanoUSDPerMillionTokens,
		row.CacheReadPriceNanoUSDPerMillionTokens,
		row.CacheWritePriceNanoUSDPerMillionTokens,
	} {
		if value != nil {
			count++
		}
	}
	return count
}

// modelPriceRulePartial reports whether any reachable input range lacks a
// complete four-slot price. Context tiers replace the base prices, so their
// completeness must be evaluated independently rather than inferred from the
// base row alone.
func modelPriceRulePartial(row models.ModelPrice, rule pricing.Rule) bool {
	baseSlots := modelPriceConfiguredSlotCount(row)
	if baseSlots > 0 && baseSlots < 4 {
		return true
	}
	if baseSlots == 0 && len(rule.ContextTiers) > 0 && rule.ContextTiers[0].InputThresholdTokens > 0 {
		return true
	}
	for _, tier := range rule.ContextTiers {
		configured := 0
		for _, value := range []pricing.Price{
			tier.Prices.Input,
			tier.Prices.Output,
			tier.Prices.CacheRead,
			tier.Prices.CacheWrite,
		} {
			if value.Set {
				configured++
			}
		}
		if configured > 0 && configured < 4 {
			return true
		}
	}
	return false
}

func modelPriceWireDecimal(value *int64) *string {
	if value == nil {
		return nil
	}
	formatted := pricing.FormatUSD(pricing.NanoUSD(*value))
	return &formatted
}

func modelPriceWireDecimalFromPrice(price pricing.Price) *string {
	if !price.Set {
		return nil
	}
	formatted := pricing.FormatUSD(price.NanoUSDPerMillion)
	return &formatted
}

func projectContextPriceTiers(tiers []pricing.ContextTier) []ModelPriceContextTierDTO {
	result := make([]ModelPriceContextTierDTO, 0, len(tiers))
	for _, tier := range tiers {
		result = append(result, ModelPriceContextTierDTO{
			ThresholdTokens: tier.InputThresholdTokens,
			Prices: PriceSlotsDTO{
				Input:      modelPriceWireDecimalFromPrice(tier.Prices.Input),
				Output:     modelPriceWireDecimalFromPrice(tier.Prices.Output),
				CacheRead:  modelPriceWireDecimalFromPrice(tier.Prices.CacheRead),
				CacheWrite: modelPriceWireDecimalFromPrice(tier.Prices.CacheWrite),
			},
		})
	}
	return result
}

func modelPriceRecordMatches(record modelPriceListRecord, query ModelPriceListQuery) bool {
	switch query.Usage {
	case ModelPriceUsageInUse:
		if !record.dto.Referenced {
			return false
		}
	case ModelPriceUsageUnreferenced:
		if record.dto.Referenced {
			return false
		}
	}
	if query.Status != ModelPriceStatusAll && string(record.dto.PricingStatus) != string(query.Status) {
		return false
	}
	return query.Search == "" ||
		accessKeyCollectionContainsFold(record.dto.ModelID, query.Search)
}

func sortModelPriceRecords(records []modelPriceListRecord) {
	sort.Slice(records, func(leftIndex, rightIndex int) bool {
		left, right := records[leftIndex], records[rightIndex]
		if left.dto.PricingStatus != right.dto.PricingStatus {
			return left.dto.PricingStatus == PricingStatusPending
		}
		if left.dto.Referenced != right.dto.Referenced {
			return left.dto.Referenced
		}
		if left.dto.ModelID != right.dto.ModelID {
			return left.dto.ModelID < right.dto.ModelID
		}
		return left.dto.ID < right.dto.ID
	})
}

func modelPriceTotalPages(totalItems, pageSize int64) int64 {
	if totalItems == 0 || pageSize <= 0 {
		return 0
	}
	pages := totalItems / pageSize
	if totalItems%pageSize != 0 {
		pages++
	}
	return pages
}

func modelPricePageItems(
	records []modelPriceListRecord,
	page, pageSize int64,
) []ModelPriceDTO {
	items := []ModelPriceDTO{}
	itemCount := int64(len(records))
	if page <= 0 || pageSize <= 0 || page-1 > itemCount/pageSize {
		return items
	}
	offset := (page - 1) * pageSize
	if offset >= itemCount {
		return items
	}
	end := offset + pageSize
	if end < offset || end > itemCount {
		end = itemCount
	}
	items = make([]ModelPriceDTO, 0, end-offset)
	for _, record := range records[offset:end] {
		items = append(items, record.dto)
	}
	return items
}
