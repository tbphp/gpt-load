// Package subscription owns durable subscription credential lifecycle state.
// Provider wire conversion remains in the execution adapter.
package subscription

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

const (
	refreshLeadTime        = 5 * time.Minute
	refreshFinalizeTimeout = 5 * time.Second
)

// CredentialManager serializes subscription refreshes and keeps the database
// and runtime registry on the same durable credential version.
type CredentialManager struct {
	db             *gorm.DB
	encryption     encryption.Service
	registry       *state.CredentialRegistry
	mutations      *health.MutationCoordinator
	runtime        *subscriptionruntime.Runtime
	refresh        func(context.Context, subscriptionruntime.Driver, subscriptionruntime.Credential) (subscriptionruntime.Credential, error)
	replaceSecret  func(uint, uint64, uint64, string, string) bool
	reconcileGroup func(uint, []state.CredentialEntry) (bool, error)
	now            func() time.Time
}

// Runtime returns the immutable capability registry used by this manager.
func (manager *CredentialManager) Runtime() *subscriptionruntime.Runtime {
	if manager == nil {
		return nil
	}
	return manager.runtime
}

// NewCredentialManager creates the shared control-plane and data-plane
// lifecycle for all compiled subscription channels.
func NewCredentialManager(
	db *gorm.DB,
	encryptionService encryption.Service,
	registry *state.CredentialRegistry,
	mutations *health.MutationCoordinator,
	runtime *subscriptionruntime.Runtime,
) *CredentialManager {
	if mutations == nil {
		mutations = health.NewMutationCoordinator()
	}
	return &CredentialManager{
		db: db, encryption: encryptionService, registry: registry, mutations: mutations,
		runtime: runtime,
		refresh: func(ctx context.Context, driver subscriptionruntime.Driver, credential subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
			return driver.Refresh(ctx, credential)
		},
		now:            time.Now,
		replaceSecret:  registry.ReplaceCredentialSecretIfMatch,
		reconcileGroup: registry.ReconcileGroup,
	}
}

// Prepare returns the currently usable credential, durably refreshing it when
// required. No provider request is sent after an uncertain refresh outcome.
func (manager *CredentialManager) Prepare(
	ctx context.Context,
	channelID channel.ID,
	snapshot execution.CredentialSnapshot,
	forceRefresh bool,
) (subscriptionruntime.Credential, *execution.ErrorEvidence) {
	driver, ok := manager.runtime.Driver(channelID)
	if !ok {
		return subscriptionruntime.Credential{}, localEvidence("credential_driver_unavailable", "subscription credential driver is unavailable")
	}
	credential, err := driver.Parse(snapshot.Data())
	if err != nil {
		return subscriptionruntime.Credential{}, localEvidence("credential_invalid", "subscription credential is invalid")
	}
	if !forceRefresh {
		if expiration, ok := credential.ExpiresAt(); !ok || expiration.After(manager.now().Add(refreshLeadTime)) {
			return credential, nil
		}
	}
	var prepared subscriptionruntime.Credential
	var prepareErr *execution.ErrorEvidence
	manager.mutations.Do(snapshot.ID, func() {
		prepared, prepareErr = manager.refreshCredentialLocked(ctx, channelID, driver, snapshot.ID, snapshot.Version, forceRefresh)
	})
	return prepared, prepareErr
}

