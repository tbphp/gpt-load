package control

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gpt-load/internal/catalog"
	"gpt-load/internal/dialect"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type discoveryTarget struct {
	baseURL     string
	protocols   []protocol.Protocol
	keys        []string
	headerRules state.HeaderRules
	providerID  *string
}

func (s *Service) executeModelDiscovery(
	ctx context.Context,
	target discoveryTarget,
) (ModelDiscoveryResult, error) {
	if err := ctx.Err(); err != nil {
		return ModelDiscoveryResult{}, err
	}
	if strings.TrimSpace(target.baseURL) == "" || len(target.protocols) == 0 || len(target.keys) == 0 {
		return ModelDiscoveryResult{}, app_errors.ErrValidation
	}

	orderedProtocols := canonicalProtocolOrder(target.protocols)
	selectedDialects := make([]dialect.Dialect, len(orderedProtocols))
	for index, value := range orderedProtocols {
		selected, ok := s.dialects[value]
		if !ok || selected == nil {
			return ModelDiscoveryResult{}, fmt.Errorf(
				"dialect for protocol %q is not configured",
				value,
			)
		}
		selectedDialects[index] = selected
	}

	discoveryCtx, cancel := context.WithTimeout(ctx, s.modelDiscoveryTimeout)
	defer cancel()
	for _, selected := range selectedDialects {
		for _, apiKey := range target.keys {
			models, err := selected.ListModels(
				discoveryCtx,
				target.baseURL,
				apiKey,
				target.headerRules,
			)
			if parentErr := ctx.Err(); parentErr != nil {
				return ModelDiscoveryResult{}, parentErr
			}
			if errors.Is(discoveryCtx.Err(), context.DeadlineExceeded) {
				if parentErr := ctx.Err(); parentErr != nil {
					return ModelDiscoveryResult{}, parentErr
				}
				return ModelDiscoveryResult{}, fmt.Errorf(
					"discover upstream models: %w",
					app_errors.ErrBadGateway,
				)
			}
			if err == nil {
				return s.mergeDiscoveredModels(discoveryCtx, normalizeDiscoveredModels(models), target)
			}
		}
	}
	if parentErr := ctx.Err(); parentErr != nil {
		return ModelDiscoveryResult{}, parentErr
	}
	return ModelDiscoveryResult{}, fmt.Errorf(
		"discover upstream models: %w",
		app_errors.ErrBadGateway,
	)
}

func (s *Service) mergeDiscoveredModels(
	ctx context.Context,
	live []string,
	target discoveryTarget,
) (ModelDiscoveryResult, error) {
	var catalogSnapshot *catalog.Snapshot
	if s.catalogRuntime != nil {
		catalogSnapshot = s.catalogRuntime.Load()
	}
	var providerModels map[string]catalog.Model
	if target.providerID != nil && catalogSnapshot != nil {
		if provider, exists := catalogSnapshot.Providers[*target.providerID]; exists {
			providerModels = provider.Models
		}
	}
	rows := map[string]*models.ModelPrice{}
	if s.db != nil {
		loaded, err := loadModelPriceRows(ctx, s.db)
		if err != nil {
			return ModelDiscoveryResult{}, err
		}
		rows = loaded
	}

	result := make([]ModelCandidate, 0, len(live)+len(providerModels))
	seen := make(map[string]int, len(live)+len(providerModels))
	for _, id := range live {
		model, catalogMatch := providerModels[id]
		pricingStatus, pricingSource := resolveCandidatePricing(rows[id], catalogSnapshot, id)
		candidate := ModelCandidate{
			ID: id, Name: id, Sources: []string{"live"},
			PricingStatus: pricingStatus, PricingSource: pricingSource,
		}
		if catalogMatch {
			if name := strings.TrimSpace(model.Name); name != "" {
				candidate.Name = name
			}
			candidate.Sources = append(candidate.Sources, "catalog")
		}
		seen[id] = len(result)
		result = append(result, candidate)
	}
	catalogOnly := make([]ModelCandidate, 0)
	for id, model := range providerModels {
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		name := strings.TrimSpace(model.Name)
		if name == "" {
			name = id
		}
		pricingStatus, pricingSource := resolveCandidatePricing(rows[id], catalogSnapshot, id)
		catalogOnly = append(catalogOnly, ModelCandidate{
			ID: id, Name: name, Sources: []string{"catalog"},
			PricingStatus: pricingStatus, PricingSource: pricingSource,
		})
	}
	sort.Slice(catalogOnly, func(left, right int) bool {
		leftName := strings.ToLower(catalogOnly[left].Name)
		rightName := strings.ToLower(catalogOnly[right].Name)
		if leftName == rightName {
			return catalogOnly[left].ID < catalogOnly[right].ID
		}
		return leftName < rightName
	})
	result = append(result, catalogOnly...)
	return ModelDiscoveryResult{Models: result}, nil
}

func normalizeDiscoveredModels(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		normalized := strings.TrimSpace(value)
		if normalized == "" {
			continue
		}
		if _, duplicate := seen[normalized]; duplicate {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result
}

func canonicalProtocolOrder(
	values []protocol.Protocol,
) []protocol.Protocol {
	present := make(map[protocol.Protocol]struct{}, len(values))
	for _, value := range values {
		present[value] = struct{}{}
	}
	result := make([]protocol.Protocol, 0, len(present))
	for _, value := range protocol.DataPlaneProtocols() {
		if _, exists := present[value]; !exists {
			continue
		}
		result = append(result, value)
		delete(present, value)
	}
	for _, value := range values {
		if _, exists := present[value]; !exists {
			continue
		}
		result = append(result, value)
		delete(present, value)
	}
	return result
}
