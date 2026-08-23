package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gpt-load/internal/channel"
	"gpt-load/internal/outboundproxy"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

const (
	credentialStageReadyTTL     = 30 * time.Minute
	credentialStageAuthTTL      = 5 * time.Minute
	credentialStageDeviceMaxTTL = 30 * time.Minute
	credentialStageTombstoneTTL = 24 * time.Hour
	maxOAuthFileBytes           = 64 * 1024
	maxDeviceAuthorizationURL   = 4096
	maxDeviceAuthorizationCode  = 128
	stagedSubscriptionSchemaV2  = 2
)

func credentialImportContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, defaultSubscriptionControlTimeout)
}

func credentialImportAPIError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return app_errors.ErrBadGateway
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return app_errors.ErrBadGateway
	}
	var upstream interface{ HTTPStatusCode() int }
	if errors.As(err, &upstream) && upstream != nil {
		switch status := upstream.HTTPStatusCode(); {
		case status == http.StatusRequestTimeout,
			status == http.StatusTooEarly,
			status == http.StatusTooManyRequests,
			status >= http.StatusInternalServerError:
			return app_errors.ErrBadGateway
		}
	}
	return app_errors.ErrOAuthFileInvalid
}

type CredentialStageAccount struct {
	EmailMask       string `json:"email_mask,omitempty"`
	ExpiresAtMS     *int64 `json:"expires_at_ms,omitempty"`
	LastRefreshAtMS *int64 `json:"last_refresh_at_ms,omitempty"`
}

type CredentialStageResult struct {
	StageID             string                 `json:"stage_id"`
	Status              string                 `json:"status"`
	AuthorizationMethod string                 `json:"authorization_method,omitempty"`
	AuthorizationURL    string                 `json:"authorization_url,omitempty"`
	RedirectURI         string                 `json:"redirect_uri,omitempty"`
	UserCode            string                 `json:"user_code,omitempty"`
	NextPollAtMS        int64                  `json:"next_poll_at_ms,omitempty"`
	Account             CredentialStageAccount `json:"account"`
	ExpiresAtMS         int64                  `json:"expires_at_ms"`
	ErrorCode           string                 `json:"error_code,omitempty"`
}

type credentialStageAuthorizationSummary struct {
	AuthorizationURL string `json:"authorization_url"`
	UserCode         string `json:"user_code"`
	NextPollAtMS     int64  `json:"next_poll_at_ms"`
	PollIntervalMS   int64  `json:"poll_interval_ms"`
}

type credentialStageSafeSummary struct {
	Authorization *credentialStageAuthorizationSummary `json:"authorization,omitempty"`
}

type stagedSubscriptionPayload struct {
	Credential  json.RawMessage                     `json:"credential,omitempty"`
	State       string                              `json:"state,omitempty"`
	DriverState json.RawMessage                     `json:"driver_state,omitempty"`
	Network     *subscriptionruntime.NetworkContext `json:"network,omitempty"`
}

func (s *Service) stagedNetworkContext(
	ctx context.Context,
	payload stagedSubscriptionPayload,
) (subscriptionruntime.NetworkContext, error) {
	if payload.Network == nil {
		return s.globalNetworkContext(ctx, s.db)
	}
	network, err := s.proxyNetworkContext(payload.Network.Proxy)
	if err != nil || network.Fingerprint != payload.Network.Fingerprint {
		return subscriptionruntime.NetworkContext{}, app_errors.ErrInternalServer
	}
	return network, nil
}

func (s *Service) credentialStageNetworkContext(
	ctx context.Context,
	row models.CredentialStage,
) (subscriptionruntime.NetworkContext, error) {
	plaintext, err := s.encryption.Decrypt(row.EncryptedPayload)
	if err != nil {
		return subscriptionruntime.NetworkContext{}, app_errors.ErrInternalServer
	}
	payload, err := decodeStagedAuthorizationPayload(row.PayloadSchemaVersion, []byte(plaintext))
	plaintext = ""
	if err != nil {
		return subscriptionruntime.NetworkContext{}, app_errors.ErrInternalServer
	}
	return s.stagedNetworkContext(ctx, payload)
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
	proxyConfigs ...*outboundproxy.Config,
) (CredentialStageResult, error) {
	driver, err := s.credentialStageImportDriver(channelID, raw)
	if err != nil {
		return CredentialStageResult{}, err
	}
	var proxyConfig *outboundproxy.Config
	if len(proxyConfigs) > 0 {
		proxyConfig = proxyConfigs[0]
	}
	network, err := s.draftNetworkContext(ctx, proxyConfig)
	if err != nil {
		return CredentialStageResult{}, err
	}
	return s.importCredentialStageWithNetwork(ctx, channelID, raw, driver, network)
}

