package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/codex"
	"gpt-load/internal/platform/canonicaljson"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/storage/models"
)

type ObservationResetCredit struct {
	ExpiresAtMS int64 `json:"expires_at_ms"`
}

type ResetCreditConsumeResponse struct {
	Status             string                         `json:"status"`
	WindowsReset       int                            `json:"windows_reset"`
	RedeemedAtMS       *int64                         `json:"redeemed_at_ms,omitempty"`
	Observation        *CredentialObservationResponse `json:"observation,omitempty"`
	ObservationPending bool                           `json:"observation_pending,omitempty"`
	Replayed           bool                           `json:"replayed"`
}

type storedResetCreditResult struct {
	Status                   string `json:"status"`
	WindowsReset             int    `json:"windows_reset"`
	RedeemedAtMS             *int64 `json:"redeemed_at_ms,omitempty"`
	ObservationVersionBefore uint64 `json:"observation_version_before"`
}

type resetCreditUpstreamPayload struct {
	Code         string `json:"code"`
	WindowsReset int    `json:"windows_reset"`
	Credit       *struct {
		Status     string `json:"status"`
		RedeemedAt string `json:"redeemed_at"`
	} `json:"credit"`
}

func (s *Service) ConsumeCredentialResetCredit(
	ctx context.Context,
	groupID uint,
	credentialID uint,
	idempotencyKey string,
) (ResetCreditConsumeResponse, error) {
	if groupID == 0 || credentialID == 0 {
		return ResetCreditConsumeResponse{}, app_errors.ErrValidation
	}
	if validateIdempotencyKey(idempotencyKey) != nil {
		return ResetCreditConsumeResponse{}, app_errors.ErrInvalidIdempotencyKey
	}
	group, credential, previousObservation, err := s.loadObservationTarget(ctx, groupID, credentialID)
	if err != nil {
		return ResetCreditConsumeResponse{}, err
	}
	if replay, found, replayErr := s.replayResetCreditOperationIfExists(
		ctx,
		groupID,
		credentialID,
		credential.IdentityFingerprint,
		idempotencyKey,
	); found {
		return replay, replayErr
	}
	codexCredential, err := s.prepareStoredCodexCredential(ctx, group, credential)
	if err != nil {
		return ResetCreditConsumeResponse{}, err
	}

	operation, replayed, err := s.beginResetCreditOperation(
		ctx,
		groupID,
		credentialID,
		credential.IdentityFingerprint,
		idempotencyKey,
	)
	if err != nil {
		return ResetCreditConsumeResponse{}, err
	}
	if replayed {
		return s.replayResetCreditOperation(ctx, groupID, credentialID, operation)
	}

	if s.consumeCodexResetCredit == nil {
		_ = s.finishResetCreditOperation(operation.IdempotencyKey, models.CredentialResetOperationOutcomeUnknown, nil, app_errors.ErrResetCreditOutcomeUnknown.Code)
		return ResetCreditConsumeResponse{}, app_errors.ErrResetCreditOutcomeUnknown
	}
	callContext, cancel := context.WithTimeout(ctx, defaultSubscriptionControlTimeout)
	upstream, consumeErr := s.consumeCodexResetCredit(callContext, codexCredential, operation.RedeemRequestID)
	cancel()
	if consumeErr != nil {
		state, apiErr := classifyResetCreditConsumeError(consumeErr)
		if persistErr := s.finishResetCreditOperation(operation.IdempotencyKey, state, nil, apiErr.Code); persistErr != nil {
			return ResetCreditConsumeResponse{}, app_errors.ErrResetCreditOutcomeUnknown
		}
		return ResetCreditConsumeResponse{}, apiErr
	}
	result, err := normalizeResetCreditConsumeResult(upstream.Payload)
	if err != nil {
		_ = s.finishResetCreditOperation(operation.IdempotencyKey, models.CredentialResetOperationOutcomeUnknown, nil, app_errors.ErrResetCreditOutcomeUnknown.Code)
		return ResetCreditConsumeResponse{}, app_errors.ErrResetCreditOutcomeUnknown
	}
	runtimeRestored := s.restoreCredentialRuntimeAfterReset(credentialID)
	result.ObservationVersionBefore = previousObservation.ObservationVersion
	canonicalResult, err := canonicaljson.Marshal(result)
	if err != nil || s.finishResetCreditOperation(
		operation.IdempotencyKey,
		models.CredentialResetOperationSucceeded,
		canonicalResult,
		"",
	) != nil {
		return ResetCreditConsumeResponse{}, app_errors.ErrResetCreditOutcomeUnknown
	}
	if !runtimeRestored {
		utils.LogPlaneBestEffort(
			logrus.StandardLogger(),
			logrus.WarnLevel,
			utils.LogPlaneControl,
			logrus.Fields{"credential_id": credentialID, "group_id": groupID},
			"Reset credit was consumed but credential runtime health could not be restored",
		)
	}

	response := resetCreditResponse(result, false)
	observation, observationErr := s.refreshCredentialObservation(
		ctx,
		groupID,
		credentialID,
		observationRefreshAfterMutation,
	)
	response.Observation = &observation
	response.ObservationPending = observationErr != nil
	return response, nil
}

