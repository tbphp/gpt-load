package control

import (
	"context"
	"slices"
	"sort"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

type ModelPriceSourceGroupDTO struct {
	ID      uint   `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type ModelPriceSourceDTO struct {
	Price   ModelPriceDTO              `json:"price"`
	Groups  []ModelPriceSourceGroupDTO `json:"groups"`
	Aliases []string                   `json:"aliases"`
}

type ModelPriceSourcesResponse struct {
	Items []ModelPriceSourceDTO `json:"items"`
}

func (s *Server) handleModelPriceSources(c *gin.Context) {
	if c.Request.URL.RawQuery != "" {
		writeServiceError(c, "model_price_sources", app_errors.ErrBadRequest)
		return
	}
	result, err := s.service.ModelPriceSources(c.Request.Context())
	if err != nil {
		writeServiceError(c, "model_price_sources", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

// ModelPriceSources 一次返回分组中使用的价格，供目标选择、参考预览与本地搜索共用。
func (s *Service) ModelPriceSources(ctx context.Context) (ModelPriceSourcesResponse, error) {
	result := ModelPriceSourcesResponse{Items: []ModelPriceSourceDTO{}}
	if ctx == nil {
		ctx = context.Background()
	}
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	var snapshot *catalog.Snapshot
	if s.catalogRuntime != nil {
		snapshot = s.catalogRuntime.Load()
	}
	err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		var groups []models.Group
		if err := tx.Order("id ASC").Find(&groups).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		references, err := buildPriceReferenceSnapshot(groups)
		if err != nil {
			return err
		}
		var rows []models.ModelPrice
		if err := tx.Order("channel_id ASC, model_id ASC").Find(&rows).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		byIdentity := make(map[pricing.Identity]int, len(rows))
		for _, row := range rows {
			identity := pricing.Identity{ChannelID: row.ChannelID, ModelID: row.ModelID}
			if references.references[identity].referenceCount == 0 {
				continue
			}
			record, err := projectModelPriceRow(row, references, snapshot)
			if err != nil {
				return err
			}
			byIdentity[identity] = len(result.Items)
			result.Items = append(result.Items, ModelPriceSourceDTO{Price: record.dto, Groups: []ModelPriceSourceGroupDTO{}, Aliases: []string{}})
		}
		for _, group := range groups {
			var entries []GroupModel
			if err := decodeGroupDiscoveryJSON(group.Models, &entries); err != nil {
				return err
			}
			seen := make(map[int]bool)
			for _, entry := range entries {
				index, exists := byIdentity[pricing.Identity{ChannelID: group.ChannelID, ModelID: entry.ID}]
				if !exists {
					continue
				}
				item := &result.Items[index]
				if !seen[index] {
					item.Groups = append(item.Groups, ModelPriceSourceGroupDTO{ID: group.ID, Name: group.Name, Enabled: group.Enabled})
					seen[index] = true
				}
				if entry.Alias != "" {
					item.Aliases = append(item.Aliases, entry.Alias)
				}
			}
		}
		return nil
	})
	if err != nil {
		return ModelPriceSourcesResponse{}, err
	}
	for index := range result.Items {
		sort.Strings(result.Items[index].Aliases)
		result.Items[index].Aliases = slices.Compact(result.Items[index].Aliases)
	}
	return result, nil
}
