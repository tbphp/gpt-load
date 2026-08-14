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
	"gpt-load/internal/codex"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

const (
	credentialStageReadyTTL     = 30 * time.Minute
	credentialStageAuthTTL      = 5 * time.Minute
	credentialStageTombstoneTTL = 24 * time.Hour
	maxOAuthFileBytes           = 64 * 1024
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
}

type stagedCodexPayload struct {
	Credential codex.Credential `json:"credential,omitempty"`
	State      string           `json:"state,omitempty"`
	Verifier   string           `json:"verifier,omitempty"`
}

func (s *Service) ImportCredentialStage(
	ctx context.Context,
	channelID channel.ID,
	raw []byte,
) (CredentialStageResult, error) {
	if s == nil || s.db == nil || s.encryption == nil || channelID != channel.Codex {
		return CredentialStageResult{}, app_errors.ErrValidation
	}
	if len(raw) > maxOAuthFileBytes {
		return CredentialStageResult{}, app_errors.ErrOAuthFileTooLarge
	}
	credential, err := codex.ParseCredentialJSON(raw)
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrOAuthFileInvalid
	}
	credential, err = s.prepareImportedCodexCredential(ctx, credential)
	if err != nil {
		return CredentialStageResult{}, err
	}
	return s.persistReadyCredentialStage(ctx, channelID, "oauth_file", credential)
}

func (s *Service) prepareImportedCodexCredential(
	ctx context.Context,
	credential codex.Credential,
) (codex.Credential, error) {
	expiresAt, known := codex.CredentialExpiresAt(credential)
	if !known || expiresAt.After(s.now().Add(5*time.Minute)) {
		return credential, nil
	}
	if s.refreshCodexCredential == nil {
		return codex.Credential{}, app_errors.ErrAuthorizationUnavailable
	}
	refreshContext, cancel := context.WithTimeout(ctx, defaultSubscriptionControlTimeout)
	defer cancel()
	refreshed, err := s.refreshCodexCredential(refreshContext, credential)
	if err != nil {
		var tokenErr *codex.TokenEndpointError
		if errors.Is(err, codex.ErrCredentialIdentityChanged) ||
			errors.As(err, &tokenErr) && codex.IsDefinitiveRefreshRejection(tokenErr.Code) {
			return codex.Credential{}, app_errors.ErrCredentialReauthorizationRequired
		}
		return codex.Credential{}, app_errors.ErrCredentialAuthOutcomeUnknown
	}
	if refreshed.AccountID != credential.AccountID {
		return codex.Credential{}, app_errors.ErrCredentialReauthorizationRequired
	}
	return refreshed, nil
}

func (s *Service) BeginCredentialAuthorization(
	ctx context.Context,
	channelID channel.ID,
) (CredentialStageResult, error) {
	if s == nil || s.db == nil || s.encryption == nil || channelID != channel.Codex {
		return CredentialStageResult{}, app_errors.ErrValidation
	}
	authorization, err := codex.BeginBrowserAuthorization()
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrAuthorizationUnavailable
	}
	payload, err := json.Marshal(stagedCodexPayload{State: authorization.State, Verifier: authorization.CodeVerifier})
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
		EncryptedPayload: ciphertext, PayloadSchemaVersion: 1, SafeSummaryJSON: models.JSON(`{}`),
		OAuthStateHash: pointerTo(s.encryption.Hash("oauth-state/v1|" + authorization.State)),
		ExpiresAtMS:    expiresAt.UnixMilli(), CreatedAtMS: now.UnixMilli(), UpdatedAtMS: now.UnixMilli(),
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return CredentialStageResult{}, app_errors.ParseDBError(err)
	}
	return CredentialStageResult{
		StageID: row.ID, Status: string(row.Status), AuthorizationURL: authorization.AuthorizationURL,
		Account: CredentialStageAccount{}, ExpiresAtMS: row.ExpiresAtMS,
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
	if s == nil || s.completeBrowserAuthorization == nil || strings.TrimSpace(returnedState) == "" ||
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
	var payload stagedCodexPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		if markErr := s.markCredentialStageOutcomeUnknown(ctx, row.ID); markErr != nil {
			return CredentialStageResult{}, app_errors.ErrInternalServer
		}
		return CredentialStageResult{}, app_errors.ErrAuthorizationExchangeFailed
	}
	exchangeContext, cancelExchange := context.WithTimeout(context.WithoutCancel(ctx), defaultSubscriptionControlTimeout)
	defer cancelExchange()
	credential, err := s.completeBrowserAuthorization(exchangeContext, codex.BrowserAuthorizationCompletion{
		ExpectedState: payload.State, ReturnedState: returnedState,
		Code: code, CodeVerifier: payload.Verifier,
	})
	if err != nil {
		var tokenErr *codex.TokenEndpointError
		var finalizeErr error
		if errors.As(err, &tokenErr) && definitiveAuthorizationCodeRejection(tokenErr.Code) {
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

func definitiveAuthorizationCodeRejection(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "invalid_grant", "access_denied":
		return true
	default:
		return false
	}
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
	credential codex.Credential,
) (CredentialStageResult, error) {
	payload, err := json.Marshal(stagedCodexPayload{Credential: credential})
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
	summary := codexCredentialAccount(credential)
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrInternalServer
	}
	now := s.now().UTC()
	row := models.CredentialStage{
		ID: stageID, ChannelID: string(channelID), ConnectionType: models.ConnectionTypeSubscription,
		AuthorizationMethod: method, Status: models.CredentialStageReady,
		EncryptedPayload: ciphertext, PayloadSchemaVersion: 1,
		SafeSummaryJSON:     models.JSON(summaryJSON),
		IdentityFingerprint: s.subscriptionIdentityFingerprint(channelID, credential.AccountID),
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
	credential codex.Credential,
) (CredentialStageResult, error) {
	payload, err := json.Marshal(stagedCodexPayload{Credential: credential})
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrInternalServer
	}
	ciphertext, err := s.encryption.Encrypt(string(payload))
	clear(payload)
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrInternalServer
	}
	summary := codexCredentialAccount(credential)
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
			"safe_summary_json":    models.JSON(summaryJSON),
			"identity_fingerprint": s.subscriptionIdentityFingerprint(channel.ID(row.ChannelID), credential.AccountID),
			"expires_at_ms":        expiresAtMS, "updated_at_ms": nowMS,
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
	return s.encryption.Hash("credential-identity/v1|" + string(channelID) + "|codex|" + strings.TrimSpace(accountID))
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
		var payload stagedCodexPayload
		if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
			plaintext = ""
			return 0, app_errors.ErrStagedCredentialMismatch
		}
		plaintext = ""
		canonical, err := codex.MarshalCredential(payload.Credential)
		if err != nil {
			return 0, app_errors.ErrInternalServer
		}
		credential, err := codex.ParseCredentialJSON(canonical)
		if err != nil {
			clear(canonical)
			return 0, app_errors.ErrStagedCredentialMismatch
		}
		identity := s.subscriptionIdentityFingerprint(channelID, credential.AccountID)
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

func codexCredentialAccount(credential codex.Credential) CredentialStageAccount {
	account := CredentialStageAccount{EmailMask: maskEmail(credential.Email)}
	account.ExpiresAtMS = credentialTimestampMS(credential.Expire)
	account.LastRefreshAtMS = credentialTimestampMS(credential.LastRefresh)
	return account
}

func credentialTimestampMS(value string) *int64 {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return nil
	}
	result := parsed.UTC().UnixMilli()
	return &result
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
