package control

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

const (
	maxModelDiscoveryPages  = 20
	maxModelDiscoveryModels = 20_000
	modelDiscoveryPageSize  = 1000
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
	clientProtocol, supported := target.resolvedTarget.PreferredProtocol(execution.OperationListModels, "")
	if !supported {
		return ModelDiscoveryResult{}, app_errors.ErrValidation
	}
	clientProtocol, method, path, body, err := utilityRequestShape(clientProtocol, execution.OperationListModels)
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
	var sequence uint32
	for _, credential := range target.credentials {
		models := make([]string, 0)
		rawQuery := modelDiscoveryInitialQuery(clientProtocol)
		seenCursors := make(map[string]struct{})
		for pageNumber := 0; pageNumber < maxModelDiscoveryPages; pageNumber++ {
			attemptID, identityErr := s.newExecutionID()
			if identityErr != nil {
				return ModelDiscoveryResult{}, fmt.Errorf("create discovery attempt identity: %w", app_errors.ErrInternalServer)
			}
			sequence++
			spec := execution.NewAttemptSpec(execution.AttemptSpec{
				RequestID: requestID, AttemptID: attemptID, Sequence: sequence,
				ChannelID: string(target.channelID),
				RouteMode: execution.RouteMode(routeMode), ClientProtocol: clientProtocol,
				Operation: execution.OperationListModels, Method: method, Path: path,
				RawQuery: rawQuery,
				Header:   applyControlHeaderRules(target.headerRules, credential.apiKey), Body: body,
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
				break
			}
			discoveredPage, parseErr := parseDiscoveredModelsPage(clientProtocol, result.Body)
			if parseErr != nil {
				break
			}
			models = append(models, discoveredPage.models...)
			if len(models) > maxModelDiscoveryModels {
				break
			}
			if discoveredPage.nextRawQuery == "" {
				return s.mergeDiscoveredModels(
					discoveryCtx,
					normalizeDiscoveredModels(models),
					target,
				)
			}
			if _, repeated := seenCursors[discoveredPage.nextRawQuery]; repeated {
				break
			}
			seenCursors[discoveredPage.nextRawQuery] = struct{}{}
			rawQuery = discoveredPage.nextRawQuery
		}
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
	clientProtocol protocol.Protocol,
	operation execution.Operation,
) (protocol.Protocol, string, string, []byte, error) {
	switch operation {
	case execution.OperationListModels:
		switch clientProtocol {
		case protocol.OpenAICompletions, protocol.Anthropic:
			return clientProtocol, http.MethodGet, "/v1/models", nil, nil
		case protocol.Gemini:
			return clientProtocol, http.MethodGet, "/v1beta/models", nil, nil
		default:
			return "", "", "", nil, app_errors.ErrValidation
		}
	}
	return "", "", "", nil, app_errors.ErrValidation
}

type discoveredModelsPage struct {
	models       []string
	nextRawQuery string
}

func parseDiscoveredModelsPage(
	clientProtocol protocol.Protocol,
	body []byte,
) (discoveredModelsPage, error) {
	switch clientProtocol {
	case protocol.OpenAICompletions:
		var payload struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := decodeSingleJSON(body, &payload); err != nil {
			return discoveredModelsPage{}, err
		}
		models := make([]string, 0, len(payload.Data))
		for _, item := range payload.Data {
			models = append(models, item.ID)
		}
		return discoveredModelsPage{models: models}, nil
	case protocol.Anthropic:
		var payload struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := decodeSingleJSON(body, &payload); err != nil {
			return discoveredModelsPage{}, err
		}
		models := make([]string, 0, len(payload.Data))
		for _, item := range payload.Data {
			models = append(models, item.ID)
		}
		query, err := nextModelDiscoveryQuery(
			clientProtocol,
			payload.HasMore,
			"after_id",
			payload.LastID,
		)
		if err != nil {
			return discoveredModelsPage{}, err
		}
		return discoveredModelsPage{models: models, nextRawQuery: query}, nil
	case protocol.Gemini:
		var payload struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := decodeSingleJSON(body, &payload); err != nil {
			return discoveredModelsPage{}, err
		}
		models := make([]string, 0, len(payload.Models))
		for _, item := range payload.Models {
			models = append(models, strings.TrimPrefix(item.Name, "models/"))
		}
		query, err := nextModelDiscoveryQuery(
			clientProtocol,
			payload.NextPageToken != "",
			"pageToken",
			payload.NextPageToken,
		)
		if err != nil {
			return discoveredModelsPage{}, err
		}
		return discoveredModelsPage{models: models, nextRawQuery: query}, nil
	default:
		return discoveredModelsPage{}, app_errors.ErrValidation
	}
}

func modelDiscoveryInitialQuery(clientProtocol protocol.Protocol) string {
	values := make(url.Values, 1)
	switch clientProtocol {
	case protocol.Anthropic:
		values.Set("limit", strconv.Itoa(modelDiscoveryPageSize))
	case protocol.Gemini:
		values.Set("pageSize", strconv.Itoa(modelDiscoveryPageSize))
	}
	return values.Encode()
}

func nextModelDiscoveryQuery(
	clientProtocol protocol.Protocol,
	hasMore bool,
	key string,
	cursor string,
) (string, error) {
	if !hasMore {
		return "", nil
	}
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return "", fmt.Errorf("model list pagination cursor is missing")
	}
	values, err := url.ParseQuery(modelDiscoveryInitialQuery(clientProtocol))
	if err != nil {
		return "", fmt.Errorf("build model list pagination query")
	}
	values.Set(key, cursor)
	return values.Encode(), nil
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
