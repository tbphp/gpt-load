package control

import (
	"crypto/sha256"
	"net/http"
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

const controlPrincipalContextKey = "gpt-load.control.principal"

type controlPrincipalType string

const (
	controlPrincipalAdmin     controlPrincipalType = "admin"
	controlPrincipalAccessKey controlPrincipalType = "access_key"
)

type controlPrincipal struct {
	Type        controlPrincipalType
	AccessKeyID uint
}

type authLockedData struct {
	RetryAfterSeconds int64 `json:"retry_after_seconds"`
}

type authSessionResponse struct {
	Authenticated bool                 `json:"authenticated"`
	PrincipalType controlPrincipalType `json:"principal_type"`
}

var accessKeyControlRoutes = map[string]struct{}{
	"/api/auth/session":     {},
	"/api/home":             {},
	"/api/home/statistics":  {},
	"/api/models":           {},
	"/api/usage":            {},
	"/api/logs":             {},
	"/api/logs/:request_id": {},
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
		adminMatches := s.compareDigest(requestDigest[:], s.authDigest[:]) == 1
		accessKeyID, accessKeyMatches := s.matchAccessKey(token)
		credentialValid := formatValid && adminMatches != accessKeyMatches
		principal := controlPrincipal{}
		if credentialValid {
			if adminMatches {
				principal.Type = controlPrincipalAdmin
			} else {
				principal.Type = controlPrincipalAccessKey
				principal.AccessKeyID = accessKeyID
			}
		}
		var decision authDecision
		if principal.Type == controlPrincipalAccessKey {
			// AccessKey 成功只授权本次请求，不清除同一来源的管理密钥失败记录。
			decision = authDecision{authorized: true}
		} else {
			decision = s.authFailures.evaluate(peer, credentialValid)
		}
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
		c.Set(controlPrincipalContextKey, principal)
		if principal.Type == controlPrincipalAccessKey {
			c.Header("Cache-Control", "no-store")
		}
		if !principalCanAccessControlRoute(principal, c.Request.Method, c.FullPath()) {
			response.ErrorI18nFromAPIError(
				c,
				app_errors.ErrForbidden,
				"auth.forbidden",
			)
			c.Abort()
			return
		}
		c.Next()
	}
}

func (s *Server) matchAccessKey(token string) (uint, bool) {
	if s == nil || s.service == nil || s.service.encryption == nil ||
		s.service.manager == nil {
		return 0, false
	}
	fingerprint := s.service.encryption.Hash(token)
	if fingerprint == "" {
		return 0, false
	}
	snapshot := s.service.manager.Current()
	if snapshot == nil {
		return 0, false
	}
	accessKey, ok := snapshot.AccessKeysByHash[fingerprint]
	if !ok || accessKey.ID == 0 {
		return 0, false
	}
	return accessKey.ID, true
}

func principalCanAccessControlRoute(
	principal controlPrincipal,
	method string,
	path string,
) bool {
	switch principal.Type {
	case controlPrincipalAdmin:
		return true
	case controlPrincipalAccessKey:
		if method != http.MethodGet {
			return false
		}
		_, ok := accessKeyControlRoutes[path]
		return ok
	default:
		return false
	}
}

func currentControlPrincipal(c *gin.Context) (controlPrincipal, bool) {
	value, exists := c.Get(controlPrincipalContextKey)
	if !exists {
		return controlPrincipal{}, false
	}
	principal, ok := value.(controlPrincipal)
	return principal, ok
}

func currentAccessKeyID(c *gin.Context) (uint, bool) {
	principal, ok := currentControlPrincipal(c)
	if !ok || principal.Type != controlPrincipalAccessKey || principal.AccessKeyID == 0 {
		return 0, false
	}
	return principal.AccessKeyID, true
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
			"event":   "auth_failed",
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
			"event":               "auth_locked",
			"peer_ip":             peer,
			"retry_after_seconds": retryAfterSeconds(retryAfter),
		},
		"Peer locked out",
	)
}

func (s *Server) handleAuthSession(c *gin.Context) {
	principal, ok := currentControlPrincipal(c)
	if !ok {
		writeServiceError(c, "auth_session", app_errors.ErrInternalServer)
		return
	}
	response.SuccessI18n(
		c,
		"common.success",
		authSessionResponse{
			Authenticated: true,
			PrincipalType: principal.Type,
		},
	)
}
