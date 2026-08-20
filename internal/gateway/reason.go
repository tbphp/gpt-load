package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/pricing"
)

type reason struct {
	Status  int
	Code    string
	Message string
}

var (
	reasonInvalidAccessKey              = reason{Status: http.StatusUnauthorized, Code: "invalid_access_key", Message: "Invalid access key."}
	reasonEndpointNotFound              = reason{Status: http.StatusNotFound, Code: "protocol_endpoint_not_found", Message: "Protocol endpoint not found."}
	reasonMethodNotAllowed              = reason{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Message: "Method not allowed."}
	reasonInvalidProtocolRequest        = reason{Status: http.StatusBadRequest, Code: "invalid_protocol_request", Message: "Invalid protocol request."}
	reasonModelRequiredByFilter         = reason{Status: http.StatusBadRequest, Code: "model_required_by_filter", Message: "A model is required by the access key filter."}
	reasonNoCandidate                   = reason{Status: http.StatusServiceUnavailable, Code: "no_available_candidate", Message: "No available upstream candidate."}
	reasonUpstreamConnect               = reason{Status: http.StatusBadGateway, Code: "upstream_connect_failed", Message: "Could not connect to an upstream service."}
	reasonUpstreamTimeout               = reason{Status: http.StatusGatewayTimeout, Code: "upstream_timeout", Message: "Upstream request timed out."}
	reasonUpstreamProtocol              = reason{Status: http.StatusBadGateway, Code: "upstream_protocol_error", Message: "Upstream returned an unsupported response."}
	reasonProtocolConversionUnsupported = reason{Status: http.StatusUnprocessableEntity, Code: "protocol_conversion_unsupported", Message: "No upstream target could preserve or convert the request."}
	reasonRequestTooLarge               = reason{Status: http.StatusRequestEntityTooLarge, Code: "request_too_large", Message: "Request body is too large."}
	reasonUnsupportedContentEncoding    = reason{
		Status:  http.StatusUnsupportedMediaType,
		Code:    "unsupported_content_encoding",
		Message: "Unsupported Content-Encoding.",
	}
	reasonInvalidContentEncoding = reason{
		Status:  http.StatusBadRequest,
		Code:    "invalid_content_encoding",
		Message: "Invalid encoded request body.",
	}
	reasonNotAcceptable = reason{
		Status:  http.StatusNotAcceptable,
		Code:    "not_acceptable",
		Message: "The gateway can only return an identity-encoded response.",
	}
	reasonModelListTooLarge    = reason{Status: http.StatusInternalServerError, Code: "model_list_too_large", Message: "Model list is too large."}
	reasonAccessKeyRateLimited = reason{
		Status:  http.StatusTooManyRequests,
		Code:    "access_key_rate_limited",
		Message: "Access key rate limit exceeded.",
	}
	reasonAccessKeyCostLimitExceeded = reason{
		Status:  http.StatusTooManyRequests,
		Code:    "access_key_cost_limit_exceeded",
		Message: "Access key cost limit exceeded.",
	}
	reasonConfigurationChanged = reason{
		Status:  http.StatusServiceUnavailable,
		Code:    "configuration_changed",
		Message: "Configuration changed; retry the request.",
	}
)

type accessKeyCostLimitRuleError struct {
	ID             uint             `json:"id"`
	Kind           accessquota.Kind `json:"kind"`
	LimitUSD       string           `json:"limit_usd"`
	UsedUSD        string           `json:"used_usd"`
	PeriodSeconds  int64            `json:"period_seconds,omitempty"`
	WindowEndsAtMS *int64           `json:"window_ends_at_ms,omitempty"`
}

