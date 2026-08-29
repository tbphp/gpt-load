package gateway

import (
	"net/http"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/health"
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

func (handler *Handler) logDataPlaneAuthFailed(
	request *http.Request,
	accessKeyID uint,
	reason accessKeyAuthFailureReason,
) {
	total, shouldLog := handler.authFailureEvents.Observe()
	if !shouldLog {
		return
	}
	fields := logrus.Fields{
		"event":   "auth_failed",
		"peer_ip": requestPeerIP(request),
		"reason":  string(reason),
		"total":   total,
	}
	if accessKeyID != 0 {
		fields["access_key_id"] = accessKeyID
	}
	utils.LogPlaneBestEffort(
		handler.logger,
		logrus.WarnLevel,
		utils.LogPlaneData,
		fields,
		"Authentication failed",
	)
}

func (handler *Handler) logCredentialCooldown(
	credentialID uint,
	category health.FailureCategory,
	statusCode int,
) {
	utils.LogPlaneBestEffort(
		handler.logger,
		logrus.WarnLevel,
		utils.LogPlaneData,
		logrus.Fields{
			"event":         "credential_cooldown",
			"credential_id": credentialID,
			"category":      category.String(),
			"status_code":   statusCode,
		},
		"Credential entered cooldown",
	)
}

func (handler *Handler) logCredentialBlacklisted(
	credentialID uint,
	failures int,
	category health.FailureCategory,
	statusCode int,
) {
	utils.LogPlaneBestEffort(
		handler.logger,
		logrus.WarnLevel,
		utils.LogPlaneData,
		logrus.Fields{
			"event":         "credential_blacklisted",
			"credential_id": credentialID,
			"failures":      failures,
			"category":      category.String(),
			"status_code":   statusCode,
		},
		"Credential blacklisted",
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
			"event":         "route_not_found",
			"peer_ip":       requestPeerIP(request),
			"access_key_id": accessKeyID,
			"total":         total,
		},
		"Route not found",
	)
}
