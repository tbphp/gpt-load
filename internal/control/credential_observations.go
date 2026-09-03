package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/storage/models"
	providerobservation "gpt-load/internal/subscription/providers/observation"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
	"gpt-load/internal/usage"
)

type ObservationPlanSummary struct {
	Name  string `json:"name,omitempty"`
	Level string `json:"level,omitempty"`
}

type ObservationAccountSummary struct {
	DisplayName               string `json:"display_name,omitempty"`
	Email                     string `json:"email,omitempty"`
	OrganizationName          string `json:"organization_name,omitempty"`
	OrganizationType          string `json:"organization_type,omitempty"`
	OrganizationRole          string `json:"organization_role,omitempty"`
	WorkspaceRole             string `json:"workspace_role,omitempty"`
	OrganizationRateLimitTier string `json:"organization_rate_limit_tier,omitempty"`
	UserRateLimitTier         string `json:"user_rate_limit_tier,omitempty"`
	SeatTier                  string `json:"seat_tier,omitempty"`
	BillingType               string `json:"billing_type,omitempty"`
	ExtraUsageEnabled         *bool  `json:"extra_usage_enabled,omitempty"`
	ExtraUsageDisabledReason  string `json:"extra_usage_disabled_reason,omitempty"`
	AccountCreatedAtMS        *int64 `json:"account_created_at_ms,omitempty"`
	SubscriptionCreatedAtMS   *int64 `json:"subscription_created_at_ms,omitempty"`
}

type ObservationQuotaWindow struct {
	ID            string                  `json:"id"`
	Label         string                  `json:"label"`
	LabelKey      string                  `json:"label_key,omitempty"`
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
	Plan                  ObservationPlanSummary     `json:"plan_summary"`
	Account               *ObservationAccountSummary `json:"account_summary,omitempty"`
	QuotaWindows          []ObservationQuotaWindow   `json:"quota_windows"`
	ResetCreditsAvailable *int64                     `json:"reset_credits_available,omitempty"`
	ResetCredits          []ObservationResetCredit   `json:"reset_credits,omitempty"`
}

type CredentialObservationResponse struct {
	State              string                         `json:"state"`
	Snapshot           *CredentialObservationSnapshot `json:"snapshot"`
	ObservationVersion uint64                         `json:"observation_version"`
	ObservedAtMS       *int64                         `json:"observed_at_ms"`
	LastAttemptAtMS    *int64                         `json:"last_attempt_at_ms"`
	LastErrorCode      string                         `json:"last_error_code,omitempty"`
}

type CredentialDetailResponse struct {
	Credential  CredentialItemResponse        `json:"credential"`
	Observation CredentialObservationResponse `json:"observation"`
}

type observationRefreshMode uint8

const (
	observationRefreshManual observationRefreshMode = iota
	observationRefreshAfterReset
)

