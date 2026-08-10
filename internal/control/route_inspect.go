package control

import (
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/execution"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
)

type routeInspectRequest struct {
	Protocol      protocol.Protocol `json:"protocol"`
	ExternalModel *string           `json:"external_model"`
	AccessKeyID   uint              `json:"access_key_id"`
}

type routeInspectAccessKeyResponse struct {
	ID     uint                  `json:"id"`
	Name   string                `json:"name"`
	Status state.AccessKeyStatus `json:"status"`
}

type routeInspectCredentialResponse struct {
	CredentialID    uint                  `json:"credential_id"`
	Available       bool                  `json:"available"`
	ReasonCode      *scheduler.ReasonCode `json:"reason_code"`
	WeightManual    *int                  `json:"weight_manual"`
	WeightAuto      int                   `json:"weight_auto"`
	EffectiveWeight int64                 `json:"effective_weight"`
	CooldownUntilMS *int64                `json:"cooldown_until_ms"`
}

type routeInspectGroupResponse struct {
	GroupID       uint                             `json:"group_id"`
	GroupName     string                           `json:"group_name"`
	UpstreamModel *string                          `json:"upstream_model"`
	WeightManual  *int                             `json:"weight_manual"`
	Included      bool                             `json:"included"`
	Routable      bool                             `json:"routable"`
	ReasonCode    *scheduler.ReasonCode            `json:"reason_code"`
	Credentials   []routeInspectCredentialResponse `json:"credentials"`
}

type routeInspectResponse struct {
	ObservedAtMS     int64                         `json:"observed_at_ms"`
	SnapshotRevision uint64                        `json:"snapshot_revision"`
	Protocol         protocol.Protocol             `json:"protocol"`
	ExternalModel    *string                       `json:"external_model"`
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
	if !request.Protocol.DataPlaneEnabled() ||
		request.AccessKeyID == 0 {
		return app_errors.ErrValidation
	}
	if request.ExternalModel != nil && !validUsageModel(*request.ExternalModel) {
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
			ClientProtocol: request.Protocol,
			Operation:      routeInspectOperation(request.Protocol, request.ExternalModel),
			ExternalModel:  cloneRouteModel(request.ExternalModel),
			AccessKey:      accessKey,
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
	)
}

func routeInspectOperation(
	clientProtocol protocol.Protocol,
	externalModel *string,
) execution.Operation {
	if clientProtocol == protocol.OpenAIResponses {
		if externalModel == nil {
			return execution.OperationResponsesRetrieve
		}
		return execution.OperationResponsesCreate
	}
	return execution.OperationChatCompletion
}

func mapRouteInspectResponse(
	observation runtimeObservation,
	request routeInspectRequest,
	accessKey state.AccessKeyView,
	explanation scheduler.Inspection,
) (routeInspectResponse, error) {
	observedAtMS, err := safeEpochMilliseconds(observation.observedAt)
	if err != nil {
		return routeInspectResponse{}, fmt.Errorf("map route inspection observed_at_ms: %w", err)
	}
	result := routeInspectResponse{
		ObservedAtMS:     observedAtMS,
		SnapshotRevision: observation.snapshot.Revision,
		Protocol:         request.Protocol,
		ExternalModel:    cloneRouteModel(request.ExternalModel),
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
			UpstreamModel: cloneRouteModel(group.UpstreamModelID),
			WeightManual:  cloneInt(group.WeightManual),
			Included:      group.Included,
			Routable:      group.Routable,
			ReasonCode:    optionalReason(group.Reason),
			Credentials:   []routeInspectCredentialResponse{},
		}
		for _, credential := range group.Credentials {
			cooldownUntilMS, err := optionalSafeEpochMilliseconds(credential.CooldownUntil)
			if err != nil {
				return routeInspectResponse{}, fmt.Errorf(
					"map route inspection cooldown_until_ms: %w",
					err,
				)
			}
			groupResponse.Credentials = append(groupResponse.Credentials, routeInspectCredentialResponse{
				CredentialID:    credential.CredentialID,
				Available:       credential.Available,
				ReasonCode:      optionalReason(credential.Reason),
				WeightManual:    cloneInt(credential.WeightManual),
				WeightAuto:      credential.WeightAuto,
				EffectiveWeight: credential.EffectiveWeight,
				CooldownUntilMS: cooldownUntilMS,
			})
		}
		result.Groups = append(result.Groups, groupResponse)
	}
	return result, nil
}

func cloneRouteModel(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
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