func (s *Service) restoreCredentialRuntimeAfterReset(credentialID uint) bool {
	if credentialID == 0 || s.registry == nil || s.stats == nil {
		return false
	}
	restored := false
	apply := func() {
		now := s.now().UTC()
		stats := s.stats.Snapshot(credentialID, now)
		stats.ConsecutiveFailure = 0
		stats.ConsecutiveProblem = 0
		stats.LastFailureCategory = 0
		stats.LastStatusCode = 0
		if !s.registry.RestoreRuntimeState(credentialID, calculateAutoWeight(stats)) {
			return
		}
		s.stats.ClearProblemState(credentialID)
		restored = true
	}
	if s.mutations == nil {
		apply()
	} else {
		s.mutations.Do(credentialID, apply)
	}
	return restored
}

func (s *Service) beginResetCreditOperation(
	ctx context.Context,
	groupID uint,
	credentialID uint,
	identityFingerprint string,
	idempotencyKey string,
) (models.CredentialResetOperation, bool, error) {
	digest := resetCreditRequestDigest(groupID, credentialID, identityFingerprint)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	nowMS := s.now().UTC().UnixMilli()

	var existing models.CredentialResetOperation
	err := s.db.WithContext(ctx).Where("idempotency_key = ?", idempotencyKey).Take(&existing).Error
	if err == nil {
		if !bytes.Equal(existing.RequestDigest, digest[:]) ||
			existing.GroupID != groupID || existing.CredentialID != credentialID {
			return models.CredentialResetOperation{}, false, app_errors.ErrIdempotencyKeyReused
		}
		if resetCreditOperationCanRetry(existing, nowMS) {
			result := s.db.WithContext(ctx).Model(&models.CredentialResetOperation{}).
				Where("idempotency_key = ? AND state = ? AND updated_at_ms = ?", existing.IdempotencyKey, existing.State, existing.UpdatedAtMS).
				Updates(map[string]any{
					"state":           models.CredentialResetOperationPrepared,
					"result_json":     models.JSON(nil),
					"error_code":      "",
					"updated_at_ms":   nowMS,
					"completed_at_ms": nil,
				})
			if result.Error != nil {
				return models.CredentialResetOperation{}, false, app_errors.ParseDBError(result.Error)
			}
			if result.RowsAffected != 1 {
				return models.CredentialResetOperation{}, false, app_errors.ErrResetCreditOutcomeUnknown
			}
			existing.State = models.CredentialResetOperationPrepared
			existing.ResultJSON = nil
			existing.ErrorCode = ""
			existing.UpdatedAtMS = nowMS
			existing.CompletedAtMS = nil
			return existing, false, nil
		}
		replayed, existingErr := classifyExistingResetCreditOperation(existing, digest, groupID, credentialID)
		if existingErr != nil {
			return models.CredentialResetOperation{}, false, existingErr
		}
		return existing, replayed, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.CredentialResetOperation{}, false, app_errors.ParseDBError(err)
	}
	if !s.resetCreditOperationAllowsNext(ctx, credentialID, identityFingerprint) {
		return models.CredentialResetOperation{}, false, app_errors.ErrResetCreditOutcomeUnknown
	}
	operation := models.CredentialResetOperation{
		IdempotencyKey: idempotencyKey, RequestDigest: append([]byte(nil), digest[:]...),
		GroupID: groupID, CredentialID: credentialID, RedeemRequestID: idempotencyKey,
		State: models.CredentialResetOperationPrepared, CreatedAtMS: nowMS, UpdatedAtMS: nowMS,
	}
	if err := s.db.WithContext(ctx).Create(&operation).Error; err != nil {
		return models.CredentialResetOperation{}, false, app_errors.ParseDBError(err)
	}
	return operation, false, nil
}

