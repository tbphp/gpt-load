package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

const (
	credentialStageReadyTTL     = 30 * time.Minute
	credentialStageAuthTTL      = 5 * time.Minute
	credentialStageTombstoneTTL = 24 * time.Hour
	maxOAuthFileBytes           = 64 * 1024
	stagedSubscriptionSchemaV2  = 2
)

type CredentialStageAccount struct {
	EmailMask       string `json:"email_mask,omitempty"`
	ExpiresAtMS     *int64 `json:"expires_at_ms,omitempty"`
	LastRefreshAtMS *int64 `json:"last_refresh_at_ms,omitempty"`
}

type CredentialStageResult struct {
	StageID          string                 `json:"stage_id"`
	Status           string                 `json:"status"`
	AuthorizationURL string                 `json:"authorization_url,omitempty"`
	Account          CredentialStageAccount `json:"account"`
	ExpiresAtMS      int64                  `json:"expires_at_ms"`
	ErrorCode        string                 `json:"error_code,omitempty"`
	LocalCallback    bool                   `json:"-"`
}

type stagedSubscriptionPayload struct {
	Credential  json.RawMessage `json:"credential,omitempty"`
	State       string          `json:"state,omitempty"`
	DriverState json.RawMessage `json:"driver_state,omitempty"`
}

// Version 1 was the Codex-only envelope used before subscription drivers were
// generalized. It remains readable only for short-lived stages created before
// an in-place upgrade.
type stagedSubscriptionPayloadV1 struct {
	State    string `json:"state,omitempty"`
	Verifier string `json:"verifier,omitempty"`
}

func decodeStagedAuthorizationPayload(schemaVersion uint, plaintext []byte) (stagedSubscriptionPayload, error) {
	switch schemaVersion {
	case 1:
		var legacy stagedSubscriptionPayloadV1
		if err := json.Unmarshal(plaintext, &legacy); err != nil {
			return stagedSubscriptionPayload{}, err
		}
		driverState, err := json.Marshal(struct {
			Verifier string `json:"verifier"`
		}{Verifier: legacy.Verifier})
		if err != nil {
			return stagedSubscriptionPayload{}, err
		}
		return stagedSubscriptionPayload{State: legacy.State, DriverState: driverState}, nil
	case stagedSubscriptionSchemaV2:
		var payload stagedSubscriptionPayload
		if err := json.Unmarshal(plaintext, &payload); err != nil {
			return stagedSubscriptionPayload{}, err
		}
		return payload, nil
	default:
		return stagedSubscriptionPayload{}, fmt.Errorf("unsupported credential stage payload schema version %d", schemaVersion)
	}
}

func (s *Service) subscriptionDriver(channelID channel.ID) (subscriptionruntime.Driver, error) {
	if s == nil || s.channelRegistry == nil || s.subscriptions == nil {
		return nil, app_errors.ErrValidation
	}
	connectionType, ok := s.channelRegistry.ConnectionType(channelID)
	if !ok || connectionType != string(models.ConnectionTypeSubscription) {
		return nil, app_errors.ErrValidation
	}
	driver, ok := s.subscriptions.Driver(channelID)
	if !ok {
		return nil, app_errors.ErrValidation
	}
	return driver, nil
}

func (s *Service) ImportCredentialStage(
	ctx context.Context,
	channelID channel.ID,
	raw []byte,
) (CredentialStageResult, error) {
	if s == nil || s.db == nil || s.encryption == nil {
		return CredentialStageResult{}, app_errors.ErrValidation
	}
	if s.channelRegistry == nil ||
		!s.channelRegistry.SupportsAuthorizationMethod(channelID, channel.AuthorizationOAuthFile) {
		return CredentialStageResult{}, app_errors.ErrValidation
	}
	driver, err := s.subscriptionDriver(channelID)
	if err != nil {
		return CredentialStageResult{}, err
	}
	if len(raw) > maxOAuthFileBytes {
		return CredentialStageResult{}, app_errors.ErrOAuthFileTooLarge
	}
	credential, err := driver.Parse(raw)
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrOAuthFileInvalid
	}
	credential, err = s.prepareImportedSubscriptionCredential(ctx, channelID, driver, credential)
	if err != nil {
		return CredentialStageResult{}, err
	}
	return s.persistReadyCredentialStage(ctx, channelID, "oauth_file", credential)
}

