package control

import (
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
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type ProjectModelGroupDTO struct {
	ID              uint                `json:"id"`
	Name            string              `json:"name"`
	ChannelID       channel.ID          `json:"channel_id"`
	Params          json.RawMessage     `json:"params"`
	Enabled         bool                `json:"enabled"`
	ClientProtocols []protocol.Protocol `json:"client_protocols"`
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

type ProjectUpstreamModelDTO struct {
	ModelID          string                           `json:"model_id"`
	AliasApplied     bool                             `json:"alias_applied"`
	Price            ModelPriceDTO                    `json:"price"`
	RouteGroups      []ProjectModelGroupDTO           `json:"route_groups"`
	AffectedGroups   []ProjectModelGroupDTO           `json:"affected_groups"`
	CatalogReference *ProjectModelCatalogReferenceDTO `json:"catalog_reference"`
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

type UpstreamModelAssociationDTO struct {
	ClientModel  string               `json:"client_model"`
	AliasApplied bool                 `json:"alias_applied"`
	Group        ProjectModelGroupDTO `json:"group"`
}

// UpstreamModelDetailDTO is the complete, unfiltered relationship view for a
// price editor. It deliberately does not inherit the Models list's page,
// search, enabled-group, or pricing-status filters.
type UpstreamModelDetailDTO struct {
	ModelID          string                           `json:"model_id"`
	Price            ModelPriceDTO                    `json:"price"`
	CatalogReference *ProjectModelCatalogReferenceDTO `json:"catalog_reference"`
	Associations     []UpstreamModelAssociationDTO    `json:"associations"`
	ClientModelCount int                              `json:"client_model_count"`
	GroupCount       int                              `json:"group_count"`
}

type projectModelGroupRecord struct {
	row    models.Group
	dto    ProjectModelGroupDTO
	models []GroupModel
}

type projectModelUpstreamRecord struct {
	modelID          string
	aliasApplied     bool
	price            ModelPriceDTO
	routeGroups      []ProjectModelGroupDTO
	affectedGroups   []ProjectModelGroupDTO
	catalogReference *ProjectModelCatalogReferenceDTO
	groupSeen        map[uint]struct{}
}

type projectModelRecord struct {
	clientModel string
	protocols   []protocol.Protocol
	upstreams   map[pricing.Identity]*projectModelUpstreamRecord
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
	protocolOverrides := map[uint][]protocol.Protocol(nil)
	if err == nil && query.AccessKeyID != nil {
		if s.manager == nil {
			err = app_errors.ErrInternalServer
		} else {
			groups, protocolOverrides, err = scopeProjectModelGroups(
				groups,
				s.manager.Current(),
				*query.AccessKeyID,
				s.channelRegistry,
			)
		}
	}
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
	groupRecords, groupDTOs, err := projectModelGroups(groups, s.channelRegistry, protocolOverrides)
	if err != nil {
		return ProjectModelListResponse{}, err
	}
	priceRecords := make(map[pricing.Identity]modelPriceListRecord, len(prices))
	for _, row := range prices {
		identity, err := PriceIdentityForChannelModel(row.ChannelID, row.ModelID)
		if err != nil {
			return ProjectModelListResponse{}, fmt.Errorf("validate persisted price identity: %w", app_errors.ErrInternalServer)
		}
		if query.AccessKeyID != nil {
			if _, referenced := references.references[identity]; !referenced {
				continue
			}
		}
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
			identity, err := PriceIdentityForChannelModel(group.row.ChannelID, model.ID)
			if err != nil {
				return ProjectModelListResponse{}, fmt.Errorf("validate group %d model %q: %w", group.row.ID, model.ID, app_errors.ErrInternalServer)
			}
			priceRecord, exists := priceRecords[identity]
			if !exists {
				return ProjectModelListResponse{}, fmt.Errorf("missing model price row for %s: %w", identity.ModelID, app_errors.ErrInternalServer)
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
					upstreams:   make(map[pricing.Identity]*projectModelUpstreamRecord),
				}
				records[clientModel] = root
			}
			root.protocols = mergeProjectModelProtocols(root.protocols, group.dto.ClientProtocols)
			upstream := root.upstreams[identity]
			if upstream == nil {
				affectedGroups, err := projectModelAffectedGroups(identity, references, groupDTOs)
				if err != nil {
					return ProjectModelListResponse{}, err
				}
				upstream = &projectModelUpstreamRecord{
					modelID: model.ID, aliasApplied: strings.TrimSpace(model.Alias) != "",
					price: priceRecord.dto, affectedGroups: affectedGroups,
					catalogReference: projectModelCatalogReference(priceRecord.dto, identity, catalogSnapshot),
					groupSeen:        make(map[uint]struct{}),
				}
				root.upstreams[identity] = upstream
			}
			if _, duplicate := upstream.groupSeen[group.row.ID]; !duplicate {
				upstream.groupSeen[group.row.ID] = struct{}{}
				upstream.routeGroups = append(upstream.routeGroups, group.dto)
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
			if !projectModelPricingVisible(upstream.price.PricingStatus, query.PricingStatus) {
				continue
			}
			sortProjectModelGroups(upstream.routeGroups)
			upstreamDTO := ProjectUpstreamModelDTO{
				ModelID: upstream.modelID, AliasApplied: upstream.aliasApplied,
				Price: upstream.price, RouteGroups: append([]ProjectModelGroupDTO{}, upstream.routeGroups...),
				AffectedGroups:   append([]ProjectModelGroupDTO{}, upstream.affectedGroups...),
				CatalogReference: upstream.catalogReference,
			}
			dto.UpstreamModels = append(dto.UpstreamModels, upstreamDTO)
		}
		sort.SliceStable(dto.UpstreamModels, func(left, right int) bool {
			if dto.UpstreamModels[left].ModelID != dto.UpstreamModels[right].ModelID {
				return dto.UpstreamModels[left].ModelID < dto.UpstreamModels[right].ModelID
			}
			return dto.UpstreamModels[left].Price.ChannelID < dto.UpstreamModels[right].Price.ChannelID
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

func scopeProjectModelGroups(
	groups []models.Group,
	snapshot *state.ConfigSnapshot,
	accessKeyID uint,
	registry *channel.Registry,
) ([]models.Group, map[uint][]protocol.Protocol, error) {
	if snapshot == nil || accessKeyID == 0 {
		return nil, nil, app_errors.ErrUnauthorized
	}
	accessKey, exists := snapshot.AccessKeysByID[accessKeyID]
	if !exists || accessKey.Status != state.AccessKeyStatusActive {
		return nil, nil, app_errors.ErrUnauthorized
	}
	result := make([]models.Group, 0, len(groups))
	protocolsByGroup := make(map[uint][]protocol.Protocol, len(groups))
	for _, group := range groups {
		if !group.Enabled {
			continue
		}
		if len(accessKey.Filters.Groups) > 0 {
			if _, allowed := accessKey.Filters.Groups[group.ID]; !allowed {
				continue
			}
		}
		protocols, err := projectModelGroupProtocols(registry, group)
		if err != nil {
			return nil, nil, err
		}
		filteredProtocols := make([]protocol.Protocol, 0, len(protocols))
		for _, value := range protocols {
			if len(accessKey.Filters.Protocols) > 0 {
				if _, allowed := accessKey.Filters.Protocols[value]; !allowed {
					continue
				}
			}
			filteredProtocols = append(filteredProtocols, value)
		}
		if len(filteredProtocols) == 0 {
			continue
		}
		var groupModels []GroupModel
		if err := decodeGroupDiscoveryJSON(group.Models, &groupModels); err != nil {
			return nil, nil, fmt.Errorf(
				"decode group %d models for access scope: %w",
				group.ID,
				app_errors.ErrInternalServer,
			)
		}
		filteredModels := make([]GroupModel, 0, len(groupModels))
		for _, model := range groupModels {
			clientModel := strings.TrimSpace(model.Alias)
			if clientModel == "" {
				clientModel = strings.TrimSpace(model.ID)
			}
			if len(accessKey.Filters.Models) > 0 {
				if _, allowed := accessKey.Filters.Models[clientModel]; !allowed {
					continue
				}
			}
			filteredModels = append(filteredModels, model)
		}
		if len(filteredModels) == 0 {
			continue
		}
		modelJSON, err := json.Marshal(filteredModels)
		if err != nil {
			return nil, nil, fmt.Errorf("encode scoped group models: %w", err)
		}
		group.Models = models.JSON(modelJSON)
		protocolsByGroup[group.ID] = append([]protocol.Protocol(nil), filteredProtocols...)
		result = append(result, group)
	}
	return result, protocolsByGroup, nil
}

func (s *Service) GetUpstreamModelDetail(ctx context.Context, priceID uint) (UpstreamModelDetailDTO, error) {
	if priceID == 0 || uint64(priceID) > uint64(maxSafeInteger) {
		return UpstreamModelDetailDTO{}, app_errors.ErrBadRequest
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
	var row models.ModelPrice
	var groups []models.Group
	if err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		if err := tx.First(&row, priceID).Error; err != nil {
			return fmt.Errorf("load model price detail: %w", app_errors.ParseDBError(err))
		}
		if err := tx.Order("id ASC").Find(&groups).Error; err != nil {
			return fmt.Errorf("load groups for model detail: %w", app_errors.ParseDBError(err))
		}
		groups = cloneGroupRows(groups)
		return nil
	}); err != nil {
		return UpstreamModelDetailDTO{}, err
	}
	references, err := buildPriceReferenceSnapshot(groups)
	if err != nil {
		return UpstreamModelDetailDTO{}, err
	}
	price, err := projectModelPriceRow(row, references, catalogSnapshot)
	if err != nil {
		return UpstreamModelDetailDTO{}, err
	}
	groupRecords, _, err := projectModelGroups(groups, s.channelRegistry, nil)
	if err != nil {
		return UpstreamModelDetailDTO{}, err
	}
	associations := make([]UpstreamModelAssociationDTO, 0)
	clientModels := make(map[string]struct{})
	groupIDs := make(map[uint]struct{})
	identity, err := PriceIdentityForChannelModel(row.ChannelID, row.ModelID)
	if err != nil {
		return UpstreamModelDetailDTO{}, fmt.Errorf("validate model detail price identity: %w", app_errors.ErrInternalServer)
	}
	for _, group := range groupRecords {
		if group.row.ChannelID != row.ChannelID {
			continue
		}
		for _, model := range group.models {
			if model.ID != row.ModelID {
				continue
			}
			clientModel := model.ID
			aliasApplied := strings.TrimSpace(model.Alias) != ""
			if aliasApplied {
				clientModel = model.Alias
			}
			associations = append(associations, UpstreamModelAssociationDTO{
				ClientModel: clientModel, AliasApplied: aliasApplied, Group: group.dto,
			})
			clientModels[clientModel] = struct{}{}
			groupIDs[group.row.ID] = struct{}{}
		}
	}
	sort.SliceStable(associations, func(left, right int) bool {
		if associations[left].ClientModel != associations[right].ClientModel {
			return associations[left].ClientModel < associations[right].ClientModel
		}
		if associations[left].Group.Name != associations[right].Group.Name {
			return associations[left].Group.Name < associations[right].Group.Name
		}
		return associations[left].Group.ID < associations[right].Group.ID
	})
	return UpstreamModelDetailDTO{
		ModelID: row.ModelID, Price: price.dto,
		CatalogReference: projectModelCatalogReference(price.dto, identity, catalogSnapshot),
		Associations:     associations, ClientModelCount: len(clientModels), GroupCount: len(groupIDs),
	}, nil
}

func projectModelGroups(
	groups []models.Group,
	registry *channel.Registry,
	protocolOverrides map[uint][]protocol.Protocol,
) ([]projectModelGroupRecord, map[uint]ProjectModelGroupDTO, error) {
	records := make([]projectModelGroupRecord, 0, len(groups))
	dtos := make(map[uint]ProjectModelGroupDTO, len(groups))
	for _, group := range groups {
		protocols, overridden := protocolOverrides[group.ID]
		var err error
		if !overridden {
			protocols, err = projectModelGroupProtocols(registry, group)
		}
		if err != nil {
			return nil, nil, err
		}
		if len(protocols) == 0 {
			return nil, nil, fmt.Errorf("group %d has no client protocols: %w", group.ID, app_errors.ErrInternalServer)
		}
		if registry == nil {
			return nil, nil, app_errors.ErrInternalServer
		}
		params, err := registry.ValidateParams(channel.ID(group.ChannelID), json.RawMessage(group.Params))
		if err != nil {
			return nil, nil, fmt.Errorf("validate group %d channel params: %w", group.ID, app_errors.ErrInternalServer)
		}
		var groupModels []GroupModel
		if err := decodeGroupDiscoveryJSON(group.Models, &groupModels); err != nil {
			return nil, nil, fmt.Errorf("decode group %d models: %w", group.ID, app_errors.ErrInternalServer)
		}
		dto := ProjectModelGroupDTO{
			ID: group.ID, Name: group.Name, ChannelID: channel.ID(group.ChannelID),
			Params: params.CanonicalJSON(), Enabled: group.Enabled,
			ClientProtocols: append([]protocol.Protocol(nil), protocols...),
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
		return nil, fmt.Errorf(
			"missing model price references for %s/%s: %w",
			identity.ChannelID,
			identity.ModelID,
			app_errors.ErrInternalServer,
		)
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
			seenPrices[upstream.price.ID] = upstream.price
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

func projectModelGroupProtocols(registry *channel.Registry, group models.Group) ([]protocol.Protocol, error) {
	if registry == nil || group.ChannelID == "" {
		return nil, fmt.Errorf("group %d has no channel: %w", group.ID, app_errors.ErrInternalServer)
	}
	descriptor, ok := registry.Get(channel.ID(group.ChannelID))
	if !ok {
		return nil, fmt.Errorf("group %d has unknown channel: %w", group.ID, app_errors.ErrInternalServer)
	}
	if _, err := registry.ValidateParams(channel.ID(group.ChannelID), json.RawMessage(group.Params)); err != nil {
		return nil, fmt.Errorf("group %d has invalid channel params: %w", group.ID, app_errors.ErrInternalServer)
	}
	result := mergeProjectModelProtocols(nil, descriptor.ClientProtocols)
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
	price ModelPriceDTO,
	identity pricing.Identity,
	snapshot *catalog.Snapshot,
) *ProjectModelCatalogReferenceDTO {
	if snapshot == nil {
		return nil
	}
	var model catalog.Model
	providerID := ""
	if price.MatchedProviderID != nil {
		providerID = *price.MatchedProviderID
		provider, exists := snapshot.Providers[providerID]
		if exists {
			model, exists = provider.Models[identity.ModelID]
			if !exists {
				providerID = ""
			}
		} else {
			providerID = ""
		}
	}
	if providerID == "" {
		var ok bool
		model, providerID, _, ok = resolveCatalogModelForIdentity(snapshot, identity, false)
		if !ok {
			return nil
		}
	}
	provider := snapshot.Providers[providerID]
	providerName := strings.TrimSpace(provider.Name)
	if providerName == "" {
		providerName = providerID
	}
	return &ProjectModelCatalogReferenceDTO{
		Source: "reference_provider", ProviderID: providerID, ProviderName: providerName,
		Model: projectModelCatalogModel(model),
	}
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
		if strings.Contains(strings.ToLower(upstream.Price.ChannelID), search) {
			return true
		}
		if reference := upstream.CatalogReference; reference != nil {
			if strings.Contains(strings.ToLower(reference.Model.Name), search) ||
				strings.Contains(strings.ToLower(reference.ProviderID), search) ||
				strings.Contains(strings.ToLower(reference.ProviderName), search) {
				return true
			}
		}
		for _, group := range upstream.RouteGroups {
			if strings.Contains(strings.ToLower(group.Name), search) {
				return true
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
