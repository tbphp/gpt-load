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

type ProviderSuggestion struct {
	ProviderID string              `json:"provider_id"`
	Name       string              `json:"name"`
	APIURL     string              `json:"api_url,omitempty"`
	Protocols  []protocol.Protocol `json:"protocols"`
	Mark       string              `json:"mark"`
	Official   bool                `json:"official"`
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
	official := s.catalogRuntime.OfficialSuggestions()
	items := make([]ProviderSuggestion, 0, 3+providerSuggestionCatalogLimit)
	for _, provider := range official {
		items = append(items, mapProviderSuggestion(provider, true))
	}
	catalogOnly := s.catalogRuntime.SearchProviderMetadata(query, providerSuggestionCatalogLimit)
	for _, provider := range catalogOnly {
		items = append(items, mapProviderSuggestion(provider, false))
	}
	return ProviderSuggestionListResponse{Items: items, Total: len(items)}, nil
}

func mapProviderSuggestion(provider catalog.Provider, official bool) ProviderSuggestion {
	return ProviderSuggestion{
		ProviderID: provider.ID,
		Name:       provider.Name,
		APIURL:     provider.APIURL,
		Protocols:  append([]protocol.Protocol(nil), provider.Protocols...),
		Mark:       provider.Mark,
		Official:   official,
	}
}

func (s *Service) validateSelectableProviderID(providerID *string) error {
	if providerID == nil || catalog.IsOfficialProviderID(*providerID) {
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
	provider, exists := s.catalogRuntime.LoadProvider(providerID)
	if !exists {
		return ProviderModelListResponse{}, app_errors.ErrResourceNotFound
	}
	scopeKey, _ := pricing.ProviderScopeKey(providerID)
	rows, err := loadPriceRowsByScope(ctx, s.db, scopeKey)
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
		status := resolveCandidatePricingStatus(row, &model)
		if query.Status != "" && query.Status != status {
			continue
		}
		name := strings.TrimSpace(model.Name)
		if name == "" {
			name = model.ID
		}
		items = append(items, ModelCandidate{
			ID: model.ID, Name: name, Sources: []string{"catalog"}, PricingStatus: status,
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

func loadPriceRowsByScope(
	ctx context.Context,
	db *gorm.DB,
	scopeKey string,
) (map[string]*models.ModelPrice, error) {
	var rows []models.ModelPrice
	if err := db.WithContext(ctx).Where("price_scope_key = ?", scopeKey).Order("id ASC").Find(&rows).Error; err != nil {
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