func (s *Service) prepareImportedSubscriptionCredential(
	ctx context.Context,
	channelID channel.ID,
	driver subscriptionruntime.Driver,
	credential subscriptionruntime.Credential,
) (subscriptionruntime.Credential, error) {
	expiresAt, known := credential.ExpiresAt()
	if !known || expiresAt.After(s.now().Add(5*time.Minute)) {
		return credential, nil
	}
	if s.refreshSubscriptionCredential == nil {
		return subscriptionruntime.Credential{}, app_errors.ErrAuthorizationUnavailable
	}
	refreshContext, cancel := context.WithTimeout(ctx, defaultSubscriptionControlTimeout)
	defer cancel()
	refreshed, err := s.refreshSubscriptionCredential(refreshContext, channelID, credential)
	if err != nil {
		if driver.ClassifyRefreshFailure(err) != subscriptionruntime.RefreshFailureOutcomeUnknown {
			return subscriptionruntime.Credential{}, app_errors.ErrCredentialReauthorizationRequired
		}
		return subscriptionruntime.Credential{}, app_errors.ErrCredentialAuthOutcomeUnknown
	}
	if refreshed.Identity() == "" || refreshed.Identity() != credential.Identity() {
		return subscriptionruntime.Credential{}, app_errors.ErrCredentialReauthorizationRequired
	}
	return refreshed, nil
}

func (s *Service) BeginCredentialAuthorization(
	ctx context.Context,
	channelID channel.ID,
) (CredentialStageResult, error) {
	if s == nil || s.db == nil || s.encryption == nil {
		return CredentialStageResult{}, app_errors.ErrValidation
	}
	if s.channelRegistry == nil ||
		!s.channelRegistry.SupportsAuthorizationMethod(channelID, channel.AuthorizationBrowserOAuth) {
		return CredentialStageResult{}, app_errors.ErrValidation
	}
	if _, err := s.subscriptionDriver(channelID); err != nil {
		return CredentialStageResult{}, err
	}
	if s.beginSubscriptionAuthorization == nil {
		return CredentialStageResult{}, app_errors.ErrAuthorizationUnavailable
	}
	authorization, err := s.beginSubscriptionAuthorization(channelID)
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrAuthorizationUnavailable
	}
	payload, err := json.Marshal(stagedSubscriptionPayload{State: authorization.State, DriverState: authorization.DriverState})
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrInternalServer
	}
	ciphertext, err := s.encryption.Encrypt(string(payload))
	clear(payload)
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrInternalServer
	}
	stageID, err := newOperationID(s.random)
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrInternalServer
	}
	now := s.now().UTC()
	expiresAt := now.Add(credentialStageAuthTTL)
	row := models.CredentialStage{
		ID: stageID, ChannelID: string(channelID),
		ConnectionType:      models.ConnectionTypeSubscription,
		AuthorizationMethod: "browser_oauth", Status: models.CredentialStagePendingAuthorization,
		EncryptedPayload: ciphertext, PayloadSchemaVersion: stagedSubscriptionSchemaV2, SafeSummaryJSON: models.JSON(`{}`),
		OAuthStateHash: pointerTo(s.encryption.Hash("oauth-state/v1|" + authorization.State)),
		ExpiresAtMS:    expiresAt.UnixMilli(), CreatedAtMS: now.UnixMilli(), UpdatedAtMS: now.UnixMilli(),
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return CredentialStageResult{}, app_errors.ParseDBError(err)
	}
	return CredentialStageResult{
		StageID: row.ID, Status: string(row.Status), AuthorizationURL: authorization.URL,
		Account: CredentialStageAccount{}, ExpiresAtMS: row.ExpiresAtMS,
		LocalCallback: authorization.LocalCallback,
	}, nil
}

