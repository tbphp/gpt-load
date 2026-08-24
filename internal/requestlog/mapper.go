package requestlog

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"gpt-load/internal/platform/epochms"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

const (
	maxSummaryBytes = 4096
	maxModelBytes   = 255
	truncatedMarker = "...[truncated]"
)

func mapEvent(
	redactor *redact.Redactor,
	event telemetry.RequestEvent,
) (models.RequestLog, error) {
	completedAtMS, err := epochms.FromTime(event.CompletedAt)
	if err != nil {
		return models.RequestLog{}, fmt.Errorf("map request event completion time: %w", err)
	}
	event = normalizeModelObservation(event)
	if err := validateModelObservation(event); err != nil {
		return models.RequestLog{}, fmt.Errorf("map request event model consistency: %w", err)
	}
	if err := validateFrozenObservation(event); err != nil {
		return models.RequestLog{}, fmt.Errorf("map request event usage/pricing: %w", err)
	}

	receipt, err := canonicalPricingReceipt(event.Usage.Pricing, string(event.Usage.ChannelID))
	if err != nil {
		return models.RequestLog{}, fmt.Errorf("map request event pricing receipt: %w", err)
	}
	attempts := make([]models.RequestLogAttempt, 0, len(event.Attempts))
	for _, attempt := range event.Attempts {
		attemptCompletedAtMS := completedAtMS
		if !attempt.CompletedAt.IsZero() {
			attemptCompletedAtMS, err = epochms.FromTime(attempt.CompletedAt)
			if err != nil {
				return models.RequestLog{}, fmt.Errorf(
					"map request event attempt %d completion time: %w",
					attempt.Sequence,
					err,
				)
			}
		}
		attemptReceipt := models.JSON(nil)
		if attempt.Sequence == event.Usage.AttemptSequence {
			attemptReceipt = receipt
		}
		attempts = append(attempts, models.RequestLogAttempt{
			RequestID:             event.RequestID,
			Sequence:              attempt.Sequence,
			CompletedAtMS:         attemptCompletedAtMS,
			GroupID:               attempt.GroupID,
			GroupName:             redactIdentityValue(redactor, attempt.GroupName),
			ChannelID:             string(attempt.ChannelID),
			CredentialID:          attempt.CredentialID,
			Operation:             string(attempt.Operation),
			RouteMode:             string(attempt.RouteMode),
			UpstreamModel:         redactIdentityValue(redactor, projectModel(attempt.UpstreamModel)),
			UpstreamRequestID:     redactIdentityValue(redactor, projectModel(attempt.UpstreamRequestID)),
			DispatchState:         string(attempt.DispatchState),
			ResponseStarted:       attempt.ResponseStarted,
			UpstreamProtocol:      string(attempt.UpstreamProtocol),
			ReasoningMode:         attempt.Reasoning.Mode,
			ReasoningEffort:       attempt.Reasoning.Effort,
			ReasoningBudgetTokens: attempt.Reasoning.BudgetTokens,
			StatusCode:            attempt.StatusCode,
			DurationMs:            attempt.DurationMs,
			FailureCategory:       string(attempt.FailureCategory),
			FailureOrigin:         string(attempt.FailureOrigin),
			FailureScope:          string(attempt.FailureScope),
			RetryDirective:        string(attempt.RetryDirective),
			Effect:                string(attempt.Effect),
			RuleID:                attempt.RuleID,
			Action:                string(attempt.Action),
			WillRetry:             attempt.WillRetry,
			ErrorCode:             attempt.ErrorCode,
			ErrorSummary:          sanitizeSummary(redactor, attempt.ErrorSummary),
			Committed:             attempt.Committed,
			PricingReceipt:        attemptReceipt,
		})
	}

	result := event.Usage.Result
	pricingObservation := event.Usage.Pricing

	return models.RequestLog{
		ID:                      event.RequestID,
		CompletedAtMS:           completedAtMS,
		AccessKeyID:             event.AccessKeyID,
		GroupID:                 event.Usage.GroupID,
		ChannelID:               string(event.Usage.ChannelID),
		CredentialID:            event.Usage.CredentialID,
		Protocol:                string(event.Protocol),
		Operation:               string(event.Operation),
		ClientModel:             redactIdentityValue(redactor, projectModel(event.ClientModel)),
		UpstreamModel:           redactIdentityValue(redactor, projectModel(event.UpstreamModel)),
		UpstreamReportedModel:   redactIdentityValue(redactor, projectModel(event.UpstreamReportedModel)),
		ModelConsistency:        string(event.ModelConsistency),
		Status:                  string(event.Status),
		StatusCode:              event.StatusCode,
		Stream:                  event.Stream,
		FirstResponseMs:         event.FirstResponseMs,
		DurationMs:              event.DurationMs,
		AttemptCount:            len(attempts),
		ErrorCode:               event.ErrorCode,
		ErrorSummary:            sanitizeSummary(redactor, event.ErrorSummary),
		AffinityHit:             event.AffinityHit,
		ReasoningMode:           event.Reasoning.Mode,
		ReasoningEffort:         event.Reasoning.Effort,
		ReasoningBudgetTokens:   event.Reasoning.BudgetTokens,
		UncachedInputTokens:     result.Tokens.UncachedInput,
		OutputTokens:            result.Tokens.Output,
		CacheReadTokens:         result.Tokens.CacheRead,
		CacheWrite5MTokens:      result.Tokens.CacheWrite5M,
		CacheWrite1HTokens:      result.Tokens.CacheWrite1H,
		CacheWriteUnknownTokens: result.Tokens.CacheWriteUnknown,
		EstimatedCostNanoUSD:    pricingObservation.EstimatedCostNanoUSD,
		UsageState:              string(result.State),
		CostState:               pricingObservation.CostState,
		PricingCompleteness:     pricingObservation.PricingCompleteness,
		AttemptRows:             attempts,
	}, nil
}

