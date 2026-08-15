package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/usage"
)

const (
	observationRefreshFloor = 5 * time.Minute
	observationFreshTTL     = time.Hour
)

type ObservationPlanSummary struct {
	Name string `json:"name,omitempty"`
}

type ObservationQuotaWindow struct {
	ID            string                  `json:"id"`
	Label         string                  `json:"label"`
	Scope         string                  `json:"scope"`
	Unit          string                  `json:"unit"`
	Used          *float64                `json:"used,omitempty"`
	Limit         *float64                `json:"limit,omitempty"`
	Remaining     *float64                `json:"remaining,omitempty"`
	Utilization   *float64                `json:"utilization,omitempty"`
	ResetAtMS     *int64                  `json:"reset_at_ms,omitempty"`
	WindowSeconds *int64                  `json:"window_seconds,omitempty"`
	ModelIDs      []string                `json:"model_ids,omitempty"`
	State         string                  `json:"state"`
	IsPrimary     bool                    `json:"is_primary,omitempty"`
	ObservedUsage *ObservationWindowUsage `json:"observed_usage,omitempty"`
}

type ObservationWindowUsage struct {
	WindowStartMS                 int64  `json:"window_start_ms"`
	WindowEndMS                   int64  `json:"window_end_ms"`
	Source                        string `json:"source"`
	DataComplete                  bool   `json:"data_complete"`
	UsageComplete                 bool   `json:"usage_complete"`
	PricingComplete               bool   `json:"pricing_complete"`
	RequestCount                  int64  `json:"request_count"`
	InputTokens                   int64  `json:"input_tokens"`
	OutputTokens                  int64  `json:"output_tokens"`
	TotalTokens                   int64  `json:"total_tokens"`
	EstimatedReferenceCostNanoUSD string `json:"estimated_reference_cost_nano_usd"`
	LastUsedAtMS                  *int64 `json:"last_used_at_ms,omitempty"`
}

type CredentialObservationSnapshot struct {
	Plan                  ObservationPlanSummary   `json:"plan_summary"`
	QuotaWindows          []ObservationQuotaWindow `json:"quota_windows"`
	ResetCreditsAvailable *int64                   `json:"reset_credits_available,omitempty"`
	ResetCredits          []ObservationResetCredit `json:"reset_credits,omitempty"`
}

type CredentialObservationResponse struct {
	State              string                         `json:"state"`
	Snapshot           *CredentialObservationSnapshot `json:"snapshot"`
	ObservationVersion uint64                         `json:"observation_version"`
	ObservedAtMS       *int64                         `json:"observed_at_ms"`
	FreshUntilMS       *int64                         `json:"fresh_until_ms"`
	LastAttemptAtMS    *int64                         `json:"last_attempt_at_ms"`
	LastErrorCode      string                         `json:"last_error_code,omitempty"`
}

type CredentialDetailResponse struct {
	Credential  CredentialItemResponse        `json:"credential"`
	Observation CredentialObservationResponse `json:"observation"`
}

type observationRefreshMode uint8

const (
	observationRefreshScheduled observationRefreshMode = iota
	observationRefreshManual
	observationRefreshAfterMutation
)

type observationFlight struct {
	done   chan struct{}
	result CredentialObservationResponse
	err    error
	joined int
	mode   observationRefreshMode
}

type observationFlightKey struct {
	groupID      uint
	credentialID uint
}

func (s *Service) RefreshCredentialObservation(
	ctx context.Context,
	groupID uint,
	credentialID uint,
) (CredentialObservationResponse, error) {
	return s.refreshCredentialObservation(ctx, groupID, credentialID, observationRefreshManual)
}