func (manager *CredentialManager) refreshCredentialLocked(
	ctx context.Context,
	channelID channel.ID,
	driver subscriptionruntime.Driver,
	credentialID uint,
	expectedVersion uint64,
	forceRefresh bool,
) (subscriptionruntime.Credential, *execution.ErrorEvidence) {
	var row models.Credential
	if err := manager.db.WithContext(ctx).First(&row, credentialID).Error; err != nil {
		return subscriptionruntime.Credential{}, localEvidence("credential_unavailable", "subscription credential is unavailable")
	}
	var group models.Group
	if err := manager.db.WithContext(ctx).Select("id", "channel_id", "connection_type").First(&group, row.GroupID).Error; err != nil ||
		group.ChannelID != string(channelID) || group.ConnectionType != models.ConnectionTypeSubscription {
		return subscriptionruntime.Credential{}, localEvidence("credential_target_mismatch", "subscription credential target does not match")
	}
	if row.AuthState != models.CredentialAuthStateReady {
		return subscriptionruntime.Credential{}, authEvidence(string(row.AuthState))
	}
	plaintext, err := manager.encryption.Decrypt(row.Data)
	if err != nil {
		return subscriptionruntime.Credential{}, localEvidence("credential_decrypt_failed", "subscription credential is unavailable")
	}
	current, err := driver.Parse([]byte(plaintext))
	plaintext = ""
	if err != nil {
		return subscriptionruntime.Credential{}, localEvidence("credential_invalid", "subscription credential is invalid")
	}
	if forceRefresh && row.SecretVersion > expectedVersion {
		return current, nil
	}
	if !forceRefresh {
		if expiration, ok := current.ExpiresAt(); !ok || expiration.After(manager.now().Add(refreshLeadTime)) {
			return current, nil
		}
	}
	if err := manager.transitionAuthState(ctx, row, row.SecretVersion, models.CredentialAuthStateRefreshing, ""); err != nil {
		finalizeContext, cancel := refreshFinalizeContext(ctx)
		restoreErr := manager.setAuthState(finalizeContext, row.ID, row.SecretVersion, models.CredentialAuthStateReady, "")
		if restoreErr == nil {
			restoreErr = manager.publishAuthState(finalizeContext, row, models.CredentialAuthStateReady)
		}
		cancel()
		if restoreErr != nil {
			manager.registry.SetCredentialAuthState(row.ID, state.CredentialAuthStateOutcomeUnknown)
			return subscriptionruntime.Credential{}, authEvidence("refresh_registry_mismatch")
		}
		return subscriptionruntime.Credential{}, localEvidence("refresh_start_failed", "subscription credential refresh could not start")
	}
	refreshed, refreshErr := manager.refresh(ctx, driver, current)
	if refreshErr != nil {
		stateValue, code := models.CredentialAuthStateOutcomeUnknown, "refresh_outcome_unknown"
		switch driver.ClassifyRefreshFailure(refreshErr) {
		case subscriptionruntime.RefreshFailureIdentityChanged:
			stateValue, code = models.CredentialAuthStateReauthorizationRequired, "refresh_identity_changed"
		case subscriptionruntime.RefreshFailureReauthorizationRequired:
			stateValue, code = models.CredentialAuthStateReauthorizationRequired, "refresh_rejected"
		}
		if err := manager.transitionAuthState(ctx, row, row.SecretVersion, stateValue, code); err != nil {
			manager.registry.SetCredentialAuthState(row.ID, state.CredentialAuthStateOutcomeUnknown)
			return subscriptionruntime.Credential{}, localEvidence("refresh_state_commit_failed", "subscription credential state could not be saved")
		}
		return subscriptionruntime.Credential{}, authEvidence(code)
	}
	if refreshed.Identity() == "" || refreshed.Identity() != current.Identity() {
		if err := manager.transitionAuthState(ctx, row, row.SecretVersion, models.CredentialAuthStateReauthorizationRequired, "refresh_identity_changed"); err != nil {
			manager.registry.SetCredentialAuthState(row.ID, state.CredentialAuthStateOutcomeUnknown)
			return subscriptionruntime.Credential{}, localEvidence("refresh_state_commit_failed", "subscription credential state could not be saved")
		}
		return subscriptionruntime.Credential{}, authEvidence("refresh_identity_changed")
	}
	canonical := refreshed.Canonical()
	if len(canonical) == 0 {
		if markErr := manager.markRefreshOutcomeUnknown(ctx, row, row.SecretVersion, "refresh_persist_failed"); markErr != nil {
			return subscriptionruntime.Credential{}, localEvidence("refresh_state_commit_failed", "subscription credential state could not be saved")
		}
		return subscriptionruntime.Credential{}, authEvidence("refresh_persist_failed")
	}
	ciphertext, err := manager.encryption.Encrypt(string(canonical))
	fingerprint := manager.encryption.Hash(string(canonical))
	clear(canonical)
	if err != nil {
		if markErr := manager.markRefreshOutcomeUnknown(ctx, row, row.SecretVersion, "refresh_persist_failed"); markErr != nil {
			return subscriptionruntime.Credential{}, localEvidence("refresh_state_commit_failed", "subscription credential state could not be saved")
		}
		return subscriptionruntime.Credential{}, authEvidence("refresh_persist_failed")
	}
	nextVersion := row.SecretVersion + 1
	finalizeContext, cancelFinalize := refreshFinalizeContext(ctx)
	updated := manager.db.WithContext(finalizeContext).Model(&models.Credential{}).
		Where("id = ? AND secret_version = ? AND auth_state = ?", row.ID, row.SecretVersion, models.CredentialAuthStateRefreshing).
		Updates(map[string]any{
			"data": ciphertext, "fingerprint": fingerprint, "secret_version": nextVersion,
			"auth_state": models.CredentialAuthStateReady, "auth_error_code": "", "updated_at_ms": manager.now().UnixMilli(),
		})
	cancelFinalize()
	if updated.Error != nil || updated.RowsAffected != 1 {
		if markErr := manager.markRefreshOutcomeUnknown(ctx, row, row.SecretVersion, "refresh_commit_failed"); markErr != nil {
			return subscriptionruntime.Credential{}, localEvidence("refresh_state_commit_failed", "subscription credential state could not be saved")
		}
		return subscriptionruntime.Credential{}, authEvidence("refresh_commit_failed")
	}
	if manager.replaceSecret == nil || !manager.replaceSecret(row.ID, row.SecretVersion, nextVersion, fingerprint, ciphertext) {
		// The rotated token is durable. Reconcile this Group from DB truth so a
		// failed incremental publication cannot leave control and data planes at
		// different secret versions.
		reconcileContext, cancelReconcile := refreshFinalizeContext(ctx)
		entries, reconcileErr := stateloader.BuildGroupCredentialEntries(reconcileContext, manager.db, row.GroupID)
		if reconcileErr != nil {
			cancelReconcile()
		} else if manager.reconcileGroup == nil {
			reconcileErr = errors.New("credential registry reconciliation is unavailable")
			cancelReconcile()
		} else {
			_, reconcileErr = manager.reconcileGroup(row.GroupID, entries)
			cancelReconcile()
		}
		if reconcileErr != nil {
			if markErr := manager.markRefreshOutcomeUnknown(ctx, row, nextVersion, "refresh_registry_mismatch"); markErr != nil {
				manager.registry.SetCredentialAuthState(row.ID, state.CredentialAuthStateOutcomeUnknown)
				return subscriptionruntime.Credential{}, localEvidence("refresh_state_commit_failed", "subscription credential state could not be saved")
			}
			return subscriptionruntime.Credential{}, authEvidence("refresh_registry_mismatch")
		}
	}
	return refreshed, nil
}