func (s *Service) ImportGroupCredentialStage(
	ctx context.Context,
	groupID uint,
	channelID channel.ID,
	raw []byte,
) (CredentialStageResult, error) {
	driver, err := s.credentialStageImportDriver(channelID, raw)
	if err != nil {
		return CredentialStageResult{}, err
	}
	network, err := s.groupCredentialStageNetworkContext(ctx, groupID, channelID)
	if err != nil {
		return CredentialStageResult{}, err
	}
	return s.importCredentialStageWithNetwork(ctx, channelID, raw, driver, network)
}

func (s *Service) credentialStageImportDriver(
	channelID channel.ID,
	raw []byte,
) (subscriptionruntime.Driver, error) {
	if s == nil || s.db == nil || s.encryption == nil {
		return nil, app_errors.ErrValidation
	}
	if s.channelRegistry == nil ||
		!s.channelRegistry.SupportsAuthorizationMethod(channelID, channel.AuthorizationOAuthFile) {
		return nil, app_errors.ErrValidation
	}
	driver, err := s.subscriptionDriver(channelID)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxOAuthFileBytes {
		return nil, app_errors.ErrOAuthFileTooLarge
	}
	return driver, nil
}

func (s *Service) importCredentialStageWithNetwork(
	ctx context.Context,
	channelID channel.ID,
	raw []byte,
	driver subscriptionruntime.Driver,
	network subscriptionruntime.NetworkContext,
) (CredentialStageResult, error) {
	ctx = subscriptionruntime.WithNetworkContext(ctx, network)
	importContext, cancelImport := credentialImportContext(ctx)
	defer cancelImport()
	credential, err := s.subscriptions.ImportCredential(importContext, channelID, raw)
	if err != nil {
		return CredentialStageResult{}, credentialImportAPIError(err)
	}
	credential, err = s.prepareTransientSubscriptionCredential(ctx, channelID, driver, credential)
	if err != nil {
		return CredentialStageResult{}, err
	}
	return s.persistReadyCredentialStage(ctx, channelID, "oauth_file", credential, network)
}

func (s *Service) prepareTransientSubscriptionCredential(
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

func (s *Service) prepareReadySubscriptionStageCredential(
	ctx context.Context,
	row models.CredentialStage,
	driver subscriptionruntime.Driver,
	credential subscriptionruntime.Credential,
	forceRefresh bool,
) (subscriptionruntime.Credential, error) {
	if err := ctx.Err(); err != nil {
		return subscriptionruntime.Credential{}, err
	}
	network, frozen := subscriptionruntime.NetworkFromContext(ctx)
	if !frozen {
		var err error
		network, err = s.credentialStageNetworkContext(ctx, row)
		if err != nil {
			return subscriptionruntime.Credential{}, err
		}
	}
	ctx = subscriptionruntime.WithNetworkContext(ctx, network)
	expiresAt, known := credential.ExpiresAt()
	if !forceRefresh && (!known || expiresAt.After(s.now().Add(5*time.Minute))) {
		return credential, nil
	}
	if s.refreshSubscriptionCredential == nil {
		return subscriptionruntime.Credential{}, app_errors.ErrAuthorizationUnavailable
	}
	now := s.now().UTC()
	if now.UnixMilli() >= row.ExpiresAtMS {
		return subscriptionruntime.Credential{}, app_errors.ErrStagedCredentialExpired
	}
	claim := s.db.WithContext(ctx).Model(&models.CredentialStage{}).
		Where(
			"id = ? AND status = ? AND encrypted_payload = ? AND expires_at_ms > ?",
			row.ID,
			models.CredentialStageReady,
			row.EncryptedPayload,
			now.UnixMilli(),
		).
		Updates(map[string]any{
			"status":        models.CredentialStageExchanging,
			"expires_at_ms": now.Add(defaultSubscriptionControlTimeout).UnixMilli(),
			"error_code":    "", "updated_at_ms": now.UnixMilli(),
		})
	if claim.Error != nil {
		return subscriptionruntime.Credential{}, app_errors.ParseDBError(claim.Error)
	}
	if claim.RowsAffected != 1 {
		return subscriptionruntime.Credential{}, app_errors.ErrStagedCredentialNotReady
	}

	refreshContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		defaultSubscriptionControlTimeout,
	)
	refreshed, refreshErr := s.refreshSubscriptionCredential(refreshContext, channel.ID(row.ChannelID), credential)
	cancel()
	if refreshErr != nil {
		status := models.CredentialStageOutcomeUnknown
		code := "credential_refresh_outcome_unknown"
		apiErr := app_errors.ErrCredentialAuthOutcomeUnknown
		switch driver.ClassifyRefreshFailure(refreshErr) {
		case subscriptionruntime.RefreshFailureIdentityChanged:
			status = models.CredentialStageFailed
			code = "credential_refresh_identity_changed"
			apiErr = app_errors.ErrCredentialReauthorizationRequired
		case subscriptionruntime.RefreshFailureReauthorizationRequired:
			status = models.CredentialStageFailed
			code = "credential_refresh_rejected"
			apiErr = app_errors.ErrCredentialReauthorizationRequired
		}
		if err := s.finishCredentialStageRefreshFailure(ctx, row.ID, status, code); err != nil {
			return subscriptionruntime.Credential{}, err
		}
		return subscriptionruntime.Credential{}, apiErr
	}
	if refreshed.Identity() == "" || refreshed.Identity() != credential.Identity() ||
		s.subscriptionIdentityFingerprint(channel.ID(row.ChannelID), refreshed.Identity()) != row.IdentityFingerprint {
		if err := s.finishCredentialStageRefreshFailure(
			ctx,
			row.ID,
			models.CredentialStageFailed,
			"credential_refresh_identity_changed",
		); err != nil {
			return subscriptionruntime.Credential{}, err
		}
		return subscriptionruntime.Credential{}, app_errors.ErrCredentialReauthorizationRequired
	}
	if err := s.finishCredentialStageRefresh(ctx, row, refreshed); err != nil {
		if finalizeErr := s.finishCredentialStageRefreshFailure(
			ctx,
			row.ID,
			models.CredentialStageOutcomeUnknown,
			"credential_refresh_persist_failed",
		); finalizeErr != nil {
			return subscriptionruntime.Credential{}, app_errors.ErrInternalServer
		}
		return subscriptionruntime.Credential{}, app_errors.ErrCredentialAuthOutcomeUnknown
	}
	return refreshed, nil
}