func (s *Service) refreshCredentialObservation(
	ctx context.Context,
	groupID uint,
	credentialID uint,
	mode observationRefreshMode,
) (CredentialObservationResponse, error) {
	if groupID == 0 || credentialID == 0 {
		return CredentialObservationResponse{}, app_errors.ErrValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	key := observationFlightKey{groupID: groupID, credentialID: credentialID}
	for {
		s.observationMu.Lock()
		if existing := s.observationFlights[key]; existing != nil {
			existing.joined++
			followUp := mode == observationRefreshAfterMutation && existing.mode != observationRefreshAfterMutation
			s.observationMu.Unlock()
			select {
			case <-ctx.Done():
				return CredentialObservationResponse{}, ctx.Err()
			case <-existing.done:
				if followUp {
					continue
				}
				return existing.result, existing.err
			}
		}
		flight := &observationFlight{done: make(chan struct{}), mode: mode}
		s.observationFlights[key] = flight
		s.observationMu.Unlock()
		defer func() {
			s.observationMu.Lock()
			delete(s.observationFlights, key)
			close(flight.done)
			s.observationMu.Unlock()
		}()
		flight.result, flight.err = s.refreshCredentialObservationOnce(ctx, groupID, credentialID, mode)
		return flight.result, flight.err
	}
}

func (s *Service) refreshCredentialObservationOnce(
	ctx context.Context,
	groupID uint,
	credentialID uint,
	mode observationRefreshMode,
) (CredentialObservationResponse, error) {
	group, credential, previous, err := s.loadObservationTarget(ctx, groupID, credentialID)
	if err != nil {
		return CredentialObservationResponse{}, err
	}
	now := s.now().UTC()
	if mode == observationRefreshScheduled && previous.IdentityFingerprint == credential.IdentityFingerprint &&
		previous.NextAllowedAtMS != nil && now.UnixMilli() < *previous.NextAllowedAtMS {
		return mapCredentialObservation(previous), nil
	}
	codexCredential, err := s.prepareStoredCodexCredential(ctx, group, credential)
	if err != nil {
		return CredentialObservationResponse{}, err
	}
	select {
	case s.observationSemaphore <- struct{}{}:
		defer func() { <-s.observationSemaphore }()
	case <-ctx.Done():
		return CredentialObservationResponse{}, ctx.Err()
	}
	attemptMS := now.UnixMilli()
	nextAllowedMS := now.Add(observationRefreshFloor).UnixMilli()
	observeContext, cancelObserve := context.WithTimeout(ctx, defaultSubscriptionControlTimeout)
	defer cancelObserve()
	observation, observeErr := s.observeCodexAccount(observeContext, codexCredential)
	if observeErr != nil {
		return s.recordCredentialObservationFailure(
			ctx, credential, previous, attemptMS, nextAllowedMS,
			"observation_upstream_failed", "refresh subscription information",
		)
	}
	snapshot, err := normalizeCodexObservation(observation.Payload)
	if err != nil {
		return s.recordCredentialObservationFailure(
			ctx, credential, previous, attemptMS, nextAllowedMS,
			"observation_payload_invalid", "normalize subscription information",
		)
	}
	if s.observeCodexResetCredits != nil {
		detailContext, cancelDetails := context.WithTimeout(ctx, defaultSubscriptionControlTimeout)
		details, detailErr := s.observeCodexResetCredits(detailContext, codexCredential)
		cancelDetails()
		if detailErr == nil {
			count, credits, listPresent, normalizeErr := normalizeCodexResetCreditDetails(details.Payload)
			if count != nil {
				snapshot.ResetCreditsAvailable = count
			}
			if normalizeErr == nil && listPresent {
				snapshot.ResetCredits = credits
			}
		}
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return CredentialObservationResponse{}, app_errors.ErrInternalServer
	}
	version := previous.ObservationVersion + 1
	if previous.ObservationVersion == 0 {
		version = 1
	}
	freshUntilMS := now.Add(observationFreshTTL).UnixMilli()
	row := models.CredentialObservation{
		CredentialID: credential.ID, IdentityFingerprint: credential.IdentityFingerprint,
		SchemaVersion: 1, ObservationVersion: version, SnapshotJSON: models.JSON(encoded),
		State: models.CredentialObservationFresh, ObservedAtMS: &attemptMS,
		FreshUntilMS: &freshUntilMS, LastAttemptAtMS: &attemptMS, NextAllowedAtMS: &nextAllowedMS,
		UpdatedAtMS: attemptMS,
	}
	if err := s.upsertCredentialObservation(ctx, row); err != nil {
		return CredentialObservationResponse{}, err
	}
	response := mapCredentialObservation(row)
	s.applyCredentialQuotaObservation(credentialID, &response)
	s.enrichCredentialObservationUsage(ctx, credentialID, &response)
	return response, nil
}

func (s *Service) recordCredentialObservationFailure(
	ctx context.Context,
	credential models.Credential,
	previous models.CredentialObservation,
	attemptMS int64,
	nextAllowedMS int64,
	code string,
	summary string,
) (CredentialObservationResponse, error) {
	failed := previous
	failed.CredentialID = credential.ID
	failed.IdentityFingerprint = credential.IdentityFingerprint
	failed.SchemaVersion = 1
	if failed.ObservationVersion == 0 {
		failed.ObservationVersion = 1
	}
	failed.State = models.CredentialObservationError
	failed.LastAttemptAtMS = &attemptMS
	failed.NextAllowedAtMS = &nextAllowedMS
	failed.LastErrorCode = code
	failed.UpdatedAtMS = attemptMS
	if len(failed.SnapshotJSON) == 0 {
		failed.SnapshotJSON = models.JSON(`{}`)
	}
	if err := s.upsertCredentialObservation(ctx, failed); err != nil {
		return CredentialObservationResponse{}, err
	}
	response := mapCredentialObservation(failed)
	s.applyCredentialQuotaObservation(credential.ID, &response)
	return response, fmt.Errorf("%s: %w", summary, app_errors.ErrBadGateway)
}

func (s *Service) GetCredentialObservation(ctx context.Context, groupID, credentialID uint) (CredentialObservationResponse, error) {
	_, credential, row, err := s.loadObservationTarget(ctx, groupID, credentialID)
	if err != nil {
		return CredentialObservationResponse{}, err
	}
	response := observationResponseValue(presentCredentialObservation(row, credential.IdentityFingerprint, s.now().UTC()))
	s.enrichCredentialObservationUsage(ctx, credentialID, &response)
	return response, nil
}

func (s *Service) GetCredentialDetail(ctx context.Context, groupID, credentialID uint) (CredentialDetailResponse, error) {
	item, err := s.loadCredentialItem(ctx, groupID, credentialID)
	if err != nil {
		return CredentialDetailResponse{}, err
	}
	observation := observationResponseValue(item.Observation)
	return CredentialDetailResponse{Credential: item, Observation: observation}, nil
}

func (s *Service) loadObservationTarget(ctx context.Context, groupID, credentialID uint) (models.Group, models.Credential, models.CredentialObservation, error) {
	var group models.Group
	var credential models.Credential
	var observation models.CredentialObservation
	err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		if err := tx.Take(&group, groupID).Error; err != nil {
			return err
		}
		if normalizeGroupConnectionType(group.ConnectionType) != models.ConnectionTypeSubscription || group.ChannelID != string(channel.Codex) {
			return app_errors.ErrValidation
		}
		if err := tx.Where("id = ? AND group_id = ?", credentialID, groupID).Take(&credential).Error; err != nil {
			return err
		}
		result := tx.Take(&observation, "credential_id = ?", credentialID)
		if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return result.Error
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return group, credential, observation, app_errors.ErrResourceNotFound
		}
		var apiErr *app_errors.APIError
		if errors.As(err, &apiErr) {
			return group, credential, observation, err
		}
		return group, credential, observation, app_errors.ParseDBError(err)
	}
	return group, credential, observation, nil
}