// CompleteCredentialAuthorization atomically claims one pending state before
// issuing the one-shot OAuth code exchange.
func (s *Service) CompleteCredentialAuthorization(
	ctx context.Context,
	returnedState string,
	code string,
) (CredentialStageResult, error) {
	return s.completeCredentialAuthorization(ctx, "", returnedState, code)
}

func (s *Service) completeCredentialAuthorizationForStage(
	ctx context.Context,
	stageID string,
	returnedState string,
	code string,
) (CredentialStageResult, error) {
	if strings.TrimSpace(stageID) == "" {
		return CredentialStageResult{}, app_errors.ErrAuthorizationStateInvalid
	}
	return s.completeCredentialAuthorization(ctx, stageID, returnedState, code)
}

func (s *Service) completeCredentialAuthorization(
	ctx context.Context,
	stageID string,
	returnedState string,
	code string,
) (CredentialStageResult, error) {
	if s == nil || s.completeSubscriptionAuthorization == nil || strings.TrimSpace(returnedState) == "" ||
		strings.TrimSpace(code) == "" {
		return CredentialStageResult{}, app_errors.ErrAuthorizationStateInvalid
	}
	stateHash := s.encryption.Hash("oauth-state/v1|" + returnedState)
	var row models.CredentialStage
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Where("oauth_state_hash = ? AND status = ?", stateHash, models.CredentialStagePendingAuthorization)
		if stageID != "" {
			query = query.Where("id = ?", stageID)
		}
		if err := query.Take(&row).Error; err != nil {
			return err
		}
		if s.now().UnixMilli() >= row.ExpiresAtMS {
			return app_errors.ErrStagedCredentialExpired
		}
		result := tx.Model(&models.CredentialStage{}).
			Where("id = ? AND status = ? AND oauth_state_hash = ?", row.ID, models.CredentialStagePendingAuthorization, stateHash).
			Updates(map[string]any{
				"status": models.CredentialStageExchanging, "oauth_state_hash": nil,
				"expires_at_ms": s.now().Add(defaultSubscriptionControlTimeout).UnixMilli(),
				"updated_at_ms": s.now().UnixMilli(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, app_errors.ErrStagedCredentialExpired) {
			return CredentialStageResult{}, err
		}
		return CredentialStageResult{}, app_errors.ErrAuthorizationStateInvalid
	}
	plaintext, err := s.encryption.Decrypt(row.EncryptedPayload)
	if err != nil {
		if markErr := s.markCredentialStageOutcomeUnknown(ctx, row.ID); markErr != nil {
			return CredentialStageResult{}, app_errors.ErrInternalServer
		}
		return CredentialStageResult{}, app_errors.ErrAuthorizationExchangeFailed
	}
	payload, err := decodeStagedAuthorizationPayload(row.PayloadSchemaVersion, []byte(plaintext))
	if err != nil {
		if markErr := s.markCredentialStageOutcomeUnknown(ctx, row.ID); markErr != nil {
			return CredentialStageResult{}, app_errors.ErrInternalServer
		}
		return CredentialStageResult{}, app_errors.ErrAuthorizationExchangeFailed
	}
	exchangeContext, cancelExchange := context.WithTimeout(context.WithoutCancel(ctx), defaultSubscriptionControlTimeout)
	defer cancelExchange()
	channelID := channel.ID(row.ChannelID)
	driver, driverErr := s.subscriptionDriver(channelID)
	if driverErr != nil {
		if markErr := s.markCredentialStageOutcomeUnknown(ctx, row.ID); markErr != nil {
			return CredentialStageResult{}, app_errors.ErrInternalServer
		}
		return CredentialStageResult{}, app_errors.ErrAuthorizationExchangeFailed
	}
	credential, err := s.completeSubscriptionAuthorization(exchangeContext, channelID, subscriptionruntime.AuthorizationCompletion{
		ExpectedState: payload.State, ReturnedState: returnedState,
		Code: code, DriverState: payload.DriverState,
	})
	if err != nil {
		var finalizeErr error
		browser, browserOK := driver.(subscriptionruntime.BrowserAuthorizationDriver)
		if browserOK && browser.AuthorizationFailureDefinitive(err) {
			finalizeErr = s.failCredentialStageExchange(ctx, row.ID, "authorization_exchange_rejected")
		} else {
			finalizeErr = s.markCredentialStageOutcomeUnknown(ctx, row.ID)
		}
		if finalizeErr != nil {
			return CredentialStageResult{}, app_errors.ErrInternalServer
		}
		return CredentialStageResult{}, app_errors.ErrAuthorizationExchangeFailed
	}
	finalizeContext, cancelFinalize := credentialStageFinalizeContext(ctx)
	defer cancelFinalize()
	result, err := s.finishCredentialStageExchange(finalizeContext, row, credential)
	if err != nil {
		if markErr := s.markCredentialStageOutcomeUnknown(ctx, row.ID); markErr != nil {
			return CredentialStageResult{}, app_errors.ErrInternalServer
		}
		return CredentialStageResult{}, err
	}
	return result, nil
}

// FailCredentialAuthorization consumes a provider rejection without retaining
// its untrusted query text or leaving the Stage pending until expiry.
func (s *Service) FailCredentialAuthorization(ctx context.Context, returnedState string, providerError string) error {
	return s.failCredentialAuthorization(ctx, "", returnedState, providerError)
}

func (s *Service) failCredentialAuthorizationForStage(ctx context.Context, stageID string, returnedState string, providerError string) error {
	if strings.TrimSpace(stageID) == "" {
		return app_errors.ErrAuthorizationStateInvalid
	}
	return s.failCredentialAuthorization(ctx, stageID, returnedState, providerError)
}

func (s *Service) failCredentialAuthorization(ctx context.Context, stageID string, returnedState string, providerError string) error {
	if s == nil || strings.TrimSpace(returnedState) == "" || strings.TrimSpace(providerError) == "" {
		return app_errors.ErrAuthorizationStateInvalid
	}
	errorCode := "authorization_failed"
	if strings.EqualFold(strings.TrimSpace(providerError), "access_denied") {
		errorCode = "authorization_denied"
	}
	nowMS := s.now().UnixMilli()
	stateHash := s.encryption.Hash("oauth-state/v1|" + returnedState)
	query := s.db.WithContext(ctx).Model(&models.CredentialStage{}).
		Where("oauth_state_hash = ? AND status = ? AND expires_at_ms > ?", stateHash, models.CredentialStagePendingAuthorization, nowMS)
	if stageID != "" {
		query = query.Where("id = ?", stageID)
	}
	result := query.
		Updates(map[string]any{
			"status": models.CredentialStageFailed, "encrypted_payload": "",
			"oauth_state_hash": nil, "error_code": errorCode, "updated_at_ms": nowMS,
		})
	if result.Error != nil {
		return app_errors.ParseDBError(result.Error)
	}
	if result.RowsAffected != 1 {
		return app_errors.ErrAuthorizationStateInvalid
	}
	return nil
}

func (s *Service) GetCredentialStage(ctx context.Context, stageID string) (CredentialStageResult, error) {
	row, err := s.loadCredentialStage(ctx, stageID)
	if err != nil {
		return CredentialStageResult{}, err
	}
	if (row.Status == models.CredentialStagePendingAuthorization || row.Status == models.CredentialStageReady ||
		row.Status == models.CredentialStageExchanging) &&
		s.now().UnixMilli() >= row.ExpiresAtMS {
		if err := s.expireCredentialStage(ctx, &row); err != nil {
			return CredentialStageResult{}, app_errors.ParseDBError(err)
		}
	}
	var account CredentialStageAccount
	if len(row.SafeSummaryJSON) > 0 {
		_ = json.Unmarshal(row.SafeSummaryJSON, &account)
	}
	return CredentialStageResult{
		StageID: row.ID, Status: string(row.Status), Account: account, ExpiresAtMS: row.ExpiresAtMS,
		ErrorCode: stageResultErrorCode(row.Status, row.ErrorCode),
	}, nil
}

func (s *Service) CancelCredentialStage(ctx context.Context, stageID string) error {
	if strings.TrimSpace(stageID) == "" {
		return app_errors.ErrBadRequest
	}
	result := s.db.WithContext(ctx).Model(&models.CredentialStage{}).
		Where("id = ? AND status IN ?", stageID, []models.CredentialStageStatus{
			models.CredentialStagePendingAuthorization, models.CredentialStageReady,
		}).Updates(map[string]any{
		"status": models.CredentialStageCancelled, "encrypted_payload": "",
		"oauth_state_hash": nil, "updated_at_ms": s.now().UnixMilli(),
	})
	if result.Error != nil {
		return app_errors.ParseDBError(result.Error)
	}
	if result.RowsAffected != 1 {
		return app_errors.ErrStagedCredentialNotReady
	}
	return nil
}

func (s *Service) persistReadyCredentialStage(
	ctx context.Context,
	channelID channel.ID,
	method string,
	credential subscriptionruntime.Credential,
) (CredentialStageResult, error) {
	payload, err := json.Marshal(stagedSubscriptionPayload{Credential: credential.Canonical()})
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrInternalServer
	}
	ciphertext, err := s.encryption.Encrypt(string(payload))
	clear(payload)
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrInternalServer
	}
	stageID, err := newOperationID(s.random)
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrInternalServer
	}
	summary := subscriptionCredentialAccount(credential)
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrInternalServer
	}
	now := s.now().UTC()
	row := models.CredentialStage{
		ID: stageID, ChannelID: string(channelID), ConnectionType: models.ConnectionTypeSubscription,
		AuthorizationMethod: method, Status: models.CredentialStageReady,
		EncryptedPayload: ciphertext, PayloadSchemaVersion: stagedSubscriptionSchemaV2,
		SafeSummaryJSON:     models.JSON(summaryJSON),
		IdentityFingerprint: s.subscriptionIdentityFingerprint(channelID, credential.Identity()),
		ExpiresAtMS:         now.Add(credentialStageReadyTTL).UnixMilli(),
		CreatedAtMS:         now.UnixMilli(), UpdatedAtMS: now.UnixMilli(),
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return CredentialStageResult{}, app_errors.ParseDBError(err)
	}
	return CredentialStageResult{
		StageID: row.ID, Status: string(row.Status), Account: summary, ExpiresAtMS: row.ExpiresAtMS,
	}, nil
}

