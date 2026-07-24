package control

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
)

type routeInspectRequest struct {
	Protocol      protocol.Protocol `json:"protocol"`
	ExternalModel string            `json:"external_model"`
	AccessKeyID   uint              `json:"access_key_id"`
}

type routeInspectAccessKeyResponse struct {
	ID     uint                  `json:"id"`
	Name   string                `json:"name"`
	Status state.AccessKeyStatus `json:"status"`
}

type routeInspectKeyResponse struct {
	KeyID           uint                  `json:"key_id"`
	Available       bool                  `json:"available"`
	ReasonCode      *scheduler.ReasonCode `json:"reason_code"`
	WeightManual    *int                  `json:"weight_manual"`
	WeightAuto      int                   `json:"weight_auto"`
	EffectiveWeight int64                 `json:"effective_weight"`
	CooldownUntil   *time.Time            `json:"cooldown_until"`
}

type routeInspectGroupResponse struct {
	GroupID       uint                      `json:"group_id"`
	GroupName     string                    `json:"group_name"`
	UpstreamModel string                    `json:"upstream_model"`
	WeightManual  *int                      `json:"weight_manual"`
	Included      bool                      `json:"included"`
	Routable      bool                      `json:"routable"`
	ReasonCode    *scheduler.ReasonCode     `json:"reason_code"`
	Keys          []routeInspectKeyResponse `json:"keys"`
}

type routeInspectResponse struct {
	ObservedAt       time.Time                     `json:"observed_at"`
	SnapshotRevision uint64                        `json:"snapshot_revision"`
	Protocol         protocol.Protocol             `json:"protocol"`
	ExternalModel    string                        `json:"external_model"`
	AccessKey        routeInspectAccessKeyResponse `json:"access_key"`
	Routable         bool                          `json:"routable"`
	ReasonCode       *scheduler.ReasonCode         `json:"reason_code"`
	Groups           []routeInspectGroupResponse   `json:"groups"`
}

func optionalReason(value scheduler.ReasonCode) *scheduler.ReasonCode {
	if value == "" {
		return nil
	}
	cloned := value
	return &cloned
}

func validateRouteInspectRequest(request routeInspectRequest) error {
	if !request.Protocol.Valid() ||
		request.AccessKeyID == 0 ||
		request.ExternalModel == "" ||
		strings.TrimSpace(request.ExternalModel) != request.ExternalModel {
		return app_errors.ErrValidation
	}
	return nil
}

func (service *Service) InspectRoute(
	request routeInspectRequest,
) (routeInspectResponse, error) {
	if err := validateRouteInspectRequest(request); err != nil {
		return routeInspectResponse{}, err
	}
	observation, err := service.captureRuntimeObservation()
	if err != nil {
		return routeInspectResponse{}, err
	}
	accessKey, exists := observation.snapshot.AccessKeysByID[request.AccessKeyID]
	if !exists {
		return routeInspectResponse{}, app_errors.ErrResourceNotFound
	}
	explanation, err := scheduler.Inspect(
		observation.snapshot,
		observation.keys,
		scheduler.Query{
			Protocol:      request.Protocol,
			ExternalModel: request.ExternalModel,
			AccessKey:     accessKey,
		},
		observation.observedAt,
	)
	if err != nil {
		if errors.Is(err, scheduler.ErrInconsistentSnapshot) {
			return routeInspectResponse{}, fmt.Errorf(
				"inspect current route: %w",
				app_errors.ErrInternalServer,
			)
		}
		return routeInspectResponse{}, err
	}
	return mapRouteInspectResponse(
		observation,
		request,
		accessKey,
		explanation,
	), nil
}

func mapRouteInspectResponse(
	observation runtimeObservation,
	request routeInspectRequest,
	accessKey state.AccessKeyView,
	explanation scheduler.Inspection,
) routeInspectResponse {
	result := routeInspectResponse{
		ObservedAt:       observation.observedAt,
		SnapshotRevision: observation.snapshot.Revision,
		Protocol:         request.Protocol,
		ExternalModel:    request.ExternalModel,
		AccessKey: routeInspectAccessKeyResponse{
			ID: accessKey.ID, Name: accessKey.Name, Status: accessKey.Status,
		},
		Routable:   explanation.Routable,
		ReasonCode: optionalReason(explanation.Reason),
		Groups:     []routeInspectGroupResponse{},
	}
	for _, group := range explanation.Groups {
		groupResponse := routeInspectGroupResponse{
			GroupID:       group.GroupID,
			GroupName:     group.GroupName,
			UpstreamModel: group.UpstreamModelID,
			WeightManual:  cloneInt(group.WeightManual),
			Included:      group.Included,
			Routable:      group.Routable,
			ReasonCode:    optionalReason(group.Reason),
			Keys:          []routeInspectKeyResponse{},
		}
		for _, key := range group.Keys {
			groupResponse.Keys = append(groupResponse.Keys, routeInspectKeyResponse{
				KeyID:           key.KeyID,
				Available:       key.Available,
				ReasonCode:      optionalReason(key.Reason),
				WeightManual:    cloneInt(key.WeightManual),
				WeightAuto:      key.WeightAuto,
				EffectiveWeight: key.EffectiveWeight,
				CooldownUntil:   optionalUTC(key.CooldownUntil),
			})
		}
		result.Groups = append(result.Groups, groupResponse)
	}
	return result
}

func (server *Server) handleRouteInspect(c *gin.Context) {
	var request routeInspectRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "inspect_route", mapControlJSONError(err))
		return
	}
	result, err := server.service.InspectRoute(request)
	if err != nil {
		writeServiceError(c, "inspect_route", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}
