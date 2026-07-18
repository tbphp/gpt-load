package control

import (
	"context"
	"fmt"
	"strings"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

type ModelDiscoveryRequest struct {
	UpstreamURL string            `json:"upstream_url"`
	Protocol    protocol.Protocol `json:"protocol"`
	Key         string            `json:"key"`
}

type ModelDiscoveryResult struct {
	Models []string `json:"models"`
}

func (s *Service) DiscoverModels(
	ctx context.Context,
	request ModelDiscoveryRequest,
) (ModelDiscoveryResult, error) {
	if err := ctx.Err(); err != nil {
		return ModelDiscoveryResult{}, err
	}

	baseURL, _, err := normalizeUpstreamBaseURL(request.UpstreamURL)
	if err != nil {
		return ModelDiscoveryResult{}, err
	}
	if !request.Protocol.Valid() || request.Protocol == protocol.OpenAIResponse {
		return ModelDiscoveryResult{}, app_errors.ErrValidation
	}
	apiKey := strings.TrimSpace(request.Key)
	if apiKey == "" {
		return ModelDiscoveryResult{}, app_errors.ErrValidation
	}

	selected, ok := s.dialects[request.Protocol]
	if !ok || selected == nil {
		return ModelDiscoveryResult{}, fmt.Errorf("dialect for protocol %q is not configured", request.Protocol)
	}

	discoveryCtx, cancel := context.WithTimeout(ctx, s.modelDiscoveryTimeout)
	defer cancel()
	models, err := selected.ListModels(discoveryCtx, baseURL, apiKey, state.HeaderRules{})
	if parentErr := ctx.Err(); parentErr != nil {
		return ModelDiscoveryResult{}, parentErr
	}
	if err != nil {
		return ModelDiscoveryResult{}, fmt.Errorf("discover upstream models: %w", app_errors.ErrBadGateway)
	}
	return ModelDiscoveryResult{Models: append([]string{}, models...)}, nil
}
