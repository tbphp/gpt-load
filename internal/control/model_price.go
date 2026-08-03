package control

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

type PriceScopeDTO struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label"`
}

type PriceSlotsDTO struct {
	Input      *string `json:"input"`
	Output     *string `json:"output"`
	CacheRead  *string `json:"cache_read"`
	CacheWrite *string `json:"cache_write"`
}

type ModelPriceDTO struct {
	ID                  uint          `json:"id"`
	ModelID             string        `json:"model_id"`
	Scope               PriceScopeDTO `json:"scope"`
	Prices              PriceSlotsDTO `json:"prices"`
	PricingStatus       PricingStatus `json:"pricing_status"`
	Method              *string       `json:"method"`
	Referenced          bool          `json:"referenced"`
	ReferenceCount      int           `json:"reference_count"`
	ReferenceGroupCount int           `json:"reference_group_count"`
	HasContextTiers     bool          `json:"has_context_tiers"`
	Partial             bool          `json:"partial"`
	UpdatedAtMS         int64         `json:"updated_at_ms"`
	CanReset            bool          `json:"can_reset"`
	CanDelete           bool          `json:"can_delete"`
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
	dto      ModelPriceDTO
	scopeKey string
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
		if _, err := parsePriceScopeKey(row.PriceScopeKey); err != nil {
			return fmt.Errorf("validate model price scope: %w", app_errors.ErrInternalServer)
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
		desired.ContextPriceTiers = nil
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
					"context_price_tiers":                           nil,
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
		scope, err := parsePriceScopeKey(row.PriceScopeKey)
		if err != nil {
			return fmt.Errorf("validate model price scope: %w", app_errors.ErrInternalServer)
		}
		references, err := loadPriceReferenceSnapshot(tx)
		if err != nil {
			return err
		}
		desired, err := resetModelPriceValues(scope, row.ModelID, catalogSnapshot)
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

func resetModelPriceValues(
	scope parsedPriceScope,
	modelID string,
	snapshot *catalog.Snapshot,
) (models.ModelPrice, error) {
	if scope.kind != priceScopeKindProvider || snapshot == nil {
		return models.ModelPrice{}, nil
	}
	provider, exists := snapshot.Providers[scope.id]
	if !exists {
		return models.ModelPrice{}, nil
	}
	model, exists := provider.Models[modelID]
	if !exists || model.Cost == nil {
		return models.ModelPrice{}, nil
	}
	return automaticCatalogValues(model)
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
		if _, err := parsePriceScopeKey(row.PriceScopeKey); err != nil {
			return fmt.Errorf("validate model price scope: %w", app_errors.ErrInternalServer)
		}
		identity := pricing.Identity{ScopeKey: row.PriceScopeKey, ModelID: row.ModelID}
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
	return request.Input.nanoUSD == nil &&
		request.Output.nanoUSD == nil &&
		request.CacheRead.nanoUSD == nil &&
		request.CacheWrite.nanoUSD == nil
}

func cloneModelPriceValue(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
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
	scope, err := parsePriceScopeKey(row.PriceScopeKey)
	if err != nil {
		return modelPriceListRecord{}, fmt.Errorf("decode persisted model price scope: %w", app_errors.ErrInternalServer)
	}
	rule, err := persistedPriceRule(row)
	if err != nil {
		return modelPriceListRecord{}, fmt.Errorf("decode persisted model price: %w", app_errors.ErrInternalServer)
	}
	if _, err := pricing.NewTable([]pricing.Rule{rule}); err != nil {
		return modelPriceListRecord{}, fmt.Errorf("validate persisted model price: %w", app_errors.ErrInternalServer)
	}
	identity := pricing.Identity{ScopeKey: row.PriceScopeKey, ModelID: row.ModelID}
	reference := references.references[identity]
	status := resolvePricingStatus(&row)
	prices := PriceSlotsDTO{
		Input:      modelPriceWireDecimal(row.InputPriceNanoUSDPerMillionTokens),
		Output:     modelPriceWireDecimal(row.OutputPriceNanoUSDPerMillionTokens),
		CacheRead:  modelPriceWireDecimal(row.CacheReadPriceNanoUSDPerMillionTokens),
		CacheWrite: modelPriceWireDecimal(row.CacheWritePriceNanoUSDPerMillionTokens),
	}
	configuredSlots := modelPriceConfiguredSlotCount(row)
	dto := ModelPriceDTO{
		ID: row.ID, ModelID: row.ModelID,
		Scope:               projectPriceScope(scope, references.groupLabels, catalogSnapshot),
		Prices:              prices,
		PricingStatus:       status,
		Method:              modelPriceMethod(row, scope, configuredSlots),
		Referenced:          reference.referenceCount > 0,
		ReferenceCount:      reference.referenceCount,
		ReferenceGroupCount: reference.referenceGroupCount(),
		HasContextTiers:     len(rule.ContextTiers) > 0,
		Partial:             configuredSlots > 0 && configuredSlots < 4,
		UpdatedAtMS:         row.UpdatedAtMS,
		CanReset:            row.IsManual,
		CanDelete:           row.IsManual && reference.referenceCount == 0,
	}
	return modelPriceListRecord{dto: dto, scopeKey: row.PriceScopeKey}, nil
}

func projectPriceScope(
	scope parsedPriceScope,
	groupLabels map[uint]string,
	catalogSnapshot *catalog.Snapshot,
) PriceScopeDTO {
	label := scope.id
	if scope.kind == priceScopeKindProvider {
		if catalogSnapshot != nil {
			if provider, exists := catalogSnapshot.Providers[scope.id]; exists && strings.TrimSpace(provider.Name) != "" {
				label = provider.Name
			}
		}
	} else if groupLabel, exists := groupLabels[scope.groupID]; exists {
		label = groupLabel
	} else {
		label = "#" + scope.id
	}
	return PriceScopeDTO{Kind: scope.kind, ID: scope.id, Label: label}
}

func modelPriceMethod(
	row models.ModelPrice,
	scope parsedPriceScope,
	configuredSlots int,
) *string {
	if !row.IsManual && configuredSlots == 0 {
		return nil
	}
	method := "auto_sync"
	if row.IsManual && configuredSlots == 0 {
		method = "user_marked_unpriced"
	} else if row.IsManual && scope.kind == priceScopeKindProvider {
		method = "user_override"
	} else if row.IsManual {
		method = "user_set"
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

func modelPriceWireDecimal(value *int64) *string {
	if value == nil {
		return nil
	}
	formatted := pricing.FormatUSD(pricing.NanoUSD(*value))
	return &formatted
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
		accessKeyCollectionContainsFold(record.dto.Scope.ID, query.Search) ||
		accessKeyCollectionContainsFold(record.dto.Scope.Label, query.Search)
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
		if left.scopeKey != right.scopeKey {
			return left.scopeKey < right.scopeKey
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