func (s *Service) finishCredentialStageRefresh(
	ctx context.Context,
	row models.CredentialStage,
	credential subscriptionruntime.Credential,
) error {
	network, err := s.credentialStageNetworkContext(ctx, row)
	if err != nil {
		return err
	}
	canonical := credential.Canonical()
	payload, err := json.Marshal(stagedSubscriptionPayload{Credential: canonical, Network: &network})
	clear(canonical)
	if err != nil {
		return err
	}
	ciphertext, err := s.encryption.Encrypt(string(payload))
	clear(payload)
	if err != nil {
		return err
	}
	summaryJSON, err := json.Marshal(subscriptionCredentialAccount(credential))
	if err != nil {
		return err
	}
	finalizeContext, cancel := credentialStageFinalizeContext(ctx)
	defer cancel()
	result := s.db.WithContext(finalizeContext).Model(&models.CredentialStage{}).
		Where("id = ? AND status = ?", row.ID, models.CredentialStageExchanging).
		Updates(map[string]any{
			"status": models.CredentialStageReady, "encrypted_payload": ciphertext,
			"payload_schema_version": stagedSubscriptionSchemaV2,
			"safe_summary_json":      models.JSON(summaryJSON),
			"identity_fingerprint":   row.IdentityFingerprint,
			"expires_at_ms":          row.ExpiresAtMS,
			"error_code":             "", "updated_at_ms": s.now().UnixMilli(),
		})
	if result.Error != nil {
		return app_errors.ParseDBError(result.Error)
	}
	if result.RowsAffected != 1 {
		return app_errors.ErrCredentialAuthOutcomeUnknown
	}
	return nil
}

func (s *Service) finishCredentialStageRefreshFailure(
	ctx context.Context,
	stageID string,
	status models.CredentialStageStatus,
	code string,
) error {
	finalizeContext, cancel := credentialStageFinalizeContext(ctx)
	defer cancel()
	result := s.db.WithContext(finalizeContext).Model(&models.CredentialStage{}).
		Where("id = ? AND status = ?", stageID, models.CredentialStageExchanging).
		Updates(map[string]any{
			"status": status, "encrypted_payload": "", "oauth_state_hash": nil,
			"error_code": code, "updated_at_ms": s.now().UnixMilli(),
		})
	if result.Error != nil {
		return app_errors.ParseDBError(result.Error)
	}
	if result.RowsAffected != 1 {
		return app_errors.ErrInternalServer
	}
	return nil
}

func (s *Service) BeginCredentialAuthorization(
	ctx context.Context,
	channelID channel.ID,
	proxyConfigs ...*outboundproxy.Config,
) (CredentialStageResult, error) {
	if err := s.validateCredentialAuthorization(channelID); err != nil {
		return CredentialStageResult{}, err
	}
	var proxyConfig *outboundproxy.Config
	if len(proxyConfigs) > 0 {
		proxyConfig = proxyConfigs[0]
	}
	network, err := s.draftNetworkContext(ctx, proxyConfig)
	if err != nil {
		return CredentialStageResult{}, err
	}
	return s.beginCredentialAuthorizationWithNetwork(ctx, channelID, network)
}