func (s *Service) finishCredentialStageExchange(
	ctx context.Context,
	row models.CredentialStage,
	credential subscriptionruntime.Credential,
) (CredentialStageResult, error) {
	payload, err := json.Marshal(stagedSubscriptionPayload{Credential: credential.Canonical()})
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrInternalServer
	}
	ciphertext, err := s.encryption.Encrypt(string(payload))
	clear(payload)
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrInternalServer
	}
	summary := subscriptionCredentialAccount(credential)
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrInternalServer
	}
	nowMS := s.now().UnixMilli()
	expiresAtMS := s.now().Add(credentialStageReadyTTL).UnixMilli()
	result := s.db.WithContext(ctx).Model(&models.CredentialStage{}).
		Where("id = ? AND status = ?", row.ID, models.CredentialStageExchanging).
		Updates(map[string]any{
			"status": models.CredentialStageReady, "encrypted_payload": ciphertext,
			"payload_schema_version": stagedSubscriptionSchemaV2,
			"safe_summary_json":      models.JSON(summaryJSON),
			"identity_fingerprint":   s.subscriptionIdentityFingerprint(channel.ID(row.ChannelID), credential.Identity()),
			"expires_at_ms":          expiresAtMS, "updated_at_ms": nowMS,
		})
	if result.Error != nil {
		return CredentialStageResult{}, app_errors.ParseDBError(result.Error)
	}
	if result.RowsAffected != 1 {
		return CredentialStageResult{}, app_errors.ErrAuthorizationStateInvalid
	}
	return CredentialStageResult{
		StageID: row.ID, Status: string(models.CredentialStageReady), Account: summary, ExpiresAtMS: expiresAtMS,
	}, nil
}

