package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
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
	ID                  uint                           `json:"id"`
	ChannelID           string                         `json:"channel_id"`
	ChannelName         string                         `json:"channel_name"`
	ChannelMark         string                         `json:"channel_mark"`
	ChannelIcon         string                         `json:"channel_icon"`
	ModelID             string                         `json:"model_id"`
	Prices              PriceSlotsDTO                  `json:"prices"`
	ModePrices          map[pricing.Mode]PriceSlotsDTO `json:"mode_prices"`
	PricingStatus       PricingStatus                  `json:"pricing_status"`
	Method              *string                        `json:"method"`
	MatchedProviderID   *string                        `json:"matched_provider_id"`
	MatchSource         *ModelPriceMatchSource         `json:"match_source"`
	Referenced          bool                           `json:"referenced"`
	ReferenceCount      int                            `json:"reference_count"`
	ReferenceGroupCount int                            `json:"reference_group_count"`
	ContextTiers        []ModelPriceContextTierDTO     `json:"context_tiers"`
	UpdatedAtMS         int64                          `json:"updated_at_ms"`
	CanReset            bool                           `json:"can_reset"`
	CanDelete           bool                           `json:"can_delete"`
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

		table, err = loadPriceTable(ctx, tx, catalogSnapshot)
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
		identity, err := PriceIdentityForChannelModel(row.ChannelID, row.ModelID)
		if err != nil {
			return fmt.Errorf("validate model price identity: %w", app_errors.ErrInternalServer)
		}
		desired, err := resetModelPriceValues(identity, catalogSnapshot)
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

		table, err = loadPriceTable(ctx, tx, catalogSnapshot)
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

func resetModelPriceValues(identity pricing.Identity, snapshot *catalog.Snapshot) (models.ModelPrice, error) {
	match, ok := resolveAutomaticPriceForIdentity(snapshot, identity)
	if !ok {
		return models.ModelPrice{}, nil
	}
	return automaticCatalogValues(match.cost)
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

	var catalogSnapshot *catalog.Snapshot
	if s.catalogRuntime != nil {
		catalogSnapshot = s.catalogRuntime.Load()
	}
	var table *pricing.Table
	err := s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		var row models.ModelPrice
		if err := tx.First(&row, id).Error; err != nil {
			return fmt.Errorf("load model price: %w", app_errors.ParseDBError(err))
		}
		identity, err := PriceIdentityForChannelModel(row.ChannelID, row.ModelID)
		if err != nil {
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
		table, err = loadPriceTable(ctx, tx, catalogSnapshot)
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
	rule, err := persistedPriceRule(row, catalogSnapshot)
	if err != nil {
		return modelPriceListRecord{}, fmt.Errorf("decode persisted model price: %w", app_errors.ErrInternalServer)
	}
	if _, err := pricing.NewTable([]pricing.Rule{rule}); err != nil {
		return modelPriceListRecord{}, fmt.Errorf("validate persisted model price: %w", app_errors.ErrInternalServer)
	}
	identity := rule.Identity
	descriptor, ok := modelPriceChannelRegistry.Get(channel.ID(identity.ChannelID))
	if !ok || strings.TrimSpace(descriptor.Name) == "" {
		return modelPriceListRecord{}, fmt.Errorf("resolve model price channel: %w", app_errors.ErrInternalServer)
	}
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
	var matchSource *ModelPriceMatchSource
	matchedAutomaticPrice := false
	if !row.IsManual {
		match, matched := resolveAutomaticPriceForIdentity(catalogSnapshot, identity)
		matchedAutomaticPrice = matched
		if configured && matchedAutomaticPrice {
			providerID := match.providerID
			matchedProviderID = &providerID
			source := match.source
			matchSource = &source
		} else {
			matchedAutomaticPrice = false
		}
	}
	dto := ModelPriceDTO{
		ID: row.ID, ChannelID: row.ChannelID, ChannelName: descriptor.Name,
		ChannelMark: descriptor.Mark, ChannelIcon: descriptor.Icon, ModelID: row.ModelID,
		Prices:              prices,
		ModePrices:          projectModePrices(rule.ModePrices),
		PricingStatus:       status,
		Method:              modelPriceMethod(row, configured, matchedAutomaticPrice),
		MatchedProviderID:   matchedProviderID,
		MatchSource:         matchSource,
		Referenced:          reference.referenceCount > 0,
		ReferenceCount:      reference.referenceCount,
		ReferenceGroupCount: reference.referenceGroupCount(),
		ContextTiers:        projectContextPriceTiers(rule.ContextTiers),
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

func projectPriceSlots(prices pricing.Prices) PriceSlotsDTO {
	return PriceSlotsDTO{
		Input:      modelPriceWireDecimalFromPrice(prices.Input),
		Output:     modelPriceWireDecimalFromPrice(prices.Output),
		CacheRead:  modelPriceWireDecimalFromPrice(prices.CacheRead),
		CacheWrite: modelPriceWireDecimalFromPrice(prices.CacheWrite),
	}
}

func projectModePrices(modePrices map[pricing.Mode]pricing.Prices) map[pricing.Mode]PriceSlotsDTO {
	result := make(map[pricing.Mode]PriceSlotsDTO, len(modePrices))
	for mode, prices := range modePrices {
		result[mode] = projectPriceSlots(prices)
	}
	return result
}

func projectContextPriceTiers(tiers []pricing.ContextTier) []ModelPriceContextTierDTO {
	result := make([]ModelPriceContextTierDTO, 0, len(tiers))
	for _, tier := range tiers {
		result = append(result, ModelPriceContextTierDTO{
			ThresholdTokens: tier.InputThresholdTokens,
			Prices:          projectPriceSlots(tier.Prices),
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
		accessKeyCollectionContainsFold(record.dto.ModelID, query.Search) ||
		accessKeyCollectionContainsFold(record.dto.ChannelID, query.Search)
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
		if left.dto.ChannelID != right.dto.ChannelID {
			return left.dto.ChannelID < right.dto.ChannelID
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
