package control

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gpt-load/internal/dialect"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

type discoveryTarget struct {
	baseURL     string
	protocols   []protocol.Protocol
	keys        []string
	headerRules state.HeaderRules
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
				return ModelDiscoveryResult{Models: normalizeDiscoveredModels(models)}, nil
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