func (s *Service) markCredentialStageOutcomeUnknown(ctx context.Context, stageID string) error {
	finalizeContext, cancel := credentialStageFinalizeContext(ctx)
	defer cancel()
	return s.db.WithContext(finalizeContext).Model(&models.CredentialStage{}).
		Where("id = ? AND status = ?", stageID, models.CredentialStageExchanging).
		Updates(map[string]any{
			"status":            models.CredentialStageOutcomeUnknown,
			"encrypted_payload": "", "error_code": "authorization_exchange_unknown",
			"updated_at_ms": s.now().UnixMilli(),
		}).Error
}

func (s *Service) failCredentialStageExchange(ctx context.Context, stageID string, code string) error {
	finalizeContext, cancel := credentialStageFinalizeContext(ctx)
	defer cancel()
	return s.db.WithContext(finalizeContext).Model(&models.CredentialStage{}).
		Where("id = ? AND status = ?", stageID, models.CredentialStageExchanging).
		Updates(map[string]any{
			"status": models.CredentialStageFailed, "encrypted_payload": "",
			"error_code": code, "updated_at_ms": s.now().UnixMilli(),
		}).Error
}

func credentialStageFinalizeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), controlTransactionCleanupTimeout)
}

func (s *Service) subscriptionIdentityFingerprint(channelID channel.ID, accountID string) string {
	driver, ok := subscriptionsDriver(s.subscriptions, channelID)
	if !ok || strings.TrimSpace(accountID) == "" {
		return ""
	}
	return s.encryption.Hash("credential-identity/v1|" + string(channelID) + "|" + string(driver.ID()) + "|" + strings.TrimSpace(accountID))
}

