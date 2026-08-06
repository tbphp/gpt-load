package control

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/storage/models"
)

type ProjectModelGroupDTO struct {
	ID         uint                `json:"id"`
	Name       string              `json:"name"`
	ProviderID *string             `json:"provider_id"`
	Enabled    bool                `json:"enabled"`
	Protocols  []protocol.Protocol `json:"protocols"`
}

type ProjectModelCapabilitiesDTO struct {
	Attachment       *bool `json:"attachment"`
	Reasoning        *bool `json:"reasoning"`
	ToolCall         *bool `json:"tool_call"`
	StructuredOutput *bool `json:"structured_output"`
	Temperature      *bool `json:"temperature"`
}

type ProjectModelModalitiesDTO struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

type ProjectModelLimitsDTO struct {
	Context *int64 `json:"context"`
	Input   *int64 `json:"input"`
	Output  *int64 `json:"output"`
}

type ProjectModelCatalogDTO struct {
	ID           string                      `json:"id"`
	Name         string                      `json:"name"`
	Description  string                      `json:"description"`
	Family       string                      `json:"family"`
	Capabilities ProjectModelCapabilitiesDTO `json:"capabilities"`
	Modalities   ProjectModelModalitiesDTO   `json:"modalities"`
	Limits       ProjectModelLimitsDTO       `json:"limits"`
	Knowledge    string                      `json:"knowledge"`
	ReleaseDate  string                      `json:"release_date"`
	LastUpdated  string                      `json:"last_updated"`
	OpenWeights  *bool                       `json:"open_weights"`
	Status       string                      `json:"status"`
}

type ProjectModelCatalogReferenceDTO struct {
	Source       string                 `json:"source"`
	ProviderID   string                 `json:"provider_id"`
	ProviderName string                 `json:"provider_name"`
	Model        ProjectModelCatalogDTO `json:"model"`
}

type ProjectModelPriceDTO struct {
	Price            ModelPriceDTO                    `json:"price"`
	RouteGroups      []ProjectModelGroupDTO           `json:"route_groups"`
	AffectedGroups   []ProjectModelGroupDTO           `json:"affected_groups"`
	CatalogReference *ProjectModelCatalogReferenceDTO `json:"catalog_reference"`
}

type ProjectUpstreamModelDTO struct {
	ModelID        string                           `json:"model_id"`
	AliasApplied   bool                             `json:"alias_applied"`
	CatalogSummary *ProjectModelCatalogReferenceDTO `json:"catalog_summary"`
	Prices         []ProjectModelPriceDTO           `json:"prices"`
}

type ProjectModelDTO struct {
	ClientModel    string                    `json:"client_model"`
	Protocols      []protocol.Protocol       `json:"protocols"`
	UpstreamModels []ProjectUpstreamModelDTO `json:"upstream_models"`
}

type ProjectModelSummaryDTO struct {
	ClientModelCount       int `json:"client_model_count"`
	UpstreamModelCount     int `json:"upstream_model_count"`
	PriceCount             int `json:"price_count"`
	PendingPriceCount      int `json:"pending_price_count"`
	UnreferencedPriceCount int `json:"unreferenced_price_count"`
}

type ProjectModelCatalogStatusDTO struct {
	Available           bool   `json:"available"`
	CheckedAtMS         int64  `json:"checked_at_ms"`
	SuccessfulFetchAtMS int64  `json:"successful_fetch_at_ms"`
	ErrorCode           string `json:"error_code"`
}

type ProjectModelPaginationDTO struct {
	Page       int64 `json:"page"`
	PageSize   int64 `json:"page_size"`
	TotalItems int64 `json:"total_items"`
	TotalPages int64 `json:"total_pages"`
}

type ProjectModelListResponse struct {
	Summary    ProjectModelSummaryDTO       `json:"summary"`
	Catalog    ProjectModelCatalogStatusDTO `json:"catalog"`
	Items      []ProjectModelDTO            `json:"items"`
	Pagination ProjectModelPaginationDTO    `json:"pagination"`
}

type projectModelGroupRecord struct {
	row    models.Group
	dto    ProjectModelGroupDTO
	models []GroupModel
}

type projectModelPriceRecord struct {
	dto       ProjectModelPriceDTO
	identity  pricing.Identity
	groupSeen map[uint]struct{}
}

type projectModelUpstreamRecord struct {
	modelID      string
	aliasApplied bool
	prices       map[pricing.Identity]*projectModelPriceRecord
}

