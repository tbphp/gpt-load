// Package subscription owns durable subscription credential lifecycle state.
// Provider wire conversion remains in the execution adapter.
package subscription

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/codex"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

const (
	refreshLeadTime        = 5 * time.Minute
	refreshFinalizeTimeout = 5 * time.Second
)

// CodexCredentialManager serializes refreshes and keeps the database and runtime
// registry on the same durable credential version.
type CodexCredentialManager struct {
	db             *gorm.DB
	encryption     encryption.Service
	registry       *state.CredentialRegistry
	mutations      *health.MutationCoordinator
	refresh        func(context.Context, codex.Credential) (codex.Credential, error)
	replaceSecret  func(uint, uint64, uint64, string, string) bool
	reconcileGroup func(uint, []state.CredentialEntry) (bool, error)
	now            func() time.Time
}

// NewCodexCredentialManager creates the shared control-plane and data-plane
// lifecycle for durable Codex subscription credentials.
func NewCodexCredentialManager(
	db *gorm.DB,
	encryptionService encryption.Service,
	registry *state.CredentialRegistry,
	mutations *health.MutationCoordinator,
) *CodexCredentialManager {
	if mutations == nil {
		mutations = health.NewMutationCoordinator()
	}
	return &CodexCredentialManager{
		db: db, encryption: encryptionService, registry: registry, mutations: mutations,
		refresh: codex.RefreshCredentialOnce, now: time.Now,
		replaceSecret:  registry.ReplaceCredentialSecretIfMatch,
		reconcileGroup: registry.ReconcileGroup,
	}
}

// PrepareCodexCredential is the control-plane entry point. It shares the same
// refresh and publication lifecycle as data-plane execution.
func (manager *CodexCredentialManager) PrepareCodexCredential(
	ctx context.Context,
	snapshot execution.CredentialSnapshot,
) (codex.Credential, *execution.ErrorEvidence) {
	return manager.Prepare(ctx, snapshot, false)
}

// Prepare returns the currently usable credential, durably refreshing it when
// required. No provider request is sent after an uncertain refresh outcome.
func (manager *CodexCredentialManager) Prepare(
	ctx context.Context,
	snapshot execution.CredentialSnapshot,
	forceRefresh bool,
) (codex.Credential, *execution.ErrorEvidence) {
	credential, err := codex.ParseCredentialJSON(snapshot.Data())
	if err != nil {
		return codex.Credential{}, localEvidence("credential_invalid", "subscription credential is invalid")
	}
	if !forceRefresh {
		if expiration, ok := codex.CredentialExpiresAt(credential); !ok || expiration.After(manager.now().Add(refreshLeadTime)) {
			return credential, nil
		}
	}
	var prepared codex.Credential
	var prepareErr *execution.ErrorEvidence
	manager.mutations.Do(snapshot.ID, func() {
		prepared, prepareErr = manager.refreshCredentialLocked(ctx, snapshot.ID, snapshot.Version, forceRefresh)
	})
	return prepared, prepareErr
}

