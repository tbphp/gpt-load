package control

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

type discoveryCredential struct {
	snapshot execution.CredentialSnapshot
	apiKey   string
}

type discoveryTarget struct {
	channelID         channel.ID
	resolvedTarget    channel.ResolvedTarget
	credentials       []discoveryCredential
	headerRules       state.HeaderRules
	timeouts          state.TimeoutConfig
	catalogProviderID string
}

func (s *Service) executeModelDiscovery(
	ctx context.Context,
	target discoveryTarget,
) (ModelDiscoveryResult, error) {
	if err := ctx.Err(); err != nil {
		return ModelDiscoveryResult{}, err
	}
	if s == nil || s.executor == nil {
		return ModelDiscoveryResult{}, app_errors.ErrInternalServer
	}
	if target.channelID == "" ||
		target.resolvedTarget.ChannelID != target.channelID || len(target.credentials) == 0 {
		return ModelDiscoveryResult{}, app_errors.ErrValidation
	}
	clientProtocol, method, path, body, err := utilityRequestShape(
		target.resolvedTarget.ProviderKind,
		execution.OperationListModels,
		"",
	)
	if err != nil {
		return ModelDiscoveryResult{}, err
	}
	routeMode, supported := target.resolvedTarget.Mode(clientProtocol, execution.OperationListModels)
	if !supported {
		return ModelDiscoveryResult{}, app_errors.ErrValidation
	}

	discoveryCtx, cancel := context.WithTimeout(ctx, s.modelDiscoveryTimeout)
	defer cancel()
	requestID, err := s.newExecutionID()
	if err != nil {
		return ModelDiscoveryResult{}, fmt.Errorf("create discovery request identity: %w", app_errors.ErrInternalServer)
	}
	for index, credential := range target.credentials {
		attemptID, identityErr := s.newExecutionID()
		if identityErr != nil {
			return ModelDiscoveryResult{}, fmt.Errorf("create discovery attempt identity: %w", app_errors.ErrInternalServer)
		}
		spec := execution.NewAttemptSpec(execution.AttemptSpec{
			RequestID: requestID, AttemptID: attemptID, Sequence: uint32(index + 1),
			ChannelID: string(target.channelID), TargetKind: string(target.resolvedTarget.ProviderKind),
			RouteMode: execution.RouteMode(routeMode), ClientProtocol: clientProtocol,
			Operation: execution.OperationListModels, Method: method, Path: path,
			Header: applyControlHeaderRules(target.headerRules, credential.apiKey), Body: body,
			TargetConfig: target.resolvedTarget.TargetConfig,
			Timeouts:     executionTimeouts(target.timeouts),
			Credential:   credential.snapshot,
		})
		if validationErr := spec.Validate(); validationErr != nil {
			return ModelDiscoveryResult{}, fmt.Errorf("build discovery attempt: %w", app_errors.ErrInternalServer)
		}
		result := s.executor.Execute(discoveryCtx, spec)
		if parentErr := ctx.Err(); parentErr != nil {
			return ModelDiscoveryResult{}, parentErr
		}
		if errors.Is(discoveryCtx.Err(), context.DeadlineExceeded) {
			return ModelDiscoveryResult{}, fmt.Errorf("discover upstream models: %w", app_errors.ErrBadGateway)
		}
		if result.Validate() != nil || result.Error != nil ||
			result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
			continue
		}
		models, parseErr := parseDiscoveredModels(clientProtocol, result.Body)
		if parseErr != nil {
			continue
		}
		return s.mergeDiscoveredModels(discoveryCtx, normalizeDiscoveredModels(models), target)
	}
	if parentErr := ctx.Err(); parentErr != nil {
		return ModelDiscoveryResult{}, parentErr
	}
	return ModelDiscoveryResult{}, fmt.Errorf("discover upstream models: %w", app_errors.ErrBadGateway)
}

func (s *Service) newExecutionID() (string, error) {
	random := io.Reader(cryptorand.Reader)
	if s != nil && s.random != nil {
		random = s.random
	}
	return newOperationID(random)
}

func executionTimeouts(value state.TimeoutConfig) execution.AttemptTimeouts {
	return execution.AttemptTimeouts{
		FirstByte: value.FirstByte,
		Request:   value.Request, StreamIdle: value.StreamIdle,
	}
}