func normalizeCredentialStageIDs(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > maxCredentialLines {
		return nil, app_errors.ErrValidation
	}
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, app_errors.ErrValidation
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, app_errors.ErrDuplicateCredentialIdentity
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized, nil
}

// consumeCredentialStages promotes ready subscription credentials and consumes
// their short-lived stages in the caller's Group-create transaction.
func (s *Service) consumeCredentialStages(
	tx *gorm.DB,
	groupID uint,
	channelID channel.ID,
	connectionType models.ConnectionType,
	stageIDs []string,
) (int, error) {
	if s == nil || s.encryption == nil || tx == nil || groupID == 0 ||
		connectionType != models.ConnectionTypeSubscription || len(stageIDs) == 0 {
		return 0, app_errors.ErrValidation
	}
	var stages []models.CredentialStage
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id IN ?", stageIDs).Order("id ASC").Find(&stages).Error; err != nil {
		return 0, app_errors.ParseDBError(err)
	}
	if len(stages) != len(stageIDs) {
		return 0, app_errors.ErrStagedCredentialNotReady
	}
	nowMS := s.now().UnixMilli()
	identities := make(map[string]struct{}, len(stages))
	for _, stage := range stages {
		if stage.ChannelID != string(channelID) || stage.ConnectionType != connectionType {
			return 0, app_errors.ErrStagedCredentialMismatch
		}
		switch stage.Status {
		case models.CredentialStageConsumed:
			return 0, app_errors.ErrStagedCredentialConsumed
		case models.CredentialStageReady:
		default:
			return 0, app_errors.ErrStagedCredentialNotReady
		}
		if nowMS >= stage.ExpiresAtMS {
			return 0, app_errors.ErrStagedCredentialExpired
		}
		if stage.IdentityFingerprint == "" {
			return 0, app_errors.ErrStagedCredentialMismatch
		}
		if _, duplicate := identities[stage.IdentityFingerprint]; duplicate {
			return 0, app_errors.ErrDuplicateCredentialIdentity
		}
		identities[stage.IdentityFingerprint] = struct{}{}
	}

	for _, stage := range stages {
		plaintext, err := s.encryption.Decrypt(stage.EncryptedPayload)
		if err != nil {
			return 0, app_errors.ErrStagedCredentialMismatch
		}
		var payload stagedSubscriptionPayload
		if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
			plaintext = ""
			return 0, app_errors.ErrStagedCredentialMismatch
		}
		plaintext = ""
		driver, driverOK := subscriptionsDriver(s.subscriptions, channelID)
		if !driverOK {
			return 0, app_errors.ErrStagedCredentialMismatch
		}
		credential, err := driver.Parse(payload.Credential)
		if err != nil {
			return 0, app_errors.ErrStagedCredentialMismatch
		}
		canonical := credential.Canonical()
		identity := s.subscriptionIdentityFingerprint(channelID, credential.Identity())
		if identity != stage.IdentityFingerprint {
			clear(canonical)
			return 0, app_errors.ErrStagedCredentialMismatch
		}
		fingerprint := s.encryption.Hash(string(canonical))
		ciphertext, err := s.encryption.Encrypt(string(canonical))
		clear(canonical)
		if err != nil {
			return 0, app_errors.ErrInternalServer
		}
		row := models.Credential{
			GroupID: groupID, Data: ciphertext, Fingerprint: fingerprint,
			IdentityFingerprint: identity, SecretVersion: 1,
			AuthState: models.CredentialAuthStateReady, Status: models.CredentialStatusActive,
			CreatedAtMS: nowMS, UpdatedAtMS: nowMS,
		}
		if err := tx.Create(&row).Error; err != nil {
			if app_errors.ParseDBError(err) == app_errors.ErrDuplicateResource {
				return 0, app_errors.ErrDuplicateCredentialIdentity
			}
			return 0, app_errors.ParseDBError(err)
		}
		result := tx.Model(&models.CredentialStage{}).
			Where("id = ? AND status = ?", stage.ID, models.CredentialStageReady).
			Updates(map[string]any{
				"status": models.CredentialStageConsumed, "encrypted_payload": "",
				"oauth_state_hash": nil, "consumed_at_ms": nowMS,
				"consumed_group_id": groupID, "updated_at_ms": nowMS,
			})
		if result.Error != nil {
			return 0, app_errors.ParseDBError(result.Error)
		}
		if result.RowsAffected != 1 {
			return 0, app_errors.ErrStagedCredentialConsumed
		}
	}
	return len(stages), nil
}