func (s *Service) upsertCredentialObservation(ctx context.Context, row models.CredentialObservation) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing models.CredentialObservation
		err := tx.Take(&existing, "credential_id = ?", row.CredentialID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&row).Error
		}
		if err != nil {
			return err
		}
		return tx.Save(&row).Error
	})
}

func (s *Service) restoreCredentialQuotaObservations(ctx context.Context) error {
	var observations []models.CredentialObservation
	if err := s.db.WithContext(ctx).Find(&observations).Error; err != nil {
		return app_errors.ParseDBError(err)
	}
	if len(observations) == 0 {
		return nil
	}
	credentialIDs := make([]uint, 0, len(observations))
	for _, observation := range observations {
		credentialIDs = append(credentialIDs, observation.CredentialID)
	}
	var credentials []models.Credential
	if err := s.db.WithContext(ctx).
		Where("id IN ?", credentialIDs).
		Find(&credentials).Error; err != nil {
		return app_errors.ParseDBError(err)
	}
	identities := make(map[uint]string, len(credentials))
	for _, credential := range credentials {
		identities[credential.ID] = credential.IdentityFingerprint
	}
	now := s.now().UTC()
	for _, observation := range observations {
		identity, exists := identities[observation.CredentialID]
		if !exists {
			continue
		}
		response := presentCredentialObservation(observation, identity, now)
		s.applyCredentialQuotaObservation(observation.CredentialID, response)
	}
	return nil
}