func normalizeModelObservation(event telemetry.RequestEvent) telemetry.RequestEvent {
	if event.Status != telemetry.RequestStatusSuccess || event.UpstreamModel == "" {
		event.UpstreamReportedModel = ""
		event.ModelConsistency = telemetry.ModelConsistencyNotApplicable
	}
	return event
}

func validateModelObservation(event telemetry.RequestEvent) error {
	successfulModeledRequest := event.Status == telemetry.RequestStatusSuccess &&
		event.UpstreamModel != ""

	switch event.ModelConsistency {
	case telemetry.ModelConsistencyNotApplicable:
		if event.UpstreamReportedModel != "" {
			return fmt.Errorf("not-applicable observation must not carry a reported model")
		}
		if successfulModeledRequest {
			return fmt.Errorf("successful modeled request requires a model consistency result")
		}
	case telemetry.ModelConsistencyUnknown:
		if !successfulModeledRequest || event.UpstreamReportedModel != "" {
			return fmt.Errorf("unknown observation requires a successful modeled request without a reported model")
		}
	case telemetry.ModelConsistencyMatch:
		if !successfulModeledRequest || event.UpstreamReportedModel == "" ||
			event.UpstreamReportedModel != event.UpstreamModel {
			return fmt.Errorf("matching observation requires identical upstream and reported models")
		}
	case telemetry.ModelConsistencyMismatch:
		if !successfulModeledRequest || event.UpstreamReportedModel == "" ||
			event.UpstreamReportedModel == event.UpstreamModel {
			return fmt.Errorf("mismatching observation requires distinct upstream and reported models")
		}
	default:
		return fmt.Errorf("invalid model consistency state %q", event.ModelConsistency)
	}

	return nil
}

func canonicalPricingReceipt(
	observation telemetry.PricingObservation,
	channelID string,
) (models.JSON, error) {
	if observation.ReceiptJSON == "" {
		return nil, nil
	}
	var receipt pricing.Receipt
	if err := json.Unmarshal([]byte(observation.ReceiptJSON), &receipt); err != nil {
		return nil, fmt.Errorf("decode receipt: %w", err)
	}
	if err := pricing.ValidateReceipt(receipt); err != nil {
		return nil, err
	}
	if receipt.SchemaVersion != 4 || receipt.Rule != (pricing.ReceiptRule{
		ChannelID: channelID,
		ModelID:   observation.UpstreamModel,
	}) ||
		receipt.TotalNanoUSD != observation.EstimatedCostNanoUSD {
		return nil, fmt.Errorf("receipt does not match frozen pricing observation")
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf("encode receipt: %w", err)
	}
	return models.JSON(encoded), nil
}