func (s *Service) loadCredentialStage(ctx context.Context, stageID string) (models.CredentialStage, error) {
	if strings.TrimSpace(stageID) == "" {
		return models.CredentialStage{}, app_errors.ErrBadRequest
	}
	var row models.CredentialStage
	if err := s.db.WithContext(ctx).Take(&row, "id = ?", stageID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.CredentialStage{}, app_errors.ErrResourceNotFound
		}
		return models.CredentialStage{}, app_errors.ParseDBError(err)
	}
	return row, nil
}

func (s *Service) expireCredentialStage(ctx context.Context, row *models.CredentialStage) error {
	status := models.CredentialStageExpired
	errorCode := ""
	if row.Status == models.CredentialStageExchanging {
		status = models.CredentialStageOutcomeUnknown
		errorCode = "authorization_exchange_interrupted"
	}
	result := s.db.WithContext(ctx).Model(&models.CredentialStage{}).
		Where("id = ? AND status = ?", row.ID, row.Status).
		Updates(map[string]any{
			"status": status, "encrypted_payload": "", "oauth_state_hash": nil,
			"error_code": errorCode, "updated_at_ms": s.now().UnixMilli(),
		})
	if result.Error != nil {
		return fmt.Errorf("expire credential stage: %w", result.Error)
	}
	if result.RowsAffected == 1 {
		row.Status = status
		row.EncryptedPayload = ""
		row.OAuthStateHash = nil
		row.ErrorCode = errorCode
		return nil
	}
	if err := s.db.WithContext(ctx).Take(row, "id = ?", row.ID).Error; err != nil {
		return fmt.Errorf("reload credential stage after expiry race: %w", err)
	}
	return nil
}

