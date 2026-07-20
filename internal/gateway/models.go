package gateway

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strconv"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

const (
	modelPlaceholderRFC3339 = "2025-01-01T00:00:00Z"
	modelPlaceholderUnix    = int64(1735689600)
)

var errModelListTooLarge = errors.New("model list exceeds response limit")

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
	result, err := collectVisibleModelIDs(snapshot, accessKey, value, math.MaxInt64)
	if err != nil {
		return []string{}
	}
	return result
}

func collectVisibleModelIDs(
	snapshot *state.ConfigSnapshot,
	accessKey state.AccessKeyView,
	value protocol.Protocol,
	limit int64,
) ([]string, error) {
	result := make([]string, 0)
	emptyBody, err := marshalModelList(value, nil)
	if err != nil {
		return nil, err
	}
	encodedLowerBound := int64(len(emptyBody))
	if limit < encodedLowerBound {
		return nil, errModelListTooLarge
	}
	if snapshot == nil {
		return result, nil
	}
	if len(accessKey.Filters.Protocols) > 0 {
		if _, ok := accessKey.Filters.Protocols[value]; !ok {
			return result, nil
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
		item, err := marshalModelListItem(value, modelID)
		if err != nil {
			return nil, err
		}
		required := int64(len(item))
		if len(result) > 0 {
			required++
		}
		if required > limit-encodedLowerBound {
			return nil, errModelListTooLarge
		}
		encodedLowerBound += required
		result = append(result, modelID)
	}
	sort.Strings(result)
	return result, nil
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

func (handler *Handler) writeVisibleModelList(
	ginContext *gin.Context,
	snapshot *state.ConfigSnapshot,
	accessKey state.AccessKeyView,
	value protocol.Protocol,
) {
	body, err := buildVisibleModelList(snapshot, accessKey, value, handler.modelListLimit)
	if err != nil {
		writeReason(ginContext, reasonModelListTooLarge)
		return
	}
	headers := http.Header{
		"Content-Length": {strconv.Itoa(len(body))},
		"Content-Type":   {"application/json; charset=utf-8"},
	}
	if err := handler.writeBufferedResponse(ginContext, http.StatusOK, headers, body); err != nil {
		return
	}
}

func buildVisibleModelList(
	snapshot *state.ConfigSnapshot,
	accessKey state.AccessKeyView,
	value protocol.Protocol,
	limit int64,
) ([]byte, error) {
	if limit < 0 {
		return nil, errModelListTooLarge
	}
	ids, err := collectVisibleModelIDs(snapshot, accessKey, value, limit)
	if err != nil {
		return nil, err
	}
	body, err := marshalModelList(value, ids)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errModelListTooLarge
	}
	return body, nil
}

func marshalModelListItem(value protocol.Protocol, id string) ([]byte, error) {
	switch value {
	case protocol.Anthropic:
		return json.Marshal(anthropicModel{
			Type: "model", ID: id, DisplayName: id, CreatedAt: modelPlaceholderRFC3339,
		})
	case protocol.Gemini:
		return json.Marshal(geminiModel{Name: "models/" + id})
	default:
		return json.Marshal(openAIModel{
			ID: id, Object: "model", Created: modelPlaceholderUnix, OwnedBy: "gpt-load",
		})
	}
}

func marshalModelList(value protocol.Protocol, ids []string) ([]byte, error) {
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
		return json.Marshal(anthropicModelList{
			Data: data, HasMore: false, FirstID: firstID, LastID: lastID,
		})
	case protocol.Gemini:
		models := make([]geminiModel, 0, len(ids))
		for _, id := range ids {
			models = append(models, geminiModel{Name: "models/" + id})
		}
		return json.Marshal(geminiModelList{Models: models})
	default:
		data := make([]openAIModel, 0, len(ids))
		for _, id := range ids {
			data = append(data, openAIModel{
				ID: id, Object: "model", Created: modelPlaceholderUnix, OwnedBy: "gpt-load",
			})
		}
		return json.Marshal(openAIModelList{Object: "list", Data: data})
	}
}
