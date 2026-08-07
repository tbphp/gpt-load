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

const (
	providerSuggestionCatalogLimit = 25
	providerModelResultLimit       = 100
)

// ProviderSuggestionSource identifies which directory a provider suggestion
// came from. Suggestions are merged official-first, then curated, then
// catalog, and deduplicated by provider ID so a higher-priority source's
// metadata always wins for a shared ID.
type ProviderSuggestionSource string

const (
	ProviderSuggestionSourceOfficial ProviderSuggestionSource = "official"
	ProviderSuggestionSourceCurated  ProviderSuggestionSource = "curated"
	ProviderSuggestionSourceCatalog  ProviderSuggestionSource = "catalog"
)

type ProviderSuggestion struct {
	ProviderID string                   `json:"provider_id"`
	Name       string                   `json:"name"`
	APIURL     string                   `json:"api_url,omitempty"`
	Protocols  []protocol.Protocol      `json:"protocols"`
	Mark       string                   `json:"mark"`
	Source     ProviderSuggestionSource `json:"source"`
}

type ProviderSuggestionListResponse struct {
	Items []ProviderSuggestion `json:"items"`
	Total int                  `json:"total"`
}

type ModelCandidate struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Sources       []string      `json:"sources"`
	PricingStatus PricingStatus `json:"pricing_status"`
	PricingSource *string       `json:"pricing_source"`
}

type ProviderModelQuery struct {
	Query  string
	Status PricingStatus
}

type ProviderModelListResponse struct {
	Items []ModelCandidate `json:"items"`
	Total int              `json:"total"`
}

func (s *Service) ListProviderSuggestions(
	ctx context.Context,
	query string,
) (ProviderSuggestionListResponse, error) {
	if err := ctx.Err(); err != nil {
		return ProviderSuggestionListResponse{}, err
	}
	query = strings.TrimSpace(query)
	if len([]rune(query)) > 200 {
		return ProviderSuggestionListResponse{}, app_errors.ErrValidation
	}
	curated := catalog.SearchCurated(query)
	catalogOnly := s.catalogRuntime.SearchProviderMetadata(query, providerSuggestionCatalogLimit)
	items := make([]ProviderSuggestion, 0, 3+len(curated)+len(catalogOnly))
	seen := make(map[string]struct{}, 3+len(curated)+len(catalogOnly))

	appendSuggestions := func(providers []catalog.Provider, source ProviderSuggestionSource) {
		for _, provider := range providers {
			if _, duplicate := seen[provider.ID]; duplicate {
				continue
			}
			seen[provider.ID] = struct{}{}
			items = append(items, mapProviderSuggestion(provider, source))
		}
	}

	// Official first, then curated, then catalog: a shared provider ID keeps
	// the higher-priority source's display metadata (mapProviderSuggestion),
	// while pricing continues to key off the same provider ID either way.
	appendSuggestions(s.catalogRuntime.OfficialSuggestions(), ProviderSuggestionSourceOfficial)
	appendSuggestions(curated, ProviderSuggestionSourceCurated)
	appendSuggestions(catalogOnly, ProviderSuggestionSourceCatalog)

	return ProviderSuggestionListResponse{Items: items, Total: len(items)}, nil
}

func (s *Service) ListProviderSuggestionsByIDs(
	ctx context.Context,
	providerIDs []string,
) (ProviderSuggestionListResponse, error) {
	if err := ctx.Err(); err != nil {
		return ProviderSuggestionListResponse{}, err
	}
	providers := catalog.SearchProviderMetadataByIDs(s.catalogRuntime.Load(), providerIDs)
	items := make([]ProviderSuggestion, 0, len(providers))
	for _, provider := range providers {
		items = append(items, mapProviderSuggestion(provider, ProviderSuggestionSourceCatalog))
	}
	return ProviderSuggestionListResponse{Items: items, Total: len(items)}, nil
}

func mapProviderSuggestion(provider catalog.Provider, source ProviderSuggestionSource) ProviderSuggestion {
	protocols := make([]protocol.Protocol, len(provider.Protocols))
	copy(protocols, provider.Protocols)
	return ProviderSuggestion{
		ProviderID: provider.ID,
		Name:       provider.Name,
		APIURL:     provider.APIURL,
		Protocols:  protocols,
		Mark:       provider.Mark,
		Source:     source,
	}
}

func (s *Service) validateSelectableProviderID(providerID *string) error {
	if providerID == nil || catalog.IsOfficialProviderID(*providerID) || catalog.IsCuratedProviderID(*providerID) {
		return nil
	}
	if s.catalogRuntime != nil {
		if _, exists := s.catalogRuntime.LoadProvider(*providerID); exists {
			return nil
		}
	}
	return app_errors.ErrValidation
}

func (s *Service) ListProviderModels(
	ctx context.Context,
	providerID string,
	query ProviderModelQuery,
) (ProviderModelListResponse, error) {
	if _, err := pricing.ProviderScopeKey(providerID); err != nil {
		return ProviderModelListResponse{}, app_errors.ErrValidation
	}
	query.Query = strings.TrimSpace(query.Query)
	if len([]rune(query.Query)) > 200 ||
		(query.Status != "" && query.Status != PricingStatusPending && query.Status != PricingStatusConfigured) {
		return ProviderModelListResponse{}, app_errors.ErrValidation
	}

	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	snapshot := s.catalogRuntime.Load()
	if snapshot == nil {
		return ProviderModelListResponse{}, app_errors.ErrResourceNotFound
	}
	provider, exists := snapshot.Providers[providerID]
	if !exists {
		return ProviderModelListResponse{}, app_errors.ErrResourceNotFound
	}
	rows, err := loadModelPriceRows(ctx, s.db)
	if err != nil {
		return ProviderModelListResponse{}, err
	}

	normalizedQuery := strings.ToLower(query.Query)
	items := make([]ModelCandidate, 0, len(provider.Models))
	for _, model := range provider.Models {
		if normalizedQuery != "" &&
			!strings.Contains(strings.ToLower(model.ID), normalizedQuery) &&
			!strings.Contains(strings.ToLower(model.Name), normalizedQuery) {
			continue
		}
		row := rows[model.ID]
		status, pricingSource := resolveCandidatePricing(row, snapshot, model.ID)
		if query.Status != "" && query.Status != status {
			continue
		}
		name := strings.TrimSpace(model.Name)
		if name == "" {
			name = model.ID
		}
		items = append(items, ModelCandidate{
			ID: model.ID, Name: name, Sources: []string{"catalog"},
			PricingStatus: status, PricingSource: pricingSource,
		})
	}
	sort.Slice(items, func(left, right int) bool {
		leftName := strings.ToLower(items[left].Name)
		rightName := strings.ToLower(items[right].Name)
		if leftName == rightName {
			return items[left].ID < items[right].ID
		}
		return leftName < rightName
	})
	total := len(items)
	if len(items) > providerModelResultLimit {
		items = items[:providerModelResultLimit]
	}
	return ProviderModelListResponse{Items: items, Total: total}, nil
}

func loadModelPriceRows(
	ctx context.Context,
	db *gorm.DB,
) (map[string]*models.ModelPrice, error) {
	var rows []models.ModelPrice
	if err := db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load model price status: %w", app_errors.ParseDBError(err))
	}
	result := make(map[string]*models.ModelPrice, len(rows))
	for index := range rows {
		if _, duplicate := result[rows[index].ModelID]; duplicate {
			return nil, fmt.Errorf("duplicate persisted price identity: %w", app_errors.ErrInternalServer)
		}
		result[rows[index].ModelID] = &rows[index]
	}
	return result, nil
}