func (s *Service) replayResetCreditOperationIfExists(
	ctx context.Context,
	groupID uint,
	credentialID uint,
	identityFingerprint string,
	idempotencyKey string,
) (ResetCreditConsumeResponse, bool, error) {
	var existing models.CredentialResetOperation
	err := s.db.WithContext(ctx).Where("idempotency_key = ?", idempotencyKey).Take(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ResetCreditConsumeResponse{}, false, nil
	}
	if err != nil {
		return ResetCreditConsumeResponse{}, true, app_errors.ParseDBError(err)
	}
	digest := resetCreditRequestDigest(groupID, credentialID, identityFingerprint)
	if !bytes.Equal(existing.RequestDigest, digest[:]) ||
		existing.GroupID != groupID || existing.CredentialID != credentialID {
		return ResetCreditConsumeResponse{}, true, app_errors.ErrIdempotencyKeyReused
	}
	if resetCreditOperationCanRetry(existing, s.now().UTC().UnixMilli()) {
		return ResetCreditConsumeResponse{}, false, nil
	}
	replayed, err := classifyExistingResetCreditOperation(existing, digest, groupID, credentialID)
	if err != nil {
		return ResetCreditConsumeResponse{}, true, err
	}
	if !replayed {
		return ResetCreditConsumeResponse{}, true, app_errors.ErrResetCreditOutcomeUnknown
	}
	response, err := s.replayResetCreditOperation(ctx, groupID, credentialID, existing)
	return response, true, err
}

func (s *Service) replayResetCreditOperation(
	ctx context.Context,
	groupID uint,
	credentialID uint,
	operation models.CredentialResetOperation,
) (ResetCreditConsumeResponse, error) {
	result, err := decodeStoredResetCreditResult(operation.ResultJSON)
	if err != nil {
		return ResetCreditConsumeResponse{}, app_errors.ErrResetCreditOutcomeUnknown
	}
	response := resetCreditResponse(result, true)
	if observation, observationErr := s.GetCredentialObservation(ctx, groupID, credentialID); observationErr == nil {
		response.Observation = &observation
	}
	return response, nil
}

func resetCreditRequestDigest(groupID, credentialID uint, identityFingerprint string) [sha256.Size]byte {
	return sha256.Sum256([]byte(fmt.Sprintf(
		"gpt-load/credential-reset/v1/%d/%d/%s",
		groupID,
		credentialID,
		identityFingerprint,
	)))
}

func resetCreditOperationCanRetry(operation models.CredentialResetOperation, nowMS int64) bool {
	if operation.State == models.CredentialResetOperationOutcomeUnknown {
		return true
	}
	if operation.State != models.CredentialResetOperationPrepared || nowMS < 0 {
		return false
	}
	retryBeforeMS := nowMS - defaultSubscriptionControlTimeout.Milliseconds()
	return retryBeforeMS >= 0 && operation.UpdatedAtMS <= retryBeforeMS
}

func classifyExistingResetCreditOperation(
	existing models.CredentialResetOperation,
	digest [sha256.Size]byte,
	groupID uint,
	credentialID uint,
) (bool, error) {
	if !bytes.Equal(existing.RequestDigest, digest[:]) ||
		existing.GroupID != groupID || existing.CredentialID != credentialID {
		return false, app_errors.ErrIdempotencyKeyReused
	}
	switch existing.State {
	case models.CredentialResetOperationSucceeded:
		return true, nil
	case models.CredentialResetOperationRejected:
		return false, resetCreditErrorByCode(existing.ErrorCode)
	default:
		return false, app_errors.ErrResetCreditOutcomeUnknown
	}
}