func (manager *CredentialManager) markRefreshOutcomeUnknown(
	ctx context.Context,
	row models.Credential,
	version uint64,
	code string,
) error {
	return manager.transitionAuthState(ctx, row, version, models.CredentialAuthStateOutcomeUnknown, code)
}

func (manager *CredentialManager) setAuthState(
	ctx context.Context,
	credentialID uint,
	version uint64,
	authState models.CredentialAuthState,
	code string,
) error {
	result := manager.db.WithContext(ctx).Model(&models.Credential{}).
		Where("id = ? AND secret_version = ?", credentialID, version).
		Updates(map[string]any{"auth_state": authState, "auth_error_code": code, "updated_at_ms": manager.now().UnixMilli()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("credential auth state update affected %d rows", result.RowsAffected)
	}
	return nil
}

func (manager *CredentialManager) transitionAuthState(
	ctx context.Context,
	row models.Credential,
	version uint64,
	authState models.CredentialAuthState,
	code string,
) error {
	finalizeContext, cancel := refreshFinalizeContext(ctx)
	defer cancel()
	if err := manager.setAuthState(finalizeContext, row.ID, version, authState, code); err != nil {
		return err
	}
	return manager.publishAuthState(finalizeContext, row, authState)
}

func (manager *CredentialManager) publishAuthState(
	ctx context.Context,
	row models.Credential,
	authState models.CredentialAuthState,
) error {
	if manager.registry.SetCredentialAuthState(row.ID, state.CredentialAuthState(authState)) {
		return nil
	}
	entries, err := stateloader.BuildGroupCredentialEntries(ctx, manager.db, row.GroupID)
	if err != nil {
		return err
	}
	if manager.reconcileGroup == nil {
		return fmt.Errorf("credential registry reconciliation is unavailable")
	}
	_, err = manager.reconcileGroup(row.GroupID, entries)
	return err
}

func refreshFinalizeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), refreshFinalizeTimeout)
}

func authEvidence(code string) *execution.ErrorEvidence {
	return &execution.ErrorEvidence{
		Kind: execution.ErrorKindProvider, Hint: execution.FailureHintReauthorizationRequired,
		Code: code, Summary: "subscription account requires reauthorization",
	}
}

func localEvidence(code, summary string) *execution.ErrorEvidence {
	return &execution.ErrorEvidence{Kind: execution.ErrorKindInternal, Code: code, Summary: summary}
}
