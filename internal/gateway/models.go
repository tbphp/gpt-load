package gateway

import (
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

const (
	modelPlaceholderRFC3339 = "2025-01-01T00:00:00Z"
	modelPlaceholderUnix    = int64(1735689600)
)

type openAIModelList struct {
	Object string        `json:"object"`
	Data   []openAIModel `json:"data"`
}

type openAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type anthropicModelList struct {
	Data    []anthropicModel `json:"data"`
	HasMore bool             `json:"has_more"`
	FirstID string           `json:"first_id"`
	LastID  string           `json:"last_id"`
}

type anthropicModel struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

type geminiModelList struct {
	Models []geminiModel `json:"models"`
}

type geminiModel struct {
	Name string `json:"name"`
}

func visibleModelIDs(
	snapshot *state.ConfigSnapshot,
	accessKey state.AccessKeyView,
	value protocol.Protocol,
) []string {
	result := make([]string, 0)
	if snapshot == nil {
		return result
	}
	if len(accessKey.Filters.Protocols) > 0 {
		if _, ok := accessKey.Filters.Protocols[value]; !ok {
			return result
		}
	}
	for modelID, targets := range snapshot.Candidates[value] {
		if len(accessKey.Filters.Models) > 0 {
			if _, ok := accessKey.Filters.Models[modelID]; !ok {
				continue
			}
		}
		if !anyVisibleTarget(targets, accessKey.Filters.Groups) {
			continue
		}
		result = append(result, modelID)
	}
	sort.Strings(result)
	return result
}

func anyVisibleTarget(targets []state.RouteTarget, groups map[uint]struct{}) bool {
	if len(targets) == 0 {
		return false
	}
	if len(groups) == 0 {
		return true
	}
	for _, target := range targets {
		if _, ok := groups[target.GroupID]; ok {
			return true
		}
	}
	return false
}

func writeVisibleModelList(
	ginContext *gin.Context,
	snapshot *state.ConfigSnapshot,
	accessKey state.AccessKeyView,
	value protocol.Protocol,
) {
	writeModelList(ginContext, value, visibleModelIDs(snapshot, accessKey, value))
}

func writeModelList(ginContext *gin.Context, value protocol.Protocol, ids []string) {
	switch value {
	case protocol.Anthropic:
		data := make([]anthropicModel, 0, len(ids))
		for _, id := range ids {
			data = append(data, anthropicModel{
				Type: "model", ID: id, DisplayName: id, CreatedAt: modelPlaceholderRFC3339,
			})
		}
		firstID, lastID := "", ""
		if len(ids) > 0 {
			firstID, lastID = ids[0], ids[len(ids)-1]
		}
		ginContext.JSON(http.StatusOK, anthropicModelList{
			Data: data, HasMore: false, FirstID: firstID, LastID: lastID,
		})
	case protocol.Gemini:
		models := make([]geminiModel, 0, len(ids))
		for _, id := range ids {
			models = append(models, geminiModel{Name: "models/" + id})
		}
		ginContext.JSON(http.StatusOK, geminiModelList{Models: models})
	default:
		data := make([]openAIModel, 0, len(ids))
		for _, id := range ids {
			data = append(data, openAIModel{
				ID: id, Object: "model", Created: modelPlaceholderUnix, OwnedBy: "gpt-load",
			})
		}
		ginContext.JSON(http.StatusOK, openAIModelList{Object: "list", Data: data})
	}
}