func validateFrozenObservation(event telemetry.RequestEvent) error {
	if event.DurationMs < 0 {
		return fmt.Errorf("negative request duration")
	}
	if event.FirstResponseMs != nil {
		if !event.Stream || *event.FirstResponseMs < 0 || *event.FirstResponseMs > event.DurationMs {
			return fmt.Errorf("invalid first response duration")
		}
	}
	result := event.Usage.Result
	for _, value := range [...]int64{
		result.Tokens.UncachedInput,
		result.Tokens.CacheRead,
		result.Tokens.CacheWrite5M,
		result.Tokens.CacheWrite1H,
		result.Tokens.CacheWriteUnknown,
		result.Tokens.Output,
	} {
		if value < 0 {
			return fmt.Errorf("negative usage token value")
		}
	}
	if _, ok := usage.CheckedTotal(result.Tokens); !ok {
		return fmt.Errorf("usage token total overflows int64")
	}

	pricingObservation := event.Usage.Pricing
	costState := pricing.CostState(pricingObservation.CostState)
	completeness := pricing.Completeness(pricingObservation.PricingCompleteness)
	if err := validateFrozenPricingState(
		result.State,
		costState,
		completeness,
		pricingObservation.EstimatedCostNanoUSD,
	); err != nil {
		return err
	}
	if pricingObservation.UpstreamModel != "" &&
		!validRawModel(pricingObservation.UpstreamModel) {
		return fmt.Errorf("invalid pricing upstream model")
	}
	if costState == pricing.CostStatePriced &&
		pricingObservation.UpstreamModel == "" {
		return fmt.Errorf("priced observation requires exact identity")
	}
	for _, attempt := range event.Attempts {
		if _, err := validatedAttemptObservation(attempt); err != nil {
			return err
		}
	}

	usageCredentialID := event.Usage.CredentialID
	usageBound := event.Usage.GroupID != 0 || usageCredentialID != 0 ||
		event.Usage.ChannelID != "" ||
		event.Usage.AttemptSequence != 0
	if !usageBound {
		if result.State != usage.StateNotApplicable {
			return fmt.Errorf("billable usage requires attempt attribution")
		}
		if pricingObservation.UpstreamModel != "" {
			return fmt.Errorf("unbound no-model usage must not carry pricing identity")
		}
		return nil
	}
	if event.Usage.GroupID == 0 || usageCredentialID == 0 || event.Usage.AttemptSequence < 1 {
		return fmt.Errorf("partial usage attribution")
	}
	if !validChannelID(string(event.Usage.ChannelID)) {
		return fmt.Errorf("invalid usage channel ID")
	}

	matchingAttempts := 0
	boundModel := ""
	for _, attempt := range event.Attempts {
		attemptCredentialID, _ := validatedAttemptObservation(attempt)
		if attempt.Sequence == event.Usage.AttemptSequence &&
			attempt.GroupID == event.Usage.GroupID &&
			attempt.ChannelID == event.Usage.ChannelID &&
			attemptCredentialID == usageCredentialID {
			matchingAttempts++
			boundModel = attempt.UpstreamModel
		}
	}
	if matchingAttempts != 1 {
		return fmt.Errorf("usage attribution must match exactly one attempt")
	}
	if !validRawModelOrEmpty(boundModel) || !validRawModelOrEmpty(event.UpstreamModel) {
		return fmt.Errorf("invalid selected upstream model")
	}
	if event.UpstreamModel != boundModel ||
		pricingObservation.UpstreamModel != boundModel {
		return fmt.Errorf("inconsistent bound upstream model")
	}
	return nil
}