func mapCredentialObservation(row models.CredentialObservation) CredentialObservationResponse {
	response := CredentialObservationResponse{
		State: string(row.State), ObservationVersion: row.ObservationVersion,
		ObservedAtMS: row.ObservedAtMS, FreshUntilMS: row.FreshUntilMS,
		LastAttemptAtMS: row.LastAttemptAtMS, LastErrorCode: row.LastErrorCode,
	}
	if len(row.SnapshotJSON) > 0 && string(row.SnapshotJSON) != "{}" {
		var snapshot CredentialObservationSnapshot
		if json.Unmarshal(row.SnapshotJSON, &snapshot) == nil {
			response.Snapshot = &snapshot
		}
	}
	return response
}

func presentCredentialObservation(row models.CredentialObservation, identityFingerprint string, now time.Time) *CredentialObservationResponse {
	if row.CredentialID == 0 || row.IdentityFingerprint != identityFingerprint {
		unavailable := CredentialObservationResponse{State: string(models.CredentialObservationUnavailable)}
		return &unavailable
	}
	if row.State == models.CredentialObservationFresh && row.FreshUntilMS != nil && now.UnixMilli() >= *row.FreshUntilMS {
		row.State = models.CredentialObservationStale
	}
	response := mapCredentialObservation(row)
	return &response
}

func observationResponseValue(value *CredentialObservationResponse) CredentialObservationResponse {
	if value != nil {
		return *value
	}
	return CredentialObservationResponse{State: string(models.CredentialObservationUnavailable)}
}

func (s *Service) applyCredentialQuotaObservation(
	credentialID uint,
	response *CredentialObservationResponse,
) {
	if s == nil || s.registry == nil || credentialID == 0 || response == nil ||
		response.State != string(models.CredentialObservationFresh) ||
		response.Snapshot == nil || response.FreshUntilMS == nil {
		if s != nil && s.registry != nil && credentialID != 0 {
			s.registry.SetCredentialQuotaObservation(credentialID, nil, time.Time{}, time.Time{})
		}
		return
	}
	nowMS := s.now().UTC().UnixMilli()
	if *response.FreshUntilMS <= nowMS {
		s.registry.SetCredentialQuotaObservation(credentialID, nil, time.Time{}, time.Time{})
		return
	}
	var remaining *float64
	var resetAtMS int64
	for _, window := range response.Snapshot.QuotaWindows {
		if window.Scope != "account" || window.ResetAtMS == nil || *window.ResetAtMS <= nowMS {
			continue
		}
		value, known := observationWindowRemainingRatio(window)
		if !known {
			continue
		}
		if remaining == nil || value < *remaining {
			cloned := value
			remaining = &cloned
			resetAtMS = *window.ResetAtMS
		} else if value == *remaining && *window.ResetAtMS > resetAtMS {
			// Equal bottlenecks must all recover before this credential is usable.
			resetAtMS = *window.ResetAtMS
		}
	}
	if remaining == nil || resetAtMS == 0 {
		s.registry.SetCredentialQuotaObservation(credentialID, nil, time.Time{}, time.Time{})
		return
	}
	s.registry.SetCredentialQuotaObservation(
		credentialID,
		remaining,
		time.UnixMilli(resetAtMS).UTC(),
		time.UnixMilli(*response.FreshUntilMS).UTC(),
	)
}