type projectModelRecord struct {
	clientModel string
	protocols   []protocol.Protocol
	upstreams   map[string]*projectModelUpstreamRecord
}

func (s *Service) ListProjectModels(ctx context.Context, query ProjectModelListQuery) (ProjectModelListResponse, error) {
	if err := validateProjectModelListQuery(query); err != nil {
		return ProjectModelListResponse{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ProjectModelListResponse{}, err
	}

	s.writeMu.RLock()
	var catalogSnapshot *catalog.Snapshot
	if s.catalogRuntime != nil {
		catalogSnapshot = s.catalogRuntime.Load()
	}
	var groups []models.Group
	var prices []models.ModelPrice
	err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		if err := tx.Order("id ASC").Find(&groups).Error; err != nil {
			return fmt.Errorf("load groups for model collection: %w", app_errors.ParseDBError(err))
		}
		if err := tx.Order("id ASC").Find(&prices).Error; err != nil {
			return fmt.Errorf("load model prices for model collection: %w", app_errors.ParseDBError(err))
		}
		groups = cloneGroupRows(groups)
		return nil
	})
	s.writeMu.RUnlock()
	if parentErr := ctx.Err(); parentErr != nil {
		return ProjectModelListResponse{}, parentErr
	}
	if err != nil {
		return ProjectModelListResponse{}, err
	}

	references, err := buildPriceReferenceSnapshot(groups)
	if err != nil {
		return ProjectModelListResponse{}, err
	}
	groupRecords, groupDTOs, err := projectModelGroups(groups)
	if err != nil {
		return ProjectModelListResponse{}, err
	}
	priceRecords := make(map[pricing.Identity]modelPriceListRecord, len(prices))
	for _, row := range prices {
		identity := pricing.Identity{ScopeKey: row.PriceScopeKey, ModelID: row.ModelID}
		if _, duplicate := priceRecords[identity]; duplicate {
			return ProjectModelListResponse{}, fmt.Errorf("duplicate persisted price identity: %w", app_errors.ErrInternalServer)
		}
		record, err := projectModelPriceRow(row, references, catalogSnapshot)
		if err != nil {
			return ProjectModelListResponse{}, err
		}
		priceRecords[identity] = record
	}

	records := make(map[string]*projectModelRecord)
	for _, group := range groupRecords {
		if !projectModelGroupVisible(group.row, query.GroupStatus) {
			continue
		}
		for _, model := range group.models {
			if err := ctx.Err(); err != nil {
				return ProjectModelListResponse{}, err
			}
			identity, err := PriceIdentityForGroup(group.row, model.ID)
			if err != nil {
				return ProjectModelListResponse{}, fmt.Errorf("validate group %d model %q: %w", group.row.ID, model.ID, app_errors.ErrInternalServer)
			}
			priceRecord, exists := priceRecords[identity]
			if !exists {
				return ProjectModelListResponse{}, fmt.Errorf("missing model price row for %s/%s: %w", identity.ScopeKey, identity.ModelID, app_errors.ErrInternalServer)
			}
			clientModel := model.ID
			if strings.TrimSpace(model.Alias) != "" {
				clientModel = model.Alias
			}
			root := records[clientModel]
			if root == nil {
				root = &projectModelRecord{
					clientModel: clientModel,
					protocols:   []protocol.Protocol{},
					upstreams:   make(map[string]*projectModelUpstreamRecord),
				}
				records[clientModel] = root
			}
			root.protocols = mergeProjectModelProtocols(root.protocols, group.dto.Protocols)
			upstream := root.upstreams[model.ID]
			if upstream == nil {
				upstream = &projectModelUpstreamRecord{
					modelID:      model.ID,
					aliasApplied: strings.TrimSpace(model.Alias) != "",
					prices:       make(map[pricing.Identity]*projectModelPriceRecord),
				}
				root.upstreams[model.ID] = upstream
			}
			branch := upstream.prices[identity]
			if branch == nil {
				affectedGroups, err := projectModelAffectedGroups(identity, references, groupDTOs)
				if err != nil {
					return ProjectModelListResponse{}, err
				}
				branch = &projectModelPriceRecord{
					dto: ProjectModelPriceDTO{
						Price:            priceRecord.dto,
						RouteGroups:      []ProjectModelGroupDTO{},
						AffectedGroups:   affectedGroups,
						CatalogReference: projectModelCatalogReference(identity, priceRecord.dto, model.ID, catalogSnapshot),
					},
					identity:  identity,
					groupSeen: make(map[uint]struct{}),
				}
				upstream.prices[identity] = branch
			}
			if _, duplicate := branch.groupSeen[group.row.ID]; !duplicate {
				branch.groupSeen[group.row.ID] = struct{}{}
				branch.dto.RouteGroups = append(branch.dto.RouteGroups, group.dto)
			}
		}
	}

	summary := projectModelSummary(records, priceRecords)
	result := make([]ProjectModelDTO, 0, len(records))
	for _, record := range records {
		dto := ProjectModelDTO{
			ClientModel:    record.clientModel,
			Protocols:      append([]protocol.Protocol(nil), record.protocols...),
			UpstreamModels: []ProjectUpstreamModelDTO{},
		}
		for _, upstream := range record.upstreams {
			upstreamDTO := ProjectUpstreamModelDTO{
				ModelID: upstream.modelID, AliasApplied: upstream.aliasApplied,
				Prices: []ProjectModelPriceDTO{},
			}
			for _, branch := range upstream.prices {
				if !projectModelPricingVisible(branch.dto.Price.PricingStatus, query.PricingStatus) {
					continue
				}
				sortProjectModelGroups(branch.dto.RouteGroups)
				upstreamDTO.Prices = append(upstreamDTO.Prices, branch.dto)
			}
			sort.SliceStable(upstreamDTO.Prices, func(left, right int) bool {
				return projectModelPriceSortKey(upstreamDTO.Prices[left]) < projectModelPriceSortKey(upstreamDTO.Prices[right])
			})
			if len(upstreamDTO.Prices) == 0 {
				continue
			}
			upstreamDTO.CatalogSummary = projectModelCatalogSummary(upstreamDTO.Prices)
			dto.UpstreamModels = append(dto.UpstreamModels, upstreamDTO)
		}
		sort.SliceStable(dto.UpstreamModels, func(left, right int) bool {
			return dto.UpstreamModels[left].ModelID < dto.UpstreamModels[right].ModelID
		})
		if len(dto.UpstreamModels) == 0 || !projectModelSearchMatch(dto, query.Search) {
			continue
		}
		result = append(result, dto)
	}
	sort.SliceStable(result, func(left, right int) bool { return result[left].ClientModel < result[right].ClientModel })
	totalItems := int64(len(result))
	start := (query.Page - 1) * query.PageSize
	end := start + query.PageSize
	if start > totalItems {
		start = totalItems
	}
	if end > totalItems {
		end = totalItems
	}
	items := append([]ProjectModelDTO{}, result[start:end]...)
	catalogStatus := ProjectModelCatalogStatusDTO{Available: catalogSnapshot != nil}
	if s.catalogSync != nil {
		status := s.catalogSync.readStatus()
		catalogStatus.CheckedAtMS = status.CheckedAtMS
		catalogStatus.SuccessfulFetchAtMS = status.SuccessfulFetchAtMS
		catalogStatus.ErrorCode = status.ErrorCode
	}
	return ProjectModelListResponse{
		Summary: summary,
		Catalog: catalogStatus,
		Items:   items,
		Pagination: ProjectModelPaginationDTO{
			Page: query.Page, PageSize: query.PageSize, TotalItems: totalItems,
			TotalPages: projectModelTotalPages(totalItems, query.PageSize),
		},
	}, nil
}

