package control

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

type ModelPriceSourceDTO struct {
	Key           string                                 `json:"key"`
	Kind          string                                 `json:"kind"`
	ProviderID    string                                 `json:"provider_id"`
	ProviderName  string                                 `json:"provider_name"`
	ModelID       string                                 `json:"model_id"`
	Name          string                                 `json:"name"`
	Prices        PriceSlotsDTO                          `json:"prices"`
	ContextTiers  []ModelPriceContextTierDTO             `json:"context_tiers"`
	ModeSchedules map[pricing.Mode]ModelPriceScheduleDTO `json:"mode_schedules"`
}

type ModelPriceSourcesResponse struct {
	Items   []ModelPriceSourceDTO `json:"items"`
	HasMore bool                  `json:"has_more"`
}

func (s *Server) handleModelPriceSources(c *gin.Context) {
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		writeServiceError(c, "model_price_sources", app_errors.ErrBadRequest)
		return
	}
	for key, values := range query {
		if (key != "mode" && key != "q") || len(values) != 1 {
			writeServiceError(c, "model_price_sources", app_errors.ErrBadRequest)
			return
		}
	}
	mode := query.Get("mode")
	if mode == "" {
		mode = "copy"
	}
	result, err := s.service.ModelPriceSources(c.Request.Context(), mode, strings.TrimSpace(query.Get("q")))
	if err != nil {
		writeServiceError(c, "model_price_sources", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Service) ModelPriceSources(ctx context.Context, mode, search string) (ModelPriceSourcesResponse, error) {
	result := ModelPriceSourcesResponse{Items: []ModelPriceSourceDTO{}}
	if (mode != "copy" && mode != "link") || !utf8.ValidString(search) || utf8.RuneCountInString(search) > maxModelPriceSearchRunes {
		return result, app_errors.ErrValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	var snapshot *catalog.Snapshot
	if s.catalogRuntime != nil {
		snapshot = s.catalogRuntime.Load()
	}
	search = strings.ToLower(search)
	matches := func(values ...string) bool {
		return search == "" || strings.Contains(strings.ToLower(strings.Join(values, " ")), search)
	}
	if snapshot != nil {
		for providerID, provider := range snapshot.Providers {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			for modelID, model := range provider.Models {
				if !matches(providerID, provider.Name, modelID, model.Name) {
					continue
				}
				if _, available := lookupCatalogPrice(snapshot, providerID, modelID); !available {
					continue
				}
				modes := make(map[pricing.Mode]ModelPriceScheduleDTO, len(model.Cost.ModePrices))
				for mode, prices := range model.Cost.ModePrices {
					modes[mode] = ModelPriceScheduleDTO{Prices: projectPriceSlots(prices), ContextTiers: []ModelPriceContextTierDTO{}}
				}
				result.Items = append(result.Items, ModelPriceSourceDTO{
					Key: "catalog:" + providerID + ":" + modelID, Kind: "catalog", ProviderID: providerID,
					ProviderName: pricingSourceName(snapshot, providerID, providerID), ModelID: modelID, Name: model.Name,
					Prices: projectPriceSlots(model.Cost.Prices), ContextTiers: projectContextPriceTiers(model.Cost.ContextTiers), ModeSchedules: modes,
				})
			}
		}
	}
	if mode == "copy" {
		var rows []models.ModelPrice
		if err := s.db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
			return result, app_errors.ParseDBError(err)
		}
		for _, row := range rows {
			if !modelPriceHasConfiguredValue(row) {
				continue
			}
			identity, err := PriceIdentityForChannelModel(row.ChannelID, row.ModelID)
			if err != nil {
				return result, app_errors.ErrInternalServer
			}
			descriptor, _ := modelPriceChannelRegistry.Get(channel.ID(identity.ChannelID))
			if !matches(row.ChannelID, descriptor.Name, row.ModelID) {
				continue
			}
			rule, err := persistedPriceRule(row)
			if err != nil {
				return result, app_errors.ErrInternalServer
			}
			result.Items = append(result.Items, ModelPriceSourceDTO{
				Key: fmt.Sprintf("local:%d", row.ID), Kind: "local", ProviderID: row.ChannelID, ProviderName: descriptor.Name,
				ModelID: row.ModelID, Name: row.ModelID, Prices: projectPriceSlots(rule.Prices),
				ContextTiers: projectContextPriceTiers(rule.ContextTiers), ModeSchedules: projectModeSchedules(rule.ModeSchedules),
			})
		}
	}
	providerRanks := make(map[string]int)
	for rank, providerID := range catalog.AutomaticPriceProviderPriority() {
		providerRanks[providerID] = rank
	}
	sourceRank := func(source ModelPriceSourceDTO) int {
		if source.Kind == "local" {
			return len(providerRanks)
		}
		if rank, exists := providerRanks[source.ProviderID]; exists {
			return rank
		}
		return len(providerRanks) + 1
	}
	sort.Slice(result.Items, func(i, j int) bool {
		left, right := result.Items[i], result.Items[j]
		leftExact, rightExact := strings.EqualFold(left.ModelID, search), strings.EqualFold(right.ModelID, search)
		if leftExact != rightExact {
			return leftExact
		}
		if leftRank, rightRank := sourceRank(left), sourceRank(right); leftRank != rightRank {
			return leftRank < rightRank
		}
		return left.Key < right.Key
	})
	const limit = 50
	if len(result.Items) > limit {
		result.HasMore = true
		result.Items = result.Items[:limit]
	}
	return result, nil
}