func observationWindowRemainingRatio(window ObservationQuotaWindow) (float64, bool) {
	if window.State == "exhausted" {
		return 0, true
	}
	if window.Utilization != nil {
		return math.Max(0, math.Min(1, 1-*window.Utilization)), true
	}
	if window.Remaining != nil && window.Limit != nil && *window.Limit > 0 {
		return math.Max(0, math.Min(1, *window.Remaining / *window.Limit)), true
	}
	return 0, false
}

// credentialDailyUsageWindow 是账号卡上「近 24 小时成功/失败」的窗口长度。
// 取 24 小时是因为 QueryCredentialWindowUsage 在不超过 24 小时时走精确请求日志，
// 再长就退化成小时聚合，边界会变成近似值。
const credentialDailyUsageWindow = 24 * time.Hour

func (s *Service) enrichCredentialDailyUsage(
	ctx context.Context,
	credentialID uint,
	item *CredentialItemResponse,
) {
	if s == nil || s.credentialWindowUsage == nil || credentialID == 0 || item == nil {
		return
	}
	now := s.now().UTC()
	fromMS := now.Add(-credentialDailyUsageWindow).UnixMilli()
	toMS := now.UnixMilli()
	if fromMS < 0 || toMS <= fromMS {
		return
	}
	observed, err := s.credentialWindowUsage.QueryCredentialWindowUsage(ctx, requestlog.CredentialWindowUsageQuery{
		CredentialID: credentialID,
		FromMS:       fromMS,
		ToMS:         toMS,
		Source:       requestlog.CredentialWindowUsageSourceRequestLogs,
	})
	if err != nil {
		return
	}
	item.DailyUsage = &CredentialDailyUsageResponse{
		WindowSeconds: int64(credentialDailyUsageWindow / time.Second),
		SuccessCount:  observed.SuccessCount,
		FailureCount:  observed.FailureCount,
		DataComplete:  observed.DataComplete,
	}
}

func (s *Service) enrichCredentialObservationUsage(
	ctx context.Context,
	credentialID uint,
	response *CredentialObservationResponse,
) {
	if s == nil || s.credentialWindowUsage == nil || credentialID == 0 ||
		response == nil || response.Snapshot == nil {
		return
	}
	nowMS := s.now().UTC().UnixMilli()
	for index := range response.Snapshot.QuotaWindows {
		window := &response.Snapshot.QuotaWindows[index]
		if window.Scope != "account" || window.ResetAtMS == nil ||
			window.WindowSeconds == nil || *window.WindowSeconds <= 0 ||
			*window.ResetAtMS <= nowMS {
			continue
		}
		if *window.WindowSeconds > math.MaxInt64/1000 {
			continue
		}
		windowMS := *window.WindowSeconds * 1000
		if windowMS > *window.ResetAtMS {
			continue
		}
		fromMS := *window.ResetAtMS - windowMS
		if fromMS >= nowMS {
			continue
		}
		source := requestlog.CredentialWindowUsageSourceRequestLogs
		if *window.WindowSeconds > int64((24*time.Hour)/time.Second) {
			source = requestlog.CredentialWindowUsageSourceHourlyStats
		}
		observed, err := s.credentialWindowUsage.QueryCredentialWindowUsage(ctx, requestlog.CredentialWindowUsageQuery{
			CredentialID: credentialID,
			FromMS:       fromMS,
			ToMS:         nowMS,
			Source:       source,
		})
		if err != nil {
			continue
		}
		mapped, ok := mapObservationWindowUsage(fromMS, nowMS, observed)
		if ok {
			window.ObservedUsage = mapped
		}
	}
}