func projectModelGroups(groups []models.Group) ([]projectModelGroupRecord, map[uint]ProjectModelGroupDTO, error) {
	records := make([]projectModelGroupRecord, 0, len(groups))
	dtos := make(map[uint]ProjectModelGroupDTO, len(groups))
	for _, group := range groups {
		protocols, err := projectModelGroupProtocols(group)
		if err != nil {
			return nil, nil, err
		}
		var groupModels []GroupModel
		if err := decodeGroupDiscoveryJSON(group.Models, &groupModels); err != nil {
			return nil, nil, fmt.Errorf("decode group %d models: %w", group.ID, app_errors.ErrInternalServer)
		}
		seenModelIDs := make(map[string]struct{}, len(groupModels))
		for _, model := range groupModels {
			if _, duplicate := seenModelIDs[model.ID]; duplicate {
				return nil, nil, fmt.Errorf("duplicate Group %d model %q: %w", group.ID, model.ID, app_errors.ErrInternalServer)
			}
			seenModelIDs[model.ID] = struct{}{}
		}
		dto := ProjectModelGroupDTO{
			ID: group.ID, Name: group.Name, ProviderID: cloneString(group.ProviderID), Enabled: group.Enabled,
			Protocols: append([]protocol.Protocol(nil), protocols...),
		}
		dtos[group.ID] = dto
		records = append(records, projectModelGroupRecord{row: group, dto: dto, models: groupModels})
	}
	return records, dtos, nil
}

