package control

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/utils"
)

const (
	mutationErrorCodeContextKey = "gpt-load.control.mutation-error-code"
	mutationLocatorContextKey   = "gpt-load.control.mutation-locator"
)

type mutationAuditDescriptor struct {
	operation    string
	resourceType string
	locator      func(*gin.Context) string
}

func newMutationDescriptor(
	operation string,
	resourceType string,
	locator func(*gin.Context) string,
) mutationAuditDescriptor {
	return mutationAuditDescriptor{
		operation:    operation,
		resourceType: resourceType,
		locator:      locator,
	}
}

func setMutationErrorCode(c *gin.Context, code string) {
	if c != nil {
		c.Set(mutationErrorCodeContextKey, code)
	}
}

func setMutationResourceLocator(c *gin.Context, locator string) {
	if c != nil {
		c.Set(mutationLocatorContextKey, locator)
	}
}

func classifyMutationOutcome(statusCode int, errorCode string) string {
	if statusCode >= http.StatusOK &&
		statusCode < http.StatusMultipleChoices {
		return "succeeded"
	}
	switch errorCode {
	case app_errors.ErrControlRecoveryPending.Code:
		return "blocked"
	case app_errors.ErrControlOperationIncomplete.Code:
		return "incomplete"
	}
	if statusCode >= http.StatusBadRequest &&
		statusCode < http.StatusInternalServerError {
		return "rejected"
	}
	return "failed"
}

func staticMutationLocator(value string) func(*gin.Context) string {
	return func(*gin.Context) string {
		return value
	}
}

func groupMutationLocator(c *gin.Context) string {
	return "group:" + mutationID(c.Param("group_id"))
}

func groupCredentialMutationLocator(c *gin.Context) string {
	return "group:" + mutationID(c.Param("group_id")) +
		"/credential:" + mutationID(c.Param("credential_id"))
}

func groupCredentialsMutationLocator(c *gin.Context) string {
	return "group:" + mutationID(c.Param("group_id")) + "/credentials"
}

func credentialStageMutationLocator(c *gin.Context) string {
	value := c.Param("stage_id")
	if validateIdempotencyKey(value) != nil {
		return "credential-stage:unknown"
	}
	return "credential-stage:" + value
}

func accessKeyMutationLocator(c *gin.Context) string {
	return "access-key:" + mutationID(c.Param("id"))
}

func modelPriceMutationLocator(c *gin.Context) string {
	id, err := parseModelPriceRowID(c.Param("id"))
	if err != nil {
		return "model-price:unknown"
	}
	return fmt.Sprintf("model-price:%d", id)
}

func mutationID(raw string) string {
	parsed, err := strconv.ParseUint(raw, 10, strconv.IntSize)
	if err != nil || parsed == 0 {
		return "unknown"
	}
	return strconv.FormatUint(parsed, 10)
}

func (s *Server) auditMutation(
	descriptor mutationAuditDescriptor,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		initialLocator := descriptor.locator(c)
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logControlMutation(
					c,
					descriptor,
					initialLocator,
					"failed",
					http.StatusInternalServerError,
					app_errors.ErrInternalServer.Code,
				)
				panic(recovered)
			}

			statusCode := c.Writer.Status()
			errorCode := mutationContextString(
				c,
				mutationErrorCodeContextKey,
			)
			outcome := classifyMutationOutcome(statusCode, errorCode)
			if outcome == "succeeded" {
				errorCode = ""
			} else if outcome == "failed" && errorCode == "" {
				errorCode = app_errors.ErrInternalServer.Code
			}
			s.logControlMutation(
				c,
				descriptor,
				initialLocator,
				outcome,
				statusCode,
				errorCode,
			)
		}()
		c.Next()
	}
}

func (s *Server) logControlMutation(
	c *gin.Context,
	descriptor mutationAuditDescriptor,
	initialLocator string,
	outcome string,
	statusCode int,
	errorCode string,
) {
	resourceLocator := initialLocator
	if override := mutationContextString(
		c,
		mutationLocatorContextKey,
	); override != "" {
		resourceLocator = override
	}
	peer := mutationContextString(c, controlPeerContextKey)
	level := logrus.WarnLevel
	if outcome == "succeeded" {
		level = logrus.InfoLevel
	}
	utils.LogPlaneBestEffort(
		s.logger,
		level,
		utils.LogPlaneControl,
		logrus.Fields{
			"event":            "mutation",
			"peer_ip":          peer,
			"operation":        descriptor.operation,
			"resource_type":    descriptor.resourceType,
			"resource_locator": resourceLocator,
			"outcome":          outcome,
			"status_code":      statusCode,
			"error_code":       errorCode,
		},
		"Mutation completed",
	)
}

func mutationContextString(c *gin.Context, key string) string {
	if c == nil {
		return ""
	}
	value, ok := c.Get(key)
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return text
}