func mapObservationWindowUsage(
	fromMS int64,
	toMS int64,
	observed requestlog.CredentialWindowUsage,
) (*ObservationWindowUsage, bool) {
	inputTokens := int64(0)
	for _, value := range []int64{
		observed.UncachedInputTokens,
		observed.CacheReadTokens,
		observed.CacheWrite5MTokens,
		observed.CacheWrite1HTokens,
		observed.CacheWriteUnknownTokens,
	} {
		var ok bool
		inputTokens, ok = usage.CheckedAdd(inputTokens, value)
		if !ok {
			return nil, false
		}
	}
	totalTokens, ok := usage.CheckedAdd(inputTokens, observed.OutputTokens)
	if !ok {
		return nil, false
	}
	return &ObservationWindowUsage{
		WindowStartMS: fromMS,
		WindowEndMS:   toMS,
		Source:        string(observed.Source),
		DataComplete:  observed.DataComplete,
		UsageComplete: observed.UsageMissingCount == 0 && observed.PartialCount == 0,
		PricingComplete: observed.UnpricedRequestCount == 0 &&
			observed.PricingPartialCount == 0,
		RequestCount:                  observed.RequestCount,
		InputTokens:                   inputTokens,
		OutputTokens:                  observed.OutputTokens,
		TotalTokens:                   totalTokens,
		EstimatedReferenceCostNanoUSD: strconv.FormatInt(observed.EstimatedCostNanoUSD, 10),
		LastUsedAtMS:                  observed.LastUsedAtMS,
	}, true
}

func normalizeCodexObservation(raw []byte) (CredentialObservationSnapshot, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return CredentialObservationSnapshot{}, errors.New("invalid observation")
	}
	result := CredentialObservationSnapshot{QuotaWindows: []ObservationQuotaWindow{}}
	result.Plan.Name = cleanObservationString(firstObservationValue(payload, "plan_type", "planType"))
	if credits, ok := observationMap(firstObservationValue(payload, "rate_limit_reset_credits", "rateLimitResetCredits")); ok {
		if value, ok := observationInt(firstObservationValue(credits, "available_count", "availableCount")); ok {
			result.ResetCreditsAvailable = &value
		}
	}
	if rate, ok := observationMap(firstObservationValue(payload, "rate_limit", "rateLimit")); ok {
		result.QuotaWindows = append(result.QuotaWindows, normalizeRateWindows(rate, "", "account")...)
	}
	if additional, ok := firstObservationValue(payload, "additional_rate_limits", "additionalRateLimits").([]any); ok {
		for index, rawEntry := range additional {
			entry, ok := observationMap(rawEntry)
			if !ok {
				continue
			}
			name := cleanObservationString(firstObservationValue(entry, "limit_name", "limitName", "metered_feature", "meteredFeature"))
			if name == "" {
				name = "additional-" + strconv.Itoa(index+1)
			}
			rate, ok := observationMap(firstObservationValue(entry, "rate_limit", "rateLimit"))
			if !ok {
				continue
			}
			windows := normalizeRateWindows(rate, safeObservationID(name)+"-", name)
			modelIDs := observationStrings(firstObservationValue(entry, "model_ids", "modelIds"))
			for windowIndex := range windows {
				windows[windowIndex].ModelIDs = append([]string(nil), modelIDs...)
			}
			result.QuotaWindows = append(result.QuotaWindows, windows...)
		}
	}
	sort.SliceStable(result.QuotaWindows, func(i, j int) bool {
		left, right := result.QuotaWindows[i], result.QuotaWindows[j]
		if (left.State == "exhausted") != (right.State == "exhausted") {
			return left.State == "exhausted"
		}
		lu, ru := -1.0, -1.0
		if left.Utilization != nil {
			lu = *left.Utilization
		}
		if right.Utilization != nil {
			ru = *right.Utilization
		}
		if lu != ru {
			return lu > ru
		}
		lr, rr := int64(math.MaxInt64), int64(math.MaxInt64)
		if left.ResetAtMS != nil {
			lr = *left.ResetAtMS
		}
		if right.ResetAtMS != nil {
			rr = *right.ResetAtMS
		}
		if lr != rr {
			return lr < rr
		}
		return left.ID < right.ID
	})
	if len(result.QuotaWindows) > 0 {
		result.QuotaWindows[0].IsPrimary = true
	}
	return result, nil
}