func projectModelAffectedGroups(
	identity pricing.Identity,
	references priceReferenceSnapshot,
	groups map[uint]ProjectModelGroupDTO,
) ([]ProjectModelGroupDTO, error) {
	reference, exists := references.references[identity]
	if !exists || len(reference.groupIDs) == 0 {
		return nil, fmt.Errorf("missing model price references for %s/%s: %w", identity.ScopeKey, identity.ModelID, app_errors.ErrInternalServer)
	}
	result := make([]ProjectModelGroupDTO, 0, len(reference.groupIDs))
	for groupID := range reference.groupIDs {
		group, exists := groups[groupID]
		if !exists {
			return nil, fmt.Errorf("missing Group %d for model price reference: %w", groupID, app_errors.ErrInternalServer)
		}
		result = append(result, group)
	}
	sortProjectModelGroups(result)
	return result, nil
}

func projectModelSummary(
	records map[string]*projectModelRecord,
	prices map[pricing.Identity]modelPriceListRecord,
) ProjectModelSummaryDTO {
	summary := ProjectModelSummaryDTO{ClientModelCount: len(records)}
	seenPrices := make(map[uint]ModelPriceDTO)
	for _, record := range records {
		summary.UpstreamModelCount += len(record.upstreams)
		for _, upstream := range record.upstreams {
			for _, branch := range upstream.prices {
				seenPrices[branch.dto.Price.ID] = branch.dto.Price
			}
		}
	}
	summary.PriceCount = len(seenPrices)
	for _, price := range seenPrices {
		if price.PricingStatus == PricingStatusPending {
			summary.PendingPriceCount++
		}
	}
	for _, price := range prices {
		if !price.dto.Referenced {
			summary.UnreferencedPriceCount++
		}
	}
	return summary
}

func projectModelGroupVisible(group models.Group, status ProjectModelGroupStatus) bool {
	return status == ProjectModelGroupStatusAll || group.Enabled
}

func projectModelPricingVisible(status PricingStatus, filter ProjectModelPricingStatus) bool {
	return filter == ProjectModelPricingStatusAll || (filter == ProjectModelPricingStatusPending && status == PricingStatusPending) || (filter == ProjectModelPricingStatusConfigured && status == PricingStatusConfigured)
}

func projectModelGroupProtocols(group models.Group) ([]protocol.Protocol, error) {
	var values []protocol.Protocol
	if err := decodeGroupDiscoveryJSON(group.Protocols, &values); err != nil {
		return nil, fmt.Errorf("decode group %d protocols: %w", group.ID, app_errors.ErrInternalServer)
	}
	result := mergeProjectModelProtocols(nil, values)
	if len(result) == 0 {
		return nil, fmt.Errorf("group %d has no valid protocols: %w", group.ID, app_errors.ErrInternalServer)
	}
	return result, nil
}

func mergeProjectModelProtocols(left, right []protocol.Protocol) []protocol.Protocol {
	seen := make(map[protocol.Protocol]struct{}, len(left)+len(right))
	for _, value := range left {
		seen[value] = struct{}{}
	}
	for _, value := range right {
		seen[value] = struct{}{}
	}
	result := make([]protocol.Protocol, 0, len(seen))
	for _, value := range protocol.DataPlaneProtocols() {
		if _, ok := seen[value]; ok {
			result = append(result, value)
		}
	}
	return result
}

func projectModelCatalogReference(
	identity pricing.Identity,
	price ModelPriceDTO,
	modelID string,
	snapshot *catalog.Snapshot,
) *ProjectModelCatalogReferenceDTO {
	if snapshot == nil {
		return nil
	}
	lookup := func(providerID, source string) *ProjectModelCatalogReferenceDTO {
		provider, exists := snapshot.Providers[providerID]
		if !exists {
			return nil
		}
		model, exists := provider.Models[modelID]
		if !exists {
			return nil
		}
		providerName := strings.TrimSpace(provider.Name)
		if providerName == "" {
			providerName = providerID
		}
		return &ProjectModelCatalogReferenceDTO{
			Source: source, ProviderID: providerID, ProviderName: providerName,
			Model: projectModelCatalogModel(model),
		}
	}

	tested := make(map[string]struct{})
	if scope, err := parsePriceScopeKey(identity.ScopeKey); err == nil && scope.kind == priceScopeKindProvider {
		tested[scope.id] = struct{}{}
		if reference := lookup(scope.id, "actual_provider"); reference != nil {
			return reference
		}
	}
	if price.MatchedProviderID != nil {
		providerID := *price.MatchedProviderID
		tested[providerID] = struct{}{}
		if reference := lookup(providerID, "reference_provider"); reference != nil {
			return reference
		}
	}
	for _, providerID := range catalogProviderLookupOrder(snapshot, "") {
		if _, alreadyTested := tested[providerID]; alreadyTested {
			continue
		}
		if reference := lookup(providerID, "reference_provider"); reference != nil {
			return reference
		}
	}
	return nil
}