func validatedAttemptObservation(attempt telemetry.Attempt) (uint, error) {
	credentialID := attempt.CredentialID
	if (credentialID == 0) != (attempt.ChannelID == "") {
		return 0, fmt.Errorf("invalid attempt %d identity", attempt.Sequence)
	}
	if !validRawModelOrEmpty(attempt.UpstreamRequestID) {
		return 0, fmt.Errorf("invalid attempt %d upstream request ID", attempt.Sequence)
	}
	if (credentialID != 0 || attempt.ChannelID != "") &&
		(!validChannelID(string(attempt.ChannelID)) || !attempt.Operation.Valid() ||
			!attempt.RouteMode.Valid() || !attempt.DispatchState.Valid()) {
		return 0, fmt.Errorf("invalid attempt %d execution observation", attempt.Sequence)
	}
	if attempt.UpstreamProtocol != "" && !attempt.UpstreamProtocol.Valid() {
		return 0, fmt.Errorf("invalid attempt %d upstream protocol", attempt.Sequence)
	}
	return credentialID, nil
}

func validChannelID(channelID string) bool {
	return channelID != "" && len(channelID) <= 64 && validRawModel(channelID)
}

func validateFrozenPricingState(
	usageState usage.State,
	costState pricing.CostState,
	completeness pricing.Completeness,
	estimatedCostNanoUSD int64,
) error {
	if estimatedCostNanoUSD < 0 {
		return fmt.Errorf("invalid estimated cost")
	}
	switch usageState {
	case usage.StateNotApplicable:
		if costState != pricing.CostStateNotApplicable ||
			completeness != pricing.CompletenessNotApplicable ||
			estimatedCostNanoUSD != 0 {
			return fmt.Errorf("invalid not-applicable pricing observation")
		}
	case usage.StateMissing:
		if costState != pricing.CostStateUnpriced ||
			completeness != pricing.CompletenessUnavailable ||
			estimatedCostNanoUSD != 0 {
			return fmt.Errorf("invalid missing-usage pricing observation")
		}
	case usage.StateComplete, usage.StatePartial:
		switch costState {
		case pricing.CostStateUnpriced:
			if completeness != pricing.CompletenessUnavailable || estimatedCostNanoUSD != 0 {
				return fmt.Errorf("invalid unpriced pricing observation")
			}
		case pricing.CostStatePriced:
			if completeness != pricing.CompletenessComplete &&
				completeness != pricing.CompletenessPartial {
				return fmt.Errorf("invalid priced completeness")
			}
		default:
			return fmt.Errorf("invalid billable cost state")
		}
	default:
		return fmt.Errorf("invalid usage state")
	}
	return nil
}

func validRawModelOrEmpty(model string) bool {
	return model == "" || validRawModel(model)
}

func validRawModel(model string) bool {
	if len(model) > maxModelBytes || !utf8.ValidString(model) ||
		strings.ToValidUTF8(model, "\uFFFD") != model ||
		strings.TrimSpace(model) != model {
		return false
	}
	for _, value := range model {
		if unicode.IsControl(value) {
			return false
		}
	}
	return true
}

func redactIdentityValue(redactor *redact.Redactor, value string) string {
	if redactor == nil {
		return value
	}
	return redactor.String(value)
}

func projectModel(model string) string {
	model = strings.ToValidUTF8(model, "\uFFFD")
	if len(model) <= maxModelBytes {
		return model
	}

	prefixBytes := maxModelBytes - len(truncatedMarker)
	for prefixBytes > 0 && !utf8.ValidString(model[:prefixBytes]) {
		prefixBytes--
	}
	return model[:prefixBytes] + truncatedMarker
}

func sanitizeSummary(redactor *redact.Redactor, summary string) string {
	return sanitizeSummaryLimit(redactor, summary, maxSummaryBytes)
}

func sanitizeSummaryLimit(
	redactor *redact.Redactor,
	summary string,
	limit int,
) string {
	summary = strings.ToValidUTF8(summary, "\uFFFD")
	summary = strings.Join(strings.Fields(summary), " ")
	if redactor != nil {
		summary = redactor.String(summary)
	}
	if len(summary) <= limit {
		return summary
	}

	prefixBytes := limit - len(truncatedMarker)
	for prefixBytes > 0 && !utf8.ValidString(summary[:prefixBytes]) {
		prefixBytes--
	}
	return summary[:prefixBytes] + truncatedMarker
}
