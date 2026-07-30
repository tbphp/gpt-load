package control

import (
	"crypto/sha256"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/platform/utils"
)

const controlPeerContextKey = "gpt-load.control.peer-ip"

type authLockedData struct {
	RetryAfterSeconds int64 `json:"retry_after_seconds"`
}

type authSessionResponse struct {
	Authenticated bool `json:"authenticated"`
}

func retryAfterSeconds(remaining time.Duration) int64 {
	seconds := int64((remaining + time.Second - 1) / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

func (s *Server) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		peer, err := utils.NormalizePeerIP(c.Request.RemoteAddr)
		if err != nil {
			logServiceError("authenticate_peer", err, app_errors.ErrInternalServer.Code)
			response.ErrorI18nFromAPIError(
				c,
				app_errors.ErrInternalServer,
				"internal_error",
			)
			c.Abort()
			return
		}

		fields := strings.Fields(c.GetHeader("Authorization"))
		formatValid := len(fields) == 2 &&
			strings.EqualFold(fields[0], "Bearer")
		token := ""
		if formatValid {
			token = fields[1]
		}
		requestDigest := sha256.Sum256([]byte(token))
		matches := s.compareDigest(requestDigest[:], s.authDigest[:]) == 1
		credentialValid := formatValid && matches
		decision := s.authFailures.evaluate(peer, credentialValid)
		if !credentialValid {
			s.logControlAuthFailed(peer)
		}
		if decision.newlyLocked {
			s.logControlPeerLocked(peer, decision.retryAfter)
		}

		if decision.retryAfter > 0 {
			seconds := retryAfterSeconds(decision.retryAfter)
			c.Header("Retry-After", strconv.FormatInt(seconds, 10))
			apiErr := app_errors.NewAPIErrorWithData(
				app_errors.ErrAuthLocked,
				authLockedData{RetryAfterSeconds: seconds},
			)
			response.ErrorI18nFromAPIError(c, apiErr, "auth.locked")
			c.Abort()
			return
		}
		if !decision.authorized {
			response.ErrorI18nFromAPIError(
				c,
				app_errors.ErrUnauthorized,
				"auth.invalid_key",
			)
			c.Abort()
			return
		}
		c.Set(controlPeerContextKey, peer)
		c.Next()
	}
}

func (s *Server) logControlAuthFailed(peer string) {
	total, shouldLog := s.authFailureEvents.Observe()
	if !shouldLog {
		return
	}
	utils.LogPlaneBestEffort(
		s.logger,
		logrus.WarnLevel,
		utils.LogPlaneControl,
		logrus.Fields{
			"event":   "control_plane_auth_failed",
			"peer_ip": peer,
			"total":   total,
		},
		"Authentication failed",
	)
}

func (s *Server) logControlPeerLocked(
	peer string,
	retryAfter time.Duration,
) {
	utils.LogPlaneBestEffort(
		s.logger,
		logrus.WarnLevel,
		utils.LogPlaneControl,
		logrus.Fields{
			"event":               "control_plane_auth_locked",
			"peer_ip":             peer,
			"retry_after_seconds": retryAfterSeconds(retryAfter),
		},
		"Peer locked out",
	)
}

func (s *Server) handleAuthSession(c *gin.Context) {
	response.SuccessI18n(
		c,
		"common.success",
		authSessionResponse{Authenticated: true},
	)
}