func projectModelCatalogModel(model catalog.Model) ProjectModelCatalogDTO {
	metadata := model.Metadata
	name := strings.TrimSpace(model.Name)
	if name == "" {
		name = model.ID
	}
	return ProjectModelCatalogDTO{
		ID: model.ID, Name: name, Description: metadata.Description, Family: metadata.Family,
		Capabilities: ProjectModelCapabilitiesDTO{
			Attachment: cloneProjectBool(metadata.Capabilities.Attachment), Reasoning: cloneProjectBool(metadata.Capabilities.Reasoning),
			ToolCall: cloneProjectBool(metadata.Capabilities.ToolCall), StructuredOutput: cloneProjectBool(metadata.Capabilities.StructuredOutput),
			Temperature: cloneProjectBool(metadata.Capabilities.Temperature),
		},
		Modalities: ProjectModelModalitiesDTO{Input: append([]string{}, metadata.Modalities.Input...), Output: append([]string{}, metadata.Modalities.Output...)},
		Limits: ProjectModelLimitsDTO{
			Context: cloneProjectInt64(metadata.Limits.Context), Input: cloneProjectInt64(metadata.Limits.Input), Output: cloneProjectInt64(metadata.Limits.Output),
		},
		Knowledge: metadata.Knowledge, ReleaseDate: metadata.ReleaseDate, LastUpdated: metadata.LastUpdated,
		OpenWeights: cloneProjectBool(metadata.OpenWeights), Status: metadata.Status,
	}
}

func projectModelCatalogSummary(prices []ProjectModelPriceDTO) *ProjectModelCatalogReferenceDTO {
	if len(prices) == 0 || prices[0].CatalogReference == nil {
		return nil
	}
	first := prices[0].CatalogReference
	for _, price := range prices[1:] {
		candidate := price.CatalogReference
		if candidate == nil || candidate.Source != first.Source || candidate.ProviderID != first.ProviderID || candidate.Model.ID != first.Model.ID {
			return nil
		}
	}
	result := *first
	return &result
}

func cloneProjectBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneProjectInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func projectModelPriceSortKey(branch ProjectModelPriceDTO) string {
	return branch.Price.Scope.Kind + ":" + branch.Price.Scope.ID + ":" + branch.Price.ModelID
}

func sortProjectModelGroups(groups []ProjectModelGroupDTO) {
	sort.SliceStable(groups, func(left, right int) bool {
		if groups[left].Name != groups[right].Name {
			return groups[left].Name < groups[right].Name
		}
		return groups[left].ID < groups[right].ID
	})
}

func projectModelSearchMatch(record ProjectModelDTO, search string) bool {
	search = strings.ToLower(strings.TrimSpace(search))
	if search == "" {
		return true
	}
	if strings.Contains(strings.ToLower(record.ClientModel), search) {
		return true
	}
	for _, upstream := range record.UpstreamModels {
		if strings.Contains(strings.ToLower(upstream.ModelID), search) {
			return true
		}
		for _, branch := range upstream.Prices {
			price := branch.Price
			if strings.Contains(strings.ToLower(price.Scope.ID), search) || strings.Contains(strings.ToLower(price.Scope.Label), search) {
				return true
			}
			if reference := branch.CatalogReference; reference != nil {
				if strings.Contains(strings.ToLower(reference.Model.Name), search) ||
					strings.Contains(strings.ToLower(reference.ProviderID), search) ||
					strings.Contains(strings.ToLower(reference.ProviderName), search) {
					return true
				}
			}
			for _, group := range branch.RouteGroups {
				if strings.Contains(strings.ToLower(group.Name), search) {
					return true
				}
			}
		}
	}
	return false
}

func projectModelTotalPages(total, pageSize int64) int64 {
	if total == 0 {
		return 0
	}
	return (total + pageSize - 1) / pageSize
}