func applyControlHeaderRules(rules state.HeaderRules, apiKey string) http.Header {
	headers := make(http.Header, len(rules.Set))
	for name, value := range rules.Set {
		headers.Set(name, strings.ReplaceAll(value, "${API_KEY}", apiKey))
	}
	for _, name := range rules.Remove {
		headers.Del(name)
	}
	headers.Set("Accept-Encoding", "identity")
	return headers
}

func utilityRequestShape(
	providerKind channel.ProviderKind,
	operation execution.Operation,
	model string,
) (protocol.Protocol, string, string, []byte, error) {
	switch operation {
	case execution.OperationListModels:
		switch providerKind {
		case channel.ProviderOpenAI, channel.ProviderOpenAICompatible:
			return protocol.OpenAICompletions, http.MethodGet, "/v1/models", nil, nil
		case channel.ProviderAzureOpenAI, channel.ProviderAWSBedrock, channel.ProviderGoogleVertex:
			return protocol.OpenAICompletions, http.MethodGet, "/v1/models", nil, nil
		case channel.ProviderAnthropic, channel.ProviderAnthropicCompatible:
			return protocol.Anthropic, http.MethodGet, "/v1/models", nil, nil
		case channel.ProviderGemini, channel.ProviderGeminiCompatible:
			return protocol.Gemini, http.MethodGet, "/v1beta/models", nil, nil
		}
	case execution.OperationProbe:
		model = strings.TrimSpace(model)
		if model == "" {
			return "", "", "", nil, app_errors.ErrValidation
		}
		switch providerKind {
		case channel.ProviderOpenAI, channel.ProviderOpenAICompatible:
			return protocol.OpenAICompletions, http.MethodPost, "/v1/chat/completions",
				[]byte(`{"model":"probe","messages":[{"role":"user","content":"ping"}],"max_tokens":1}`), nil
		case channel.ProviderAzureOpenAI, channel.ProviderAWSBedrock, channel.ProviderGoogleVertex:
			return protocol.OpenAICompletions, http.MethodPost, "/v1/chat/completions",
				[]byte(`{"model":"probe","messages":[{"role":"user","content":"ping"}],"max_tokens":1}`), nil
		case channel.ProviderAnthropic, channel.ProviderAnthropicCompatible:
			return protocol.Anthropic, http.MethodPost, "/v1/messages",
				[]byte(`{"model":"probe","max_tokens":1,"messages":[{"role":"user","content":"ping"}]}`), nil
		case channel.ProviderGemini, channel.ProviderGeminiCompatible:
			return protocol.Gemini, http.MethodPost, "/v1beta/models/probe:generateContent",
				[]byte(`{"contents":[{"parts":[{"text":"ping"}]}],"generationConfig":{"maxOutputTokens":1}}`), nil
		}
	}
	return "", "", "", nil, app_errors.ErrValidation
}

func parseDiscoveredModels(clientProtocol protocol.Protocol, body []byte) ([]string, error) {
	switch clientProtocol {
	case protocol.OpenAICompletions, protocol.OpenAIResponses, protocol.Anthropic:
		var payload struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := decodeSingleJSON(body, &payload); err != nil {
			return nil, err
		}
		models := make([]string, 0, len(payload.Data))
		for _, item := range payload.Data {
			models = append(models, item.ID)
		}
		return models, nil
	case protocol.Gemini:
		var payload struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := decodeSingleJSON(body, &payload); err != nil {
			return nil, err
		}
		models := make([]string, 0, len(payload.Models))
		for _, item := range payload.Models {
			models = append(models, strings.TrimPrefix(item.Name, "models/"))
		}
		return models, nil
	default:
		return nil, app_errors.ErrValidation
	}
}

func decodeSingleJSON(body []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
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
	if target.catalogProviderID != "" && catalogSnapshot != nil {
		if provider, exists := catalogSnapshot.Providers[target.catalogProviderID]; exists {
			providerModels = provider.Models
		}
	}
	rows := modelPriceRows{}
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
		identity := pricing.Identity{ChannelID: string(target.channelID), ModelID: id}
		pricingStatus, pricingSource := resolveCandidatePricing(rows[identity], catalogSnapshot, identity)
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
		identity := pricing.Identity{ChannelID: string(target.channelID), ModelID: id}
		pricingStatus, pricingSource := resolveCandidatePricing(rows[identity], catalogSnapshot, identity)
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