func (s *Service) BeginGroupCredentialAuthorization(
	ctx context.Context,
	groupID uint,
	channelID channel.ID,
) (CredentialStageResult, error) {
	if err := s.validateCredentialAuthorization(channelID); err != nil {
		return CredentialStageResult{}, err
	}
	network, err := s.groupCredentialStageNetworkContext(ctx, groupID, channelID)
	if err != nil {
		return CredentialStageResult{}, err
	}
	return s.beginCredentialAuthorizationWithNetwork(ctx, channelID, network)
}

func (s *Service) validateCredentialAuthorization(channelID channel.ID) error {
	if s == nil || s.db == nil || s.encryption == nil {
		return app_errors.ErrValidation
	}
	if s.channelRegistry == nil {
		return app_errors.ErrValidation
	}
	if _, err := s.subscriptionDriver(channelID); err != nil {
		return err
	}
	return nil
}

func (s *Service) beginCredentialAuthorizationWithNetwork(
	ctx context.Context,
	channelID channel.ID,
	network subscriptionruntime.NetworkContext,
) (CredentialStageResult, error) {
	if s.channelRegistry.SupportsAuthorizationMethod(channelID, channel.AuthorizationBrowserOAuth) {
		return s.beginBrowserCredentialAuthorization(ctx, channelID, network)
	}
	if s.channelRegistry.SupportsAuthorizationMethod(channelID, channel.AuthorizationDeviceOAuth) {
		return s.beginDeviceCredentialAuthorization(ctx, channelID, network)
	}
	return CredentialStageResult{}, app_errors.ErrValidation
}

func (s *Service) beginBrowserCredentialAuthorization(
	ctx context.Context,
	channelID channel.ID,
	network subscriptionruntime.NetworkContext,
) (CredentialStageResult, error) {
	if s.beginSubscriptionAuthorization == nil {
		return CredentialStageResult{}, app_errors.ErrAuthorizationUnavailable
	}
	authorization, err := s.beginSubscriptionAuthorization(channelID)
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrAuthorizationUnavailable
	}
	payload, err := json.Marshal(stagedSubscriptionPayload{
		State: authorization.State, DriverState: authorization.DriverState, Network: &network,
	})
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
		StageID: row.ID, Status: string(row.Status), AuthorizationMethod: row.AuthorizationMethod,
		AuthorizationURL: authorization.URL,
		RedirectURI:      authorization.RedirectURI,
		Account:          CredentialStageAccount{}, ExpiresAtMS: row.ExpiresAtMS,
	}, nil
}

func (s *Service) beginDeviceCredentialAuthorization(
	ctx context.Context,
	channelID channel.ID,
	network subscriptionruntime.NetworkContext,
) (CredentialStageResult, error) {
	if s.beginDeviceAuthorization == nil {
		return CredentialStageResult{}, app_errors.ErrAuthorizationUnavailable
	}
	ctx = subscriptionruntime.WithNetworkContext(ctx, network)
	beginContext, cancelBegin := context.WithTimeout(ctx, defaultSubscriptionControlTimeout)
	defer cancelBegin()
	authorization, err := s.beginDeviceAuthorization(beginContext, channelID)
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrAuthorizationUnavailable
	}
	now := s.now().UTC()
	verificationURL := strings.TrimSpace(authorization.VerificationURL)
	userCode := strings.TrimSpace(authorization.UserCode)
	parsedURL, parseErr := url.Parse(verificationURL)
	pollInterval, intervalOK := normalizeDevicePollInterval(authorization.PollInterval)
	if parseErr != nil || !parsedURL.IsAbs() || parsedURL.Opaque != "" || parsedURL.Host == "" ||
		!strings.EqualFold(parsedURL.Scheme, "https") || parsedURL.User != nil || parsedURL.Fragment != "" ||
		len(verificationURL) > maxDeviceAuthorizationURL || !validDeviceAuthorizationCode(userCode) ||
		len(authorization.DriverState) == 0 || len(authorization.DriverState) > maxOAuthFileBytes ||
		!intervalOK || !authorization.ExpiresAt.After(now) ||
		authorization.ExpiresAt.After(now.Add(credentialStageDeviceMaxTTL)) {
		return CredentialStageResult{}, app_errors.ErrAuthorizationUnavailable
	}
	nextPollAtMS := now.Add(pollInterval).UnixMilli()
	summaryJSON, err := json.Marshal(credentialStageSafeSummary{Authorization: &credentialStageAuthorizationSummary{
		AuthorizationURL: verificationURL,
		UserCode:         userCode,
		NextPollAtMS:     nextPollAtMS,
		PollIntervalMS:   pollInterval.Milliseconds(),
	}})
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrInternalServer
	}
	payload, err := json.Marshal(stagedSubscriptionPayload{DriverState: authorization.DriverState, Network: &network})
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
	row := models.CredentialStage{
		ID: stageID, ChannelID: string(channelID), ConnectionType: models.ConnectionTypeSubscription,
		AuthorizationMethod: string(channel.AuthorizationDeviceOAuth), Status: models.CredentialStagePendingAuthorization,
		EncryptedPayload: ciphertext, PayloadSchemaVersion: stagedSubscriptionSchemaV2,
		SafeSummaryJSON: models.JSON(summaryJSON), ExpiresAtMS: authorization.ExpiresAt.UTC().UnixMilli(),
		CreatedAtMS: now.UnixMilli(), UpdatedAtMS: now.UnixMilli(),
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return CredentialStageResult{}, app_errors.ParseDBError(err)
	}
	return CredentialStageResult{
		StageID: row.ID, Status: string(row.Status), AuthorizationMethod: row.AuthorizationMethod,
		AuthorizationURL: verificationURL, UserCode: userCode, NextPollAtMS: nextPollAtMS,
		Account: CredentialStageAccount{}, ExpiresAtMS: row.ExpiresAtMS,
	}, nil
}