type observationFlight struct {
	done   chan struct{}
	result CredentialObservationResponse
	err    error
	joined int
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
			followUp := mode == observationRefreshAfterReset
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
		flight := &observationFlight{done: make(chan struct{})}
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

func (s *Service) credentialObservationRefreshInFlight(groupID, credentialID uint) bool {
	if s == nil || groupID == 0 || credentialID == 0 {
		return false
	}
	s.observationMu.Lock()
	defer s.observationMu.Unlock()
	return s.observationFlights[observationFlightKey{
		groupID: groupID, credentialID: credentialID,
	}] != nil
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
	network, err := s.credentialNetworkContext(ctx, s.db, group, credential)
	if err != nil {
		return CredentialObservationResponse{}, err
	}
	ctx = subscriptionruntime.WithNetworkContext(ctx, network)
	if mode == observationRefreshAfterReset {
		if _, err := s.invalidateCredentialObservationAfterReset(ctx, credential, &previous); err != nil {
			return CredentialObservationResponse{}, err
		}
	}
	now := s.now().UTC()
	preparedCredential, err := s.prepareStoredSubscriptionCredential(ctx, group, credential)
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
	observeContext, cancelObserve := context.WithTimeout(ctx, defaultSubscriptionControlTimeout)
	defer cancelObserve()
	channelID := channel.ID(group.ChannelID)
	observation, observeErr := s.observeSubscriptionAccount(observeContext, channelID, preparedCredential)
	authRefreshVersion := previous.LastAuthRefreshSecretVersion
	if subscriptionUpstreamHTTPStatus(observeErr) == http.StatusUnauthorized &&
		(previous.LastAuthRefreshSecretVersion == nil ||
			*previous.LastAuthRefreshSecretVersion != credential.SecretVersion) &&
		s.prepareSubscriptionCredential != nil {
		preparedCredential, err = s.prepareStoredSubscriptionCredentialWithForce(
			observeContext,
			group,
			credential,
			true,
		)
		if err != nil {
			return CredentialObservationResponse{}, err
		}
		var refreshed models.Credential
		if err := s.db.WithContext(observeContext).Select("secret_version").First(&refreshed, credential.ID).Error; err != nil {
			return CredentialObservationResponse{}, app_errors.ParseDBError(err)
		}
		version := refreshed.SecretVersion
		authRefreshVersion = &version
		observation, observeErr = s.observeSubscriptionAccount(observeContext, channelID, preparedCredential)
	}
	if observeErr != nil {
		if errors.Is(observeErr, subscriptionruntime.ErrObservationPayloadInvalid) {
			return s.recordCredentialObservationFailure(
				ctx, credential, previous, attemptMS,
				"observation_payload_invalid", "normalize subscription information", nil,
			)
		}
		if errors.Is(observeErr, subscriptionruntime.ErrObservationUnavailable) {
			return s.recordCredentialObservationFailure(
				ctx, credential, previous, attemptMS,
				"observation_unavailable", "read subscription information", nil,
			)
		}
		if status := subscriptionUpstreamHTTPStatus(observeErr); status == http.StatusUnauthorized ||
			status == http.StatusForbidden {
			code, summary := "observation_authorization_failed", "authorize subscription information"
			if status == http.StatusForbidden {
				code, summary = "observation_access_denied", "access subscription information"
			}
			return s.recordCredentialObservationFailure(
				ctx, credential, previous, attemptMS,
				code, summary, authRefreshVersion,
			)
		}
		return s.recordCredentialObservationFailure(
			ctx, credential, previous, attemptMS,
			"observation_upstream_failed", "refresh subscription information", nil,
		)
	}
	var snapshot CredentialObservationSnapshot
	if err := json.Unmarshal(observation.Payload, &snapshot); err != nil || snapshot.QuotaWindows == nil {
		return s.recordCredentialObservationFailure(
			ctx, credential, previous, attemptMS,
			"observation_payload_invalid", "normalize subscription information", nil,
		)
	}
	version := previous.ObservationVersion + 1
	if previous.ObservationVersion == 0 {
		version = 1
	}
	state := models.CredentialObservationFresh
	lastErrorCode := ""
	var previousSnapshot CredentialObservationSnapshot
	previousSnapshotOK := len(previous.SnapshotJSON) > 0 &&
		json.Unmarshal(previous.SnapshotJSON, &previousSnapshot) == nil
	previousQuotaFresh := previousSnapshotOK && previous.State == models.CredentialObservationFresh
	if observation.Partial {
		lastErrorCode = "observation_partial"
		if previousSnapshotOK {
			if observation.AccountObserved {
				snapshot.Plan = mergeObservationPlanSummary(previousSnapshot.Plan, snapshot.Plan)
				snapshot.Account = mergeObservationAccountSummary(previousSnapshot.Account, snapshot.Account)
			} else {
				snapshot.Plan = previousSnapshot.Plan
				snapshot.Account = previousSnapshot.Account
			}
		}
	}
	switch {
	case len(observation.ObservedQuotaScopes) > 0:
		currentQuotaData := observation.QuotaObserved || len(snapshot.QuotaWindows) > 0
		var previousQuotaWindows []ObservationQuotaWindow
		if previousQuotaFresh {
			previousQuotaWindows = previousSnapshot.QuotaWindows
		}
		var preservedPrevious bool
		snapshot.QuotaWindows, preservedPrevious = mergeObservationQuotaWindows(
			previousQuotaWindows,
			snapshot.QuotaWindows,
			observation.ObservedQuotaScopes,
		)
		switch {
		case preservedPrevious:
		case !observation.Partial || currentQuotaData:
		default:
			state = models.CredentialObservationStale
		}
	case observation.Partial && !observation.QuotaObserved && previousQuotaFresh:
		snapshot.QuotaWindows = previousSnapshot.QuotaWindows
		snapshot.ResetCreditsAvailable = previousSnapshot.ResetCreditsAvailable
		snapshot.ResetCredits = previousSnapshot.ResetCredits
	case observation.Partial && !observation.QuotaObserved:
		state = models.CredentialObservationStale
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return CredentialObservationResponse{}, app_errors.ErrInternalServer
	}
	// The observation time is when this result was actually obtained, not when
	// the attempt started: a slow refresh would otherwise be stamped older
	// than passive samples captured while it was still in flight, letting them
	// overwrite it. Keeping updated_at_ms on the same instant also guarantees
	// the passive writer's compare-and-set token changes on every active write.
	completedMS := s.now().UTC().UnixMilli()
	if completedMS < attemptMS {
		completedMS = attemptMS
	}
	row := models.CredentialObservation{
		CredentialID: credential.ID, IdentityFingerprint: credential.IdentityFingerprint,
		SchemaVersion: 1, ObservationVersion: version, SnapshotJSON: models.JSON(encoded),
		State: state, ObservedAtMS: &completedMS,
		LastAttemptAtMS: &attemptMS,
		LastErrorCode:   lastErrorCode, UpdatedAtMS: completedMS,
	}
	if err := s.upsertCredentialObservation(ctx, row); err != nil {
		return CredentialObservationResponse{}, err
	}
	response := mapCredentialObservation(row)
	s.applyCredentialQuotaObservation(credentialID, &response)
	s.enrichCredentialObservationUsage(ctx, credentialID, &response)
	return response, nil
}

func mergeObservationPlanSummary(previous, current ObservationPlanSummary) ObservationPlanSummary {
	if current.Name == "" {
		return previous
	}
	return current
}

func mergeObservationAccountSummary(
	previous *ObservationAccountSummary,
	current *ObservationAccountSummary,
) *ObservationAccountSummary {
	if previous == nil {
		return current
	}
	if current == nil {
		return previous
	}
	merged := *previous
	for target, value := range map[*string]string{
		&merged.DisplayName: current.DisplayName, &merged.Email: current.Email,
		&merged.OrganizationName: current.OrganizationName, &merged.OrganizationType: current.OrganizationType,
		&merged.OrganizationRole: current.OrganizationRole, &merged.WorkspaceRole: current.WorkspaceRole,
		&merged.OrganizationRateLimitTier: current.OrganizationRateLimitTier,
		&merged.UserRateLimitTier:         current.UserRateLimitTier,
		&merged.SeatTier:                  current.SeatTier,
		&merged.BillingType:               current.BillingType,
	} {
		if value != "" {
			*target = value
		}
	}
	if current.ExtraUsageEnabled != nil {
		merged.ExtraUsageEnabled = current.ExtraUsageEnabled
		merged.ExtraUsageDisabledReason = current.ExtraUsageDisabledReason
	} else if current.ExtraUsageDisabledReason != "" {
		merged.ExtraUsageDisabledReason = current.ExtraUsageDisabledReason
	}
	if current.AccountCreatedAtMS != nil {
		merged.AccountCreatedAtMS = current.AccountCreatedAtMS
	}
	if current.SubscriptionCreatedAtMS != nil {
		merged.SubscriptionCreatedAtMS = current.SubscriptionCreatedAtMS
	}
	return &merged
}

func mergeObservationQuotaWindows(
	previous []ObservationQuotaWindow,
	current []ObservationQuotaWindow,
	observedScopes []string,
) ([]ObservationQuotaWindow, bool) {
	covered := make(map[string]struct{}, len(observedScopes))
	for _, scope := range observedScopes {
		if scope != "" {
			covered[scope] = struct{}{}
		}
	}
	merged := make([]ObservationQuotaWindow, 0, len(previous)+len(current))
	primary := -1
	for _, window := range current {
		if _, ok := covered[window.Scope]; !ok {
			continue
		}
		if primary < 0 && window.IsPrimary {
			primary = len(merged)
		}
		window.IsPrimary = false
		merged = append(merged, window)
	}
	preservedPrevious := false
	for _, window := range previous {
		if _, ok := covered[window.Scope]; ok {
			continue
		}
		if primary < 0 && window.IsPrimary {
			primary = len(merged)
		}
		window.IsPrimary = false
		merged = append(merged, window)
		preservedPrevious = true
	}
	if primary < 0 && len(merged) > 0 {
		primary = 0
	}
	if primary >= 0 {
		merged[primary].IsPrimary = true
	}
	return merged, preservedPrevious
}

func subscriptionUpstreamHTTPStatus(err error) int {
	var upstream *subscriptionruntime.UpstreamHTTPError
	if errors.As(err, &upstream) && upstream != nil {
		return upstream.StatusCode
	}
	return 0
}

func (s *Service) recordCredentialObservationFailure(
	ctx context.Context,
	credential models.Credential,
	previous models.CredentialObservation,
	attemptMS int64,
	code string,
	summary string,
	authRefreshSecretVersion *uint64,
) (CredentialObservationResponse, error) {
	failed := previous
	failed.CredentialID = credential.ID
	failed.IdentityFingerprint = credential.IdentityFingerprint
	failed.SchemaVersion = 1
	if failed.ObservationVersion == 0 {
		failed.ObservationVersion = 1
	}
	if failed.State != models.CredentialObservationFresh {
		failed.State = models.CredentialObservationError
	}
	failed.LastAttemptAtMS = &attemptMS
	failed.NextAllowedAtMS = nil
	failed.LastErrorCode = code
	failed.UpdatedAtMS = attemptMS
	if authRefreshSecretVersion != nil {
		value := *authRefreshSecretVersion
		failed.LastAuthRefreshSecretVersion = &value
	}
	if len(failed.SnapshotJSON) == 0 {
		failed.SnapshotJSON = models.JSON(`{}`)
	}
	updates := map[string]any{
		"identity_fingerprint": failed.IdentityFingerprint,
		"schema_version":       failed.SchemaVersion,
		"state":                failed.State,
		"last_attempt_at_ms":   failed.LastAttemptAtMS,
		"next_allowed_at_ms":   failed.NextAllowedAtMS,
		"last_error_code":      failed.LastErrorCode,
		"updated_at_ms":        failed.UpdatedAtMS,
	}
	if authRefreshSecretVersion != nil {
		updates["last_auth_refresh_secret_version"] = failed.LastAuthRefreshSecretVersion
	}
	if err := s.upsertCredentialObservationMetadataOnly(ctx, credential.ID, failed, updates); err != nil {
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
	response := observationResponseValue(presentCredentialObservation(row, credential.IdentityFingerprint))
	s.enrichCredentialObservationUsage(ctx, credentialID, &response)
	return response, nil
}

func (s *Service) GetCredentialDetail(ctx context.Context, groupID, credentialID uint) (CredentialDetailResponse, error) {
	item, err := s.loadCredentialItem(ctx, groupID, credentialID)
	if err != nil {
		return CredentialDetailResponse{}, err
	}
	s.enrichCredentialObservationUsage(ctx, credentialID, item.Observation)
	s.enrichCredentialDailyUsage(ctx, credentialID, &item)
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
		if normalizeGroupConnectionType(group.ConnectionType) != models.ConnectionTypeSubscription {
			return app_errors.ErrValidation
		}
		if _, supported := s.subscriptions.QuotaObservation(channel.ID(group.ChannelID)); !supported {
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

// upsertCredentialObservationMetadataOnly creates a new row when none exists
// for credentialID, or otherwise updates only the given columns. It never
// touches snapshot_json, so an active failure or reset invalidation can never
// clobber a concurrent passive quota write with the snapshot it read before
// that write landed.
func (s *Service) upsertCredentialObservationMetadataOnly(
	ctx context.Context,
	credentialID uint,
	fallback models.CredentialObservation,
	updates map[string]any,
) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing models.CredentialObservation
		err := tx.Take(&existing, "credential_id = ?", credentialID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&fallback).Error
		}
		if err != nil {
			return err
		}
		return tx.Model(&models.CredentialObservation{}).Where("credential_id = ?", credentialID).Updates(updates).Error
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
	for _, observation := range observations {
		identity, exists := identities[observation.CredentialID]
		if !exists {
			continue
		}
		response := presentCredentialObservation(observation, identity)
		s.applyCredentialQuotaObservation(observation.CredentialID, response)
	}
	return nil
}

func mapCredentialObservation(row models.CredentialObservation) CredentialObservationResponse {
	response := CredentialObservationResponse{
		State: string(row.State), ObservationVersion: row.ObservationVersion,
		ObservedAtMS:    row.ObservedAtMS,
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

func presentCredentialObservation(row models.CredentialObservation, identityFingerprint string) *CredentialObservationResponse {
	if row.CredentialID == 0 || row.IdentityFingerprint != identityFingerprint {
		unavailable := CredentialObservationResponse{State: string(models.CredentialObservationUnavailable)}
		return &unavailable
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
		response.Snapshot == nil {
		if s != nil && s.registry != nil && credentialID != 0 {
			s.registry.SetCredentialQuotaObservation(credentialID, nil, time.Time{})
		}
		return
	}
	s.registry.ApplyQuotaWindows(credentialID, providerQuotaWindows(response.Snapshot.QuotaWindows))
}

// providerQuotaWindows narrows the API-facing snapshot windows to the fields
// the shared registry bottleneck projection reads. ObservedUsage is a
// read-time-only annotation and is intentionally not carried across.
func providerQuotaWindows(windows []ObservationQuotaWindow) []providerobservation.QuotaWindow {
	result := make([]providerobservation.QuotaWindow, 0, len(windows))
	for _, window := range windows {
		result = append(result, providerobservation.QuotaWindow{
			ID: window.ID, Label: window.Label, LabelKey: window.LabelKey,
			Scope: window.Scope, Unit: window.Unit,
			Used: window.Used, Limit: window.Limit, Remaining: window.Remaining, Utilization: window.Utilization,
			ResetAtMS: window.ResetAtMS, WindowSeconds: window.WindowSeconds, ModelIDs: window.ModelIDs,
			State: window.State, IsPrimary: window.IsPrimary,
		})
	}
	return result
}

// credentialDailyUsageWindow 是账号卡上「近 24 小时成功/失败」的窗口长度。
const credentialDailyUsageWindow = 24 * time.Hour

func (s *Service) enrichCredentialActivities(
	ctx context.Context,
	items []CredentialItemResponse,
) {
	if s == nil || s.credentialActivity == nil || len(items) == 0 {
		return
	}
	credentialIDs := make([]uint, 0, len(items))
	for index := range items {
		if items[index].ConnectionType == string(models.ConnectionTypeSubscription) &&
			items[index].CredentialID != 0 {
			credentialIDs = append(credentialIDs, items[index].CredentialID)
		}
	}
	if len(credentialIDs) == 0 {
		return
	}
	now := s.now().UTC()
	fromMS := now.Add(-credentialDailyUsageWindow).UnixMilli()
	toMS := now.UnixMilli()
	if fromMS < 0 || toMS <= fromMS {
		return
	}
	activities, err := s.credentialActivity.QueryCredentialActivity(
		ctx,
		requestlog.CredentialActivityQuery{
			CredentialIDs: credentialIDs,
			FromMS:        fromMS,
			ToMS:          toMS,
		},
	)
	if err != nil {
		return
	}
	for index := range items {
		activity, exists := activities[items[index].CredentialID]
		if !exists {
			continue
		}
		items[index].LastUsedAtMS = activity.LastUsedAtMS
		items[index].DailyUsage = &CredentialDailyUsageResponse{
			WindowSeconds: int64(credentialDailyUsageWindow / time.Second),
			SuccessCount:  activity.SuccessCount,
			FailureCount:  activity.FailureCount,
			DataComplete:  activity.DataComplete,
		}
	}
}

func (s *Service) enrichCredentialDailyUsage(
	ctx context.Context,
	credentialID uint,
	item *CredentialItemResponse,
) {
	if item == nil || credentialID == 0 {
		return
	}
	item.CredentialID = credentialID
	items := []CredentialItemResponse{*item}
	s.enrichCredentialActivities(ctx, items)
	*item = items[0]
}

func (s *Service) enrichCredentialObservationUsage(
	ctx context.Context,
	credentialID uint,
	response *CredentialObservationResponse,
) {
	if s == nil || s.credentialWindowUsage == nil || credentialID == 0 ||
		response == nil || response.State != string(models.CredentialObservationFresh) ||
		response.Snapshot == nil {
		return
	}
	for index := range response.Snapshot.QuotaWindows {
		window := &response.Snapshot.QuotaWindows[index]
		if window.Scope != "account" || window.ResetAtMS == nil ||
			window.WindowSeconds == nil || *window.WindowSeconds <= 0 ||
			*window.ResetAtMS <= 0 {
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
		if fromMS >= *window.ResetAtMS {
			continue
		}
		observed, err := s.credentialWindowUsage.QueryCredentialWindowUsage(ctx, requestlog.CredentialWindowUsageQuery{
			CredentialID: credentialID,
			FromMS:       fromMS,
			ToMS:         *window.ResetAtMS,
			Source:       requestlog.CredentialWindowUsageSourceHourlyStats,
		})
		if err != nil {
			continue
		}
		mapped, ok := mapObservationWindowUsage(fromMS, *window.ResetAtMS, observed)
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