type accessKeyCostLimitClientError struct {
	Type     string `json:"type"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	ResetsAt *int64 `json:"resets_at,omitempty"`
}

func (handler *Handler) completeAccessQuotaReason(
	context *gin.Context,
	recorder *requestRecorder,
	decision accessquota.Decision,
) {
	recorder.completeReason(reasonAccessKeyCostLimitExceeded)
	if err := handler.writeAccessQuotaReason(context, decision); err != nil {
		handler.completeWriteTerminal(context, recorder, reasonAccessKeyCostLimitExceeded.Status)
	}
}

func (handler *Handler) writeAccessQuotaReason(
	context *gin.Context,
	decision accessquota.Decision,
) error {
	blocking := make([]accessKeyCostLimitRuleError, 0, len(decision.BlockingRules))
	for _, rule := range decision.BlockingRules {
		blocking = append(blocking, accessKeyCostLimitRuleError{
			ID: rule.ID, Kind: rule.Kind,
			LimitUSD:       pricing.FormatUSD(pricing.NanoUSD(rule.LimitNanoUSD)),
			UsedUSD:        pricing.FormatUSD(pricing.NanoUSD(rule.UsedNanoUSD)),
			PeriodSeconds:  rule.PeriodSeconds,
			WindowEndsAtMS: cloneReasonInt64(rule.WindowEndsAtMS),
		})
	}
	message := accessKeyCostLimitMessage(decision)
	var resetsAt *int64
	if decision.Recoverable && decision.NextAvailableAtMS != nil {
		seconds := (*decision.NextAvailableAtMS + 999) / 1_000
		resetsAt = &seconds
	}
	body, err := json.Marshal(struct {
		Code    string                        `json:"code"`
		Message string                        `json:"message"`
		Error   accessKeyCostLimitClientError `json:"error"`
		Data    struct {
			Recoverable       bool                          `json:"recoverable"`
			NextAvailableAtMS *int64                        `json:"next_available_at_ms"`
			BlockingRules     []accessKeyCostLimitRuleError `json:"blocking_rules"`
		} `json:"data"`
	}{
		Code:    reasonAccessKeyCostLimitExceeded.Code,
		Message: message,
		Error: accessKeyCostLimitClientError{
			Type: "usage_limit_reached", Code: reasonAccessKeyCostLimitExceeded.Code,
			Message: message, ResetsAt: resetsAt,
		},
		Data: struct {
			Recoverable       bool                          `json:"recoverable"`
			NextAvailableAtMS *int64                        `json:"next_available_at_ms"`
			BlockingRules     []accessKeyCostLimitRuleError `json:"blocking_rules"`
		}{
			Recoverable:       decision.Recoverable,
			NextAvailableAtMS: cloneReasonInt64(decision.NextAvailableAtMS),
			BlockingRules:     blocking,
		},
	})
	if err != nil {
		return err
	}
	headers := http.Header{"Content-Type": {"application/json; charset=utf-8"}}
	if decision.Recoverable && decision.NextAvailableAtMS != nil {
		deltaMS := *decision.NextAvailableAtMS - handler.quotaNow().UnixMilli()
		seconds := (deltaMS + 999) / 1000
		if seconds < 1 {
			seconds = 1
		}
		headers.Set("Retry-After", strconv.FormatInt(seconds, 10))
	}
	return handler.writeBufferedResponse(
		context,
		reasonAccessKeyCostLimitExceeded.Status,
		headers,
		body,
	)
}

func accessKeyCostLimitMessage(decision accessquota.Decision) string {
	details := make([]string, 0, len(decision.BlockingRules))
	for _, rule := range decision.BlockingRules {
		if rule.Kind == accessquota.KindTotal {
			details = append(details, "total limit (does not reset automatically)")
			continue
		}
		label := accessKeyCostLimitPeriodLabel(rule.PeriodSeconds) + " limit"
		if rule.WindowEndsAtMS != nil {
			label += " (available again at " +
				time.UnixMilli(*rule.WindowEndsAtMS).UTC().Format(time.RFC3339) + ")"
		}
		details = append(details, label)
	}
	if len(details) == 0 {
		return reasonAccessKeyCostLimitExceeded.Message
	}
	return "Access key estimated cost limit exceeded: " + strings.Join(details, "; ") + "."
}

func accessKeyCostLimitPeriodLabel(seconds int64) string {
	units := []struct {
		seconds int64
		name    string
	}{
		{seconds: 86_400, name: "day"},
		{seconds: 3_600, name: "hour"},
		{seconds: 60, name: "minute"},
	}
	for _, unit := range units {
		if seconds >= unit.seconds && seconds%unit.seconds == 0 {
			count := seconds / unit.seconds
			return fmt.Sprintf("%d-%s", count, unit.name)
		}
	}
	return fmt.Sprintf("%d-second", seconds)
}

func cloneReasonInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (handler *Handler) completeConfigurationChanged(
	context *gin.Context,
	recorder *requestRecorder,
) {
	context.Writer.Header().Set("Retry-After", "1")
	handler.completeReason(context, recorder, reasonConfigurationChanged)
}

func (handler *Handler) writeReason(context *gin.Context, value reason) error {
	body, err := json.Marshal(struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: value.Code, Message: value.Message})
	if err != nil {
		return err
	}
	return handler.writeBufferedResponse(
		context,
		value.Status,
		http.Header{"Content-Type": {"application/json; charset=utf-8"}},
		body,
	)
}
