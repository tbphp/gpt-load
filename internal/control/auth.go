package control

import (
	"crypto/sha256"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
)

type authLockedData struct {
	RetryAfterSeconds int64 `json:"retry_after_seconds"`
}

type authSessionResponse struct {
	Authenticated bool `json:"authenticated"`
}

func normalizePeerIP(remoteAddr string) (string, error) {
	endpoint, err := netip.ParseAddrPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		return "", fmt.Errorf("parse remote peer endpoint: %w", err)
	}
	return endpoint.Addr().Unmap().WithZone("").String(), nil
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
		peer, err := normalizePeerIP(c.Request.RemoteAddr)
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

		decision := s.authFailures.evaluate(peer, func() bool {
			fields := strings.Fields(c.GetHeader("Authorization"))
			token := ""
			if len(fields) == 2 {
				token = fields[1]
			}
			requestDigest := sha256.Sum256([]byte(token))
			formatValid := len(fields) == 2 &&
				strings.EqualFold(fields[0], "Bearer")
			matches := s.compareDigest(requestDigest[:], s.authDigest[:]) == 1
			return formatValid && matches
		})

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
		c.Next()
	}
}

func (s *Server) handleAuthSession(c *gin.Context) {
	response.SuccessI18n(
		c,
		"common.success",
		authSessionResponse{Authenticated: true},
	)
}