func (s *Service) resetCreditOperationAllowsNext(
	ctx context.Context,
	credentialID uint,
	identityFingerprint string,
) bool {
	var latest models.CredentialResetOperation
	err := s.db.WithContext(ctx).
		Where("credential_id = ? AND state IN ?", credentialID, []models.CredentialResetOperationState{
			models.CredentialResetOperationPrepared,
			models.CredentialResetOperationSucceeded,
			models.CredentialResetOperationOutcomeUnknown,
		}).
		Order("updated_at_ms DESC").
		Order("idempotency_key DESC").
		Take(&latest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true
	}
	if err != nil || latest.State != models.CredentialResetOperationSucceeded {
		return false
	}
	var observation models.CredentialObservation
	if err := s.db.WithContext(ctx).Take(&observation, "credential_id = ?", credentialID).Error; err != nil {
		return false
	}
	result, err := decodeStoredResetCreditResult(latest.ResultJSON)
	if err != nil {
		return false
	}
	return observation.IdentityFingerprint == identityFingerprint &&
		observation.State == models.CredentialObservationFresh &&
		observation.ObservationVersion > result.ObservationVersionBefore
}

func (s *Service) finishResetCreditOperation(
	idempotencyKey string,
	state models.CredentialResetOperationState,
	result []byte,
	errorCode string,
) error {
	cleanupContext, cancel := context.WithTimeout(context.Background(), controlTransactionCleanupTimeout)
	defer cancel()
	nowMS := s.now().UTC().UnixMilli()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	updates := map[string]any{
		"state": state, "result_json": models.JSON(result), "error_code": errorCode,
		"updated_at_ms": nowMS, "completed_at_ms": nowMS,
	}
	resultUpdate := s.db.WithContext(cleanupContext).Model(&models.CredentialResetOperation{}).
		Where("idempotency_key = ? AND state = ?", idempotencyKey, models.CredentialResetOperationPrepared).
		Updates(updates)
	if resultUpdate.Error != nil {
		return resultUpdate.Error
	}
	if resultUpdate.RowsAffected != 1 {
		return fmt.Errorf("credential reset operation state changed")
	}
	return nil
}

func classifyResetCreditConsumeError(err error) (models.CredentialResetOperationState, *app_errors.APIError) {
	var upstream *codex.UpstreamHTTPError
	if !errors.As(err, &upstream) || upstream.StatusCode >= http.StatusInternalServerError {
		return models.CredentialResetOperationOutcomeUnknown, app_errors.ErrResetCreditOutcomeUnknown
	}
	switch upstream.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return models.CredentialResetOperationRejected, app_errors.ErrCredentialReauthorizationRequired
	case http.StatusConflict, http.StatusNotFound:
		return models.CredentialResetOperationRejected, app_errors.ErrResetCreditUnavailable
	default:
		return models.CredentialResetOperationRejected, app_errors.ErrResetCreditRejected
	}
}

func resetCreditErrorByCode(code string) *app_errors.APIError {
	switch code {
	case app_errors.ErrCredentialReauthorizationRequired.Code:
		return app_errors.ErrCredentialReauthorizationRequired
	case app_errors.ErrResetCreditUnavailable.Code:
		return app_errors.ErrResetCreditUnavailable
	case app_errors.ErrResetCreditRejected.Code:
		return app_errors.ErrResetCreditRejected
	default:
		return app_errors.ErrResetCreditOutcomeUnknown
	}
}

func normalizeResetCreditConsumeResult(raw []byte) (storedResetCreditResult, error) {
	var payload resetCreditUpstreamPayload
	if json.Unmarshal(raw, &payload) != nil || !strings.EqualFold(strings.TrimSpace(payload.Code), "reset") || payload.WindowsReset < 0 {
		return storedResetCreditResult{}, errors.New("invalid Codex reset credit response")
	}
	result := storedResetCreditResult{Status: "succeeded", WindowsReset: payload.WindowsReset}
	if payload.Credit != nil && strings.TrimSpace(payload.Credit.RedeemedAt) != "" {
		if parsed, err := time.Parse(time.RFC3339, payload.Credit.RedeemedAt); err == nil {
			value := parsed.UnixMilli()
			result.RedeemedAtMS = &value
		}
	}
	return result, nil
}