// CleanupCredentialStages removes expired secret material and prunes terminal
// tombstones after their short audit/debug window.
func (s *Service) CleanupCredentialStages(ctx context.Context, now time.Time) error {
	if s == nil || s.db == nil {
		return app_errors.ErrInternalServer
	}
	nowMS := now.UTC().UnixMilli()
	terminalBeforeMS := now.UTC().Add(-credentialStageTombstoneTTL).UnixMilli()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.CredentialStage{}).
			Where("expires_at_ms <= ? AND status = ?", nowMS, models.CredentialStageExchanging).
			Updates(map[string]any{
				"status":            models.CredentialStageOutcomeUnknown,
				"encrypted_payload": "", "oauth_state_hash": nil,
				"error_code": "authorization_exchange_interrupted", "updated_at_ms": nowMS,
			}).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		if err := tx.Model(&models.CredentialStage{}).
			Where("expires_at_ms <= ? AND status IN ?", nowMS, []models.CredentialStageStatus{
				models.CredentialStagePendingAuthorization,
				models.CredentialStageReady,
			}).Updates(map[string]any{
			"status": models.CredentialStageExpired, "encrypted_payload": "",
			"oauth_state_hash": nil, "updated_at_ms": nowMS,
		}).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		if err := tx.Where("updated_at_ms < ? AND status IN ?", terminalBeforeMS, []models.CredentialStageStatus{
			models.CredentialStageConsumed,
			models.CredentialStageFailed,
			models.CredentialStageCancelled,
			models.CredentialStageExpired,
			models.CredentialStageOutcomeUnknown,
		}).Delete(&models.CredentialStage{}).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		return nil
	})
}

func maskEmail(email string) string {
	email = strings.TrimSpace(email)
	parts := strings.Split(email, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	local := []rune(parts[0])
	if len(local) == 1 {
		return string(local[0]) + "***@" + parts[1]
	}
	return string(local[0]) + "***" + string(local[len(local)-1]) + "@" + parts[1]
}

func subscriptionCredentialAccount(credential subscriptionruntime.Credential) CredentialStageAccount {
	metadata := credential.Account()
	account := CredentialStageAccount{EmailMask: maskEmail(metadata.Email)}
	if metadata.ExpiresAtKnown {
		value := metadata.ExpiresAt.UTC().UnixMilli()
		account.ExpiresAtMS = &value
	}
	if metadata.LastRefreshKnown {
		value := metadata.LastRefresh.UTC().UnixMilli()
		account.LastRefreshAtMS = &value
	}
	return account
}

func stageResultErrorCode(status models.CredentialStageStatus, code string) string {
	switch status {
	case models.CredentialStageFailed, models.CredentialStageOutcomeUnknown:
		return safeInternalErrorCode(code)
	default:
		return ""
	}
}

func safeInternalErrorCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 64 {
		return ""
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return ""
		}
	}
	return value
}

func pointerTo[T any](value T) *T { return &value }