func (manager *CodexCredentialManager) refreshCredentialLocked(
	ctx context.Context,
	credentialID uint,
	expectedVersion uint64,
	forceRefresh bool,
) (codex.Credential, *execution.ErrorEvidence) {
	var row models.Credential
	if err := manager.db.WithContext(ctx).First(&row, credentialID).Error; err != nil {
		return codex.Credential{}, localEvidence("credential_unavailable", "subscription credential is unavailable")
	}
	if row.AuthState != models.CredentialAuthStateReady {
		return codex.Credential{}, authEvidence(string(row.AuthState))
	}
	plaintext, err := manager.encryption.Decrypt(row.Data)
	if err != nil {
		return codex.Credential{}, localEvidence("credential_decrypt_failed", "subscription credential is unavailable")
	}
	current, err := codex.ParseCredentialJSON([]byte(plaintext))
	plaintext = ""
	if err != nil {
		return codex.Credential{}, localEvidence("credential_invalid", "subscription credential is invalid")
	}
	if forceRefresh && row.SecretVersion > expectedVersion {
		return current, nil
	}
	if !forceRefresh {
		if expiration, ok := codex.CredentialExpiresAt(current); !ok || expiration.After(manager.now().Add(refreshLeadTime)) {
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
			return codex.Credential{}, authEvidence("refresh_registry_mismatch")
		}
		return codex.Credential{}, localEvidence("refresh_start_failed", "subscription credential refresh could not start")
	}
	refreshed, refreshErr := manager.refresh(ctx, current)
	if refreshErr != nil {
		stateValue, code := models.CredentialAuthStateOutcomeUnknown, "refresh_outcome_unknown"
		var tokenErr *codex.TokenEndpointError
		if errors.Is(refreshErr, codex.ErrCredentialIdentityChanged) {
			stateValue, code = models.CredentialAuthStateReauthorizationRequired, "refresh_identity_changed"
		} else if errors.As(refreshErr, &tokenErr) && codex.IsDefinitiveRefreshRejection(tokenErr.Code) {
			stateValue, code = models.CredentialAuthStateReauthorizationRequired, "refresh_rejected"
		}
		if err := manager.transitionAuthState(ctx, row, row.SecretVersion, stateValue, code); err != nil {
			manager.registry.SetCredentialAuthState(row.ID, state.CredentialAuthStateOutcomeUnknown)
			return codex.Credential{}, localEvidence("refresh_state_commit_failed", "subscription credential state could not be saved")
		}
		return codex.Credential{}, authEvidence(code)
	}
	if refreshed.AccountID != current.AccountID {
		if err := manager.transitionAuthState(ctx, row, row.SecretVersion, models.CredentialAuthStateReauthorizationRequired, "refresh_identity_changed"); err != nil {
			manager.registry.SetCredentialAuthState(row.ID, state.CredentialAuthStateOutcomeUnknown)
			return codex.Credential{}, localEvidence("refresh_state_commit_failed", "subscription credential state could not be saved")
		}
		return codex.Credential{}, authEvidence("refresh_identity_changed")
	}
	canonical, err := codex.MarshalCredential(refreshed)
	if err != nil {
		if markErr := manager.markRefreshOutcomeUnknown(ctx, row, row.SecretVersion, "refresh_persist_failed"); markErr != nil {
			return codex.Credential{}, localEvidence("refresh_state_commit_failed", "subscription credential state could not be saved")
		}
		return codex.Credential{}, authEvidence("refresh_persist_failed")
	}
	ciphertext, err := manager.encryption.Encrypt(string(canonical))
	fingerprint := manager.encryption.Hash(string(canonical))
	clear(canonical)
	if err != nil {
		if markErr := manager.markRefreshOutcomeUnknown(ctx, row, row.SecretVersion, "refresh_persist_failed"); markErr != nil {
			return codex.Credential{}, localEvidence("refresh_state_commit_failed", "subscription credential state could not be saved")
		}
		return codex.Credential{}, authEvidence("refresh_persist_failed")
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
			return codex.Credential{}, localEvidence("refresh_state_commit_failed", "subscription credential state could not be saved")
		}
		return codex.Credential{}, authEvidence("refresh_commit_failed")
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
				return codex.Credential{}, localEvidence("refresh_state_commit_failed", "subscription credential state could not be saved")
			}
			return codex.Credential{}, authEvidence("refresh_registry_mismatch")
		}
	}
	return refreshed, nil
}

func (manager *CodexCredentialManager) markRefreshOutcomeUnknown(
	ctx context.Context,
	row models.Credential,
	version uint64,
	code string,
) error {
	return manager.transitionAuthState(ctx, row, version, models.CredentialAuthStateOutcomeUnknown, code)
}

func (manager *CodexCredentialManager) setAuthState(
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

func (manager *CodexCredentialManager) transitionAuthState(
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

func (manager *CodexCredentialManager) publishAuthState(
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
