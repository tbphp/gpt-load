package gateway

import (
	"net/http"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/platform/utils"
)

func requestPeerIP(request *http.Request) string {
	if request == nil {
		return ""
	}
	peer, err := utils.NormalizePeerIP(request.RemoteAddr)
	if err != nil {
		return ""
	}
	return peer
}

func (handler *Handler) logDataPlaneAuthFailed(request *http.Request) {
	total, shouldLog := handler.authFailureEvents.Observe()
	if !shouldLog {
		return
	}
	utils.LogPlaneBestEffort(
		handler.logger,
		logrus.WarnLevel,
		utils.LogPlaneData,
		logrus.Fields{
			"event":   "data_plane_auth_failed",
			"peer_ip": requestPeerIP(request),
			"total":   total,
		},
		"Authentication failed",
	)
}

func (handler *Handler) logDataPlaneRouteNotFound(
	request *http.Request,
	accessKeyID uint,
) {
	total, shouldLog := handler.routeNotFoundEvents.Observe()
	if !shouldLog {
		return
	}
	utils.LogPlaneBestEffort(
		handler.logger,
		logrus.WarnLevel,
		utils.LogPlaneData,
		logrus.Fields{
			"event":         "data_plane_route_not_found",
			"peer_ip":       requestPeerIP(request),
			"access_key_id": accessKeyID,
			"total":         total,
		},
		"Route not found",
	)
}