func normalizeRateWindows(rate map[string]any, prefix, scope string) []ObservationQuotaWindow {
	result := make([]ObservationQuotaWindow, 0, 2)
	for _, name := range []string{"primary", "secondary"} {
		window, ok := observationMap(firstObservationValue(rate, name+"_window", name+"Window"))
		if !ok {
			continue
		}
		item := ObservationQuotaWindow{ID: prefix + name, Label: observationWindowLabel(window, name, scope), Scope: scope, Unit: "percent", State: "unknown"}
		if used, ok := observationFloat(firstObservationValue(window, "used_percent", "usedPercent")); ok {
			used = math.Max(0, math.Min(100, used))
			limit, remaining, utilization := 100.0, 100-used, used/100
			item.Used, item.Limit, item.Remaining, item.Utilization = &used, &limit, &remaining, &utilization
			if used >= 100 {
				item.State = "exhausted"
			} else {
				item.State = "available"
			}
		}
		if allowed, ok := firstObservationValue(rate, "allowed").(bool); ok && !allowed {
			item.State = "exhausted"
		}
		if reached, ok := firstObservationValue(rate, "limit_reached", "limitReached").(bool); ok && reached {
			item.State = "exhausted"
		}
		if seconds, ok := observationInt(firstObservationValue(window, "limit_window_seconds", "limitWindowSeconds")); ok {
			item.WindowSeconds = &seconds
		}
		if reset, ok := observationInt(firstObservationValue(window, "reset_at", "resetAt")); ok {
			value := reset * 1000
			item.ResetAtMS = &value
		}
		result = append(result, item)
	}
	return result
}

func observationWindowLabel(window map[string]any, fallback, scope string) string {
	if value := cleanObservationString(firstObservationValue(window, "label", "display_name", "displayName")); value != "" {
		return value
	}
	if seconds, ok := observationInt(firstObservationValue(window, "limit_window_seconds", "limitWindowSeconds")); ok {
		duration := strconv.FormatInt(seconds, 10) + "s"
		switch seconds {
		case 5 * 60 * 60:
			duration = "5h"
		case 7 * 24 * 60 * 60:
			duration = "7d"
		}
		if scope != "account" {
			return scope + " · " + duration
		}
		return duration
	}
	if scope != "account" {
		return scope + " · " + fallback
	}
	return fallback
}

func firstObservationValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}
func observationMap(value any) (map[string]any, bool) {
	result, ok := value.(map[string]any)
	return result, ok
}
func cleanObservationString(value any) string {
	result, _ := value.(string)
	return strings.TrimSpace(result)
}
func observationFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		result, err := typed.Float64()
		return result, err == nil
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	}
	return 0, false
}
func observationInt(value any) (int64, bool) {
	result, ok := observationFloat(value)
	if !ok || math.Trunc(result) != result || result < 0 || result > math.MaxInt64 {
		return 0, false
	}
	return int64(result), true
}
func observationStrings(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value, ok := raw.(string)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
func safeObservationID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