func decodeStoredResetCreditResult(raw []byte) (storedResetCreditResult, error) {
	var result storedResetCreditResult
	if json.Unmarshal(raw, &result) != nil || result.Status != "succeeded" || result.WindowsReset < 0 {
		return storedResetCreditResult{}, errors.New("invalid stored reset credit result")
	}
	return result, nil
}

func resetCreditResponse(result storedResetCreditResult, replayed bool) ResetCreditConsumeResponse {
	return ResetCreditConsumeResponse{
		Status: result.Status, WindowsReset: result.WindowsReset,
		RedeemedAtMS: result.RedeemedAtMS, Replayed: replayed,
	}
}

type resetCreditDetailPayload struct {
	ExpiresAt      string `json:"expires_at"`
	ExpiresAtCamel string `json:"expiresAt"`
	ResetType      string `json:"reset_type"`
	ResetTypeCamel string `json:"resetType"`
	Status         string `json:"status"`
}

type resetCreditDetailsEnvelope struct {
	AvailableCount        json.RawMessage `json:"available_count"`
	AvailableCountCamel   json.RawMessage `json:"availableCount"`
	Credits               json.RawMessage `json:"credits"`
	RateLimitResetCredits json.RawMessage `json:"rate_limit_reset_credits"`
	Items                 json.RawMessage `json:"items"`
	Data                  json.RawMessage `json:"data"`
}

func normalizeCodexResetCreditDetails(raw []byte) (*int64, []ObservationResetCredit, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil, false, nil
	}

	var details []*resetCreditDetailPayload
	var count *int64
	listPresent := false
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &details); err != nil {
			return nil, nil, false, err
		}
		listPresent = true
	} else {
		var envelope resetCreditDetailsEnvelope
		if err := json.Unmarshal(trimmed, &envelope); err != nil {
			return nil, nil, false, err
		}
		count = parseResetCreditCount(envelope.AvailableCount, envelope.AvailableCountCamel)
		for _, candidate := range []json.RawMessage{
			envelope.Credits,
			envelope.RateLimitResetCredits,
			envelope.Items,
			envelope.Data,
		} {
			candidate = bytes.TrimSpace(candidate)
			if len(candidate) == 0 || bytes.Equal(candidate, []byte("null")) {
				continue
			}
			if err := json.Unmarshal(candidate, &details); err != nil {
				return count, nil, false, err
			}
			listPresent = true
			break
		}
	}

	availableCount := int64(0)
	credits := make([]ObservationResetCredit, 0, len(details))
	for _, detail := range details {
		if detail == nil {
			continue
		}
		resetType := strings.TrimSpace(detail.ResetType)
		if resetType == "" {
			resetType = strings.TrimSpace(detail.ResetTypeCamel)
		}
		if resetType != "" && !strings.EqualFold(resetType, "codex_rate_limits") {
			continue
		}
		if status := strings.TrimSpace(detail.Status); status != "" && !strings.EqualFold(status, "available") {
			continue
		}
		availableCount++
		expiresAt := strings.TrimSpace(detail.ExpiresAt)
		if expiresAt == "" {
			expiresAt = strings.TrimSpace(detail.ExpiresAtCamel)
		}
		parsed, err := time.Parse(time.RFC3339, expiresAt)
		if err != nil {
			continue
		}
		credits = append(credits, ObservationResetCredit{ExpiresAtMS: parsed.UnixMilli()})
	}
	if count == nil && listPresent {
		count = &availableCount
	}
	sort.Slice(credits, func(i, j int) bool { return credits[i].ExpiresAtMS < credits[j].ExpiresAtMS })
	return count, credits, listPresent, nil
}

func parseResetCreditCount(values ...json.RawMessage) *int64 {
	for _, value := range values {
		trimmed := bytes.TrimSpace(value)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			continue
		}
		var count int64
		if trimmed[0] == '"' {
			var text string
			if json.Unmarshal(trimmed, &text) != nil {
				continue
			}
			parsed, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
			if err != nil {
				continue
			}
			count = parsed
		} else if json.Unmarshal(trimmed, &count) != nil {
			continue
		}
		if count >= 0 {
			return &count
		}
	}
	return nil
}