func normalizeDevicePollInterval(value time.Duration) (time.Duration, bool) {
	if value < time.Second || value > time.Minute {
		return 0, false
	}
	return value, true
}

func validDeviceAuthorizationCode(value string) bool {
	if value == "" || len(value) > maxDeviceAuthorizationCode {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < 0x20 || value[index] > 0x7e {
			return false
		}
	}
	return true
}

// CompleteCredentialAuthorization atomically claims one pending state before
// issuing the one-shot OAuth code exchange.
func (s *Service) CompleteCredentialAuthorization(
	ctx context.Context,
	returnedState string,
	code string,
) (CredentialStageResult, error) {
	return s.completeCredentialAuthorization(ctx, "", "", returnedState, code)
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
	return s.completeCredentialAuthorization(ctx, stageID, "", returnedState, code)
}

func (s *Service) completeCredentialAuthorizationFromCallback(
	ctx context.Context,
	callback subscriptionruntime.LocalCallbackSpec,
	returnedState string,
	code string,
) (CredentialStageResult, error) {
	return s.completeCredentialAuthorization(ctx, "", callback.RedirectURI, returnedState, code)
}

func (s *Service) completeCredentialAuthorization(
	ctx context.Context,
	stageID string,
	expectedRedirectURI string,
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
		if !s.credentialStageMatchesCallback(row, expectedRedirectURI) {
			return app_errors.ErrAuthorizationStateInvalid
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
	network, err := s.stagedNetworkContext(ctx, payload)
	if err != nil {
		_ = s.markCredentialStageOutcomeUnknown(ctx, row.ID)
		return CredentialStageResult{}, app_errors.ErrAuthorizationExchangeFailed
	}
	exchangeContext, cancelExchange := context.WithTimeout(context.WithoutCancel(ctx), defaultSubscriptionControlTimeout)
	defer cancelExchange()
	exchangeContext = subscriptionruntime.WithNetworkContext(exchangeContext, network)
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
	result, err := s.finishCredentialStageExchange(finalizeContext, row, credential, network)
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
	return s.failCredentialAuthorization(ctx, "", "", returnedState, providerError)
}

func (s *Service) failCredentialAuthorizationForStage(ctx context.Context, stageID string, returnedState string, providerError string) error {
	if strings.TrimSpace(stageID) == "" {
		return app_errors.ErrAuthorizationStateInvalid
	}
	return s.failCredentialAuthorization(ctx, stageID, "", returnedState, providerError)
}

func (s *Service) failCredentialAuthorizationFromCallback(
	ctx context.Context,
	callback subscriptionruntime.LocalCallbackSpec,
	returnedState string,
	providerError string,
) error {
	return s.failCredentialAuthorization(ctx, "", callback.RedirectURI, returnedState, providerError)
}

func (s *Service) failCredentialAuthorization(
	ctx context.Context,
	stageID string,
	expectedRedirectURI string,
	returnedState string,
	providerError string,
) error {
	if s == nil || strings.TrimSpace(returnedState) == "" || strings.TrimSpace(providerError) == "" {
		return app_errors.ErrAuthorizationStateInvalid
	}
	errorCode := "authorization_failed"
	if strings.EqualFold(strings.TrimSpace(providerError), "access_denied") {
		errorCode = "authorization_denied"
	}
	nowMS := s.now().UnixMilli()
	stateHash := s.encryption.Hash("oauth-state/v1|" + returnedState)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row models.CredentialStage
		query := tx.Where(
			"oauth_state_hash = ? AND status = ? AND expires_at_ms > ?",
			stateHash,
			models.CredentialStagePendingAuthorization,
			nowMS,
		)
		if stageID != "" {
			query = query.Where("id = ?", stageID)
		}
		if err := query.Take(&row).Error; err != nil {
			return err
		}
		if !s.credentialStageMatchesCallback(row, expectedRedirectURI) {
			return app_errors.ErrAuthorizationStateInvalid
		}
		result := tx.Model(&models.CredentialStage{}).
			Where("id = ? AND oauth_state_hash = ? AND status = ?", row.ID, stateHash, models.CredentialStagePendingAuthorization).
			Updates(map[string]any{
				"status": models.CredentialStageFailed, "encrypted_payload": "",
				"oauth_state_hash": nil, "error_code": errorCode, "updated_at_ms": nowMS,
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
		return app_errors.ErrAuthorizationStateInvalid
	}
	return nil
}

func (s *Service) credentialStageMatchesCallback(row models.CredentialStage, expectedRedirectURI string) bool {
	if expectedRedirectURI == "" {
		return true
	}
	if s == nil || s.subscriptions == nil {
		return false
	}
	callback, ok := s.subscriptions.LocalCallback(channel.ID(row.ChannelID))
	return ok && callback.RedirectURI == expectedRedirectURI
}

func (s *Service) CompleteCredentialAuthorizationCallback(
	ctx context.Context,
	stageID string,
	rawCallbackURL string,
) (CredentialStageResult, error) {
	if strings.TrimSpace(stageID) == "" {
		return CredentialStageResult{}, app_errors.ErrAuthorizationStateInvalid
	}
	row, err := s.loadCredentialStage(ctx, stageID)
	if err != nil {
		return CredentialStageResult{}, err
	}
	callbackSpec, ok := s.subscriptions.LocalCallback(channel.ID(row.ChannelID))
	if !ok {
		return CredentialStageResult{}, app_errors.ErrAuthorizationStateInvalid
	}
	callback, err := parseManualOAuthCallbackURL(rawCallbackURL, callbackSpec)
	if err != nil {
		return CredentialStageResult{}, app_errors.ErrValidation
	}
	if callback.ProviderError != "" {
		if err := s.failCredentialAuthorization(
			ctx,
			stageID,
			callbackSpec.RedirectURI,
			callback.State,
			callback.ProviderError,
		); err != nil {
			return CredentialStageResult{}, err
		}
		return s.GetCredentialStage(ctx, stageID)
	}
	return s.completeCredentialAuthorization(
		ctx,
		stageID,
		callbackSpec.RedirectURI,
		callback.State,
		callback.Code,
	)
}

// PollCredentialDeviceAuthorization performs at most one provider token poll.
// Calling it before the advertised interval returns the current Stage without
// dispatching upstream.
func (s *Service) PollCredentialDeviceAuthorization(
	ctx context.Context,
	stageID string,
) (CredentialStageResult, error) {
	if s == nil || s.db == nil || s.encryption == nil || s.pollDeviceAuthorization == nil ||
		strings.TrimSpace(stageID) == "" {
		return CredentialStageResult{}, app_errors.ErrAuthorizationStateInvalid
	}
	row, err := s.loadCredentialStage(ctx, stageID)
	if err != nil {
		return CredentialStageResult{}, err
	}
	if row.AuthorizationMethod != string(channel.AuthorizationDeviceOAuth) {
		return CredentialStageResult{}, app_errors.ErrAuthorizationStateInvalid
	}
	if row.Status == models.CredentialStageExchanging {
		return s.GetCredentialStage(ctx, stageID)
	}
	if row.Status != models.CredentialStagePendingAuthorization {
		return CredentialStageResult{}, app_errors.ErrAuthorizationStateInvalid
	}
	now := s.now().UTC()
	if now.UnixMilli() >= row.ExpiresAtMS {
		if err := s.expireCredentialStage(ctx, &row); err != nil {
			return CredentialStageResult{}, app_errors.ParseDBError(err)
		}
		return s.GetCredentialStage(ctx, stageID)
	}
	_, authorization := decodeCredentialStageSafeSummary(row.SafeSummaryJSON)
	if authorization == nil {
		return CredentialStageResult{}, app_errors.ErrAuthorizationStateInvalid
	}
	if now.UnixMilli() < authorization.NextPollAtMS {
		return s.GetCredentialStage(ctx, stageID)
	}
	claim := s.db.WithContext(ctx).Model(&models.CredentialStage{}).
		Where(
			"id = ? AND authorization_method = ? AND status = ? AND expires_at_ms > ?",
			row.ID, string(channel.AuthorizationDeviceOAuth), models.CredentialStagePendingAuthorization, now.UnixMilli(),
		).
		Updates(map[string]any{
			"status":        models.CredentialStageExchanging,
			"expires_at_ms": now.Add(defaultSubscriptionControlTimeout).UnixMilli(),
			"updated_at_ms": now.UnixMilli(),
		})
	if claim.Error != nil {
		return CredentialStageResult{}, app_errors.ParseDBError(claim.Error)
	}
	if claim.RowsAffected != 1 {
		return s.GetCredentialStage(ctx, stageID)
	}
	plaintext, err := s.encryption.Decrypt(row.EncryptedPayload)
	if err != nil {
		_ = s.markCredentialStageOutcomeUnknown(ctx, row.ID)
		return CredentialStageResult{}, app_errors.ErrAuthorizationExchangeFailed
	}
	payload, err := decodeStagedAuthorizationPayload(row.PayloadSchemaVersion, []byte(plaintext))
	if err != nil || len(payload.DriverState) == 0 {
		_ = s.markCredentialStageOutcomeUnknown(ctx, row.ID)
		return CredentialStageResult{}, app_errors.ErrAuthorizationExchangeFailed
	}
	network, err := s.stagedNetworkContext(ctx, payload)
	if err != nil {
		_ = s.markCredentialStageOutcomeUnknown(ctx, row.ID)
		return CredentialStageResult{}, app_errors.ErrAuthorizationExchangeFailed
	}
	pollContext, cancelPoll := context.WithTimeout(context.WithoutCancel(ctx), defaultSubscriptionControlTimeout)
	defer cancelPoll()
	pollContext = subscriptionruntime.WithNetworkContext(pollContext, network)
	poll, err := s.pollDeviceAuthorization(pollContext, channel.ID(row.ChannelID), payload.DriverState)
	if err != nil {
		if markErr := s.markCredentialStageOutcomeUnknown(ctx, row.ID); markErr != nil {
			return CredentialStageResult{}, app_errors.ErrInternalServer
		}
		return CredentialStageResult{}, app_errors.ErrAuthorizationExchangeFailed
	}
	switch poll.Status {
	case subscriptionruntime.DeviceAuthorizationPending:
		return s.finishPendingDeviceAuthorization(ctx, row, payload.DriverState, authorization, poll, network)
	case subscriptionruntime.DeviceAuthorizationAuthorized:
		finalizeContext, cancelFinalize := credentialStageFinalizeContext(ctx)
		defer cancelFinalize()
		return s.finishCredentialStageExchange(finalizeContext, row, poll.Credential, network)
	case subscriptionruntime.DeviceAuthorizationDenied:
		if err := s.finishDeviceAuthorizationFailure(ctx, row.ID, models.CredentialStageFailed, "authorization_denied"); err != nil {
			return CredentialStageResult{}, err
		}
		return s.GetCredentialStage(ctx, row.ID)
	case subscriptionruntime.DeviceAuthorizationExpired:
		if err := s.finishDeviceAuthorizationFailure(ctx, row.ID, models.CredentialStageExpired, "authorization_expired"); err != nil {
			return CredentialStageResult{}, err
		}
		return s.GetCredentialStage(ctx, row.ID)
	default:
		if err := s.markCredentialStageOutcomeUnknown(ctx, row.ID); err != nil {
			return CredentialStageResult{}, app_errors.ErrInternalServer
		}
		return CredentialStageResult{}, app_errors.ErrAuthorizationExchangeFailed
	}
}

func (s *Service) finishPendingDeviceAuthorization(
	ctx context.Context,
	row models.CredentialStage,
	previousDriverState []byte,
	authorization *credentialStageAuthorizationSummary,
	poll subscriptionruntime.DeviceAuthorizationPoll,
	network subscriptionruntime.NetworkContext,
) (CredentialStageResult, error) {
	driverState := poll.DriverState
	if len(driverState) == 0 {
		driverState = previousDriverState
	}
	if len(driverState) == 0 || len(driverState) > maxOAuthFileBytes {
		_ = s.markCredentialStageOutcomeUnknown(ctx, row.ID)
		return CredentialStageResult{}, app_errors.ErrAuthorizationExchangeFailed
	}
	pollInterval := poll.PollInterval
	if pollInterval == 0 {
		pollInterval = time.Duration(authorization.PollIntervalMS) * time.Millisecond
	}
	var intervalOK bool
	pollInterval, intervalOK = normalizeDevicePollInterval(pollInterval)
	if !intervalOK {
		_ = s.markCredentialStageOutcomeUnknown(ctx, row.ID)
		return CredentialStageResult{}, app_errors.ErrAuthorizationExchangeFailed
	}
	payload, err := json.Marshal(stagedSubscriptionPayload{DriverState: driverState, Network: &network})
	if err != nil {
		_ = s.markCredentialStageOutcomeUnknown(ctx, row.ID)
		return CredentialStageResult{}, app_errors.ErrInternalServer
	}
	ciphertext, err := s.encryption.Encrypt(string(payload))
	clear(payload)
	if err != nil {
		_ = s.markCredentialStageOutcomeUnknown(ctx, row.ID)
		return CredentialStageResult{}, app_errors.ErrInternalServer
	}
	now := s.now().UTC()
	authorization.NextPollAtMS = now.Add(pollInterval).UnixMilli()
	authorization.PollIntervalMS = pollInterval.Milliseconds()
	summaryJSON, err := json.Marshal(credentialStageSafeSummary{Authorization: authorization})
	if err != nil {
		_ = s.markCredentialStageOutcomeUnknown(ctx, row.ID)
		return CredentialStageResult{}, app_errors.ErrInternalServer
	}
	finalizeContext, cancelFinalize := credentialStageFinalizeContext(ctx)
	defer cancelFinalize()
	result := s.db.WithContext(finalizeContext).Model(&models.CredentialStage{}).
		Where("id = ? AND status = ?", row.ID, models.CredentialStageExchanging).
		Updates(map[string]any{
			"status": models.CredentialStagePendingAuthorization, "encrypted_payload": ciphertext,
			"safe_summary_json": models.JSON(summaryJSON), "expires_at_ms": row.ExpiresAtMS,
			"updated_at_ms": now.UnixMilli(),
		})
	if result.Error != nil {
		return CredentialStageResult{}, app_errors.ParseDBError(result.Error)
	}
	if result.RowsAffected != 1 {
		return CredentialStageResult{}, app_errors.ErrCredentialAuthOutcomeUnknown
	}
	return s.GetCredentialStage(ctx, row.ID)
}

func (s *Service) finishDeviceAuthorizationFailure(
	ctx context.Context,
	stageID string,
	status models.CredentialStageStatus,
	errorCode string,
) error {
	finalizeContext, cancelFinalize := credentialStageFinalizeContext(ctx)
	defer cancelFinalize()
	result := s.db.WithContext(finalizeContext).Model(&models.CredentialStage{}).
		Where("id = ? AND status = ?", stageID, models.CredentialStageExchanging).
		Updates(map[string]any{
			"status": status, "encrypted_payload": "", "error_code": errorCode,
			"updated_at_ms": s.now().UnixMilli(),
		})
	if result.Error != nil {
		return app_errors.ParseDBError(result.Error)
	}
	if result.RowsAffected != 1 {
		return app_errors.ErrCredentialAuthOutcomeUnknown
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
	account, authorization := decodeCredentialStageSafeSummary(row.SafeSummaryJSON)
	result := CredentialStageResult{
		StageID: row.ID, Status: string(row.Status), AuthorizationMethod: row.AuthorizationMethod,
		Account: account, ExpiresAtMS: row.ExpiresAtMS,
		ErrorCode: stageResultErrorCode(row.Status, row.ErrorCode),
	}
	if authorization != nil {
		result.AuthorizationURL = authorization.AuthorizationURL
		result.UserCode = authorization.UserCode
		result.NextPollAtMS = authorization.NextPollAtMS
	}
	if row.Status == models.CredentialStagePendingAuthorization || row.Status == models.CredentialStageExchanging {
		if callback, ok := s.subscriptions.LocalCallback(channel.ID(row.ChannelID)); ok {
			result.RedirectURI = callback.RedirectURI
		}
	}
	return result, nil
}

func decodeCredentialStageSafeSummary(raw []byte) (CredentialStageAccount, *credentialStageAuthorizationSummary) {
	if len(raw) == 0 {
		return CredentialStageAccount{}, nil
	}
	var safe credentialStageSafeSummary
	if json.Unmarshal(raw, &safe) == nil && safe.Authorization != nil {
		return CredentialStageAccount{}, safe.Authorization
	}
	var account CredentialStageAccount
	_ = json.Unmarshal(raw, &account)
	return account, nil
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
	networks ...subscriptionruntime.NetworkContext,
) (CredentialStageResult, error) {
	var network *subscriptionruntime.NetworkContext
	if len(networks) > 0 {
		value := networks[0]
		network = &value
	}
	payload, err := json.Marshal(stagedSubscriptionPayload{Credential: credential.Canonical(), Network: network})
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
		StageID: row.ID, Status: string(row.Status), AuthorizationMethod: row.AuthorizationMethod,
		Account: summary, ExpiresAtMS: row.ExpiresAtMS,
	}, nil
}

func (s *Service) finishCredentialStageExchange(
	ctx context.Context,
	row models.CredentialStage,
	credential subscriptionruntime.Credential,
	network subscriptionruntime.NetworkContext,
) (CredentialStageResult, error) {
	payload, err := json.Marshal(stagedSubscriptionPayload{Credential: credential.Canonical(), Network: &network})
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
		StageID: row.ID, Status: string(models.CredentialStageReady), AuthorizationMethod: row.AuthorizationMethod,
		Account: summary, ExpiresAtMS: expiresAtMS,
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
