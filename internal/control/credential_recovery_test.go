package control

import (
	"context"
	"errors"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

// 手动刷新凭据是异常账号唯一的原地恢复入口，不再要求先重新连接或导入。
func TestRefreshGroupCredentialRecoversFailedAuthStates(t *testing.T) {
	t.Parallel()

	for _, authState := range []models.CredentialAuthState{
		models.CredentialAuthStateOutcomeUnknown,
		models.CredentialAuthStateReauthorizationRequired,
		models.CredentialAuthStateRefreshing,
	} {
		t.Run(string(authState), func(t *testing.T) {
			t.Parallel()

			fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
			markStoredCredentialAuthState(t, fixture, credentialID, authState, "refresh_outcome_unknown")
			recoveryCalls := 0
			fixture.service.recoverSubscriptionCredential = func(
				context.Context,
				channel.ID,
				execution.CredentialSnapshot,
			) (subscriptionruntime.Credential, *execution.ErrorEvidence) {
				recoveryCalls++
				return subscriptionruntime.Credential{}, nil
			}

			if _, err := fixture.service.RefreshGroupCredential(t.Context(), groupID, credentialID); err != nil {
				t.Fatalf("RefreshGroupCredential() error = %v", err)
			}
			if recoveryCalls != 1 {
				t.Fatalf("recovery calls = %d, want 1", recoveryCalls)
			}
		})
	}
}

// 恢复权限只属于手动刷新凭据，其他控制面操作在异常状态下继续被拒。
func TestSubscriptionControlOperationsStillRejectFailedAuthStates(t *testing.T) {
	t.Parallel()

	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	markStoredCredentialAuthState(
		t, fixture, credentialID, models.CredentialAuthStateOutcomeUnknown, "refresh_outcome_unknown",
	)
	prepareCalls := 0
	fixture.service.prepareSubscriptionCredential = func(
		context.Context,
		channel.ID,
		execution.CredentialSnapshot,
		bool,
	) (subscriptionruntime.Credential, *execution.ErrorEvidence) {
		prepareCalls++
		return subscriptionruntime.Credential{}, nil
	}
	fixture.service.recoverSubscriptionCredential = func(
		context.Context,
		channel.ID,
		execution.CredentialSnapshot,
	) (subscriptionruntime.Credential, *execution.ErrorEvidence) {
		t.Fatal("manual recovery must not run for automatic control-plane operations")
		return subscriptionruntime.Credential{}, nil
	}

	if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID); !errors.Is(
		err, app_errors.ErrCredentialAuthOutcomeUnknown,
	) {
		t.Fatalf("RefreshCredentialObservation() error = %v, want outcome unknown", err)
	}
	if prepareCalls != 0 {
		t.Fatalf("prepare calls = %d, want 0", prepareCalls)
	}
}

func markStoredCredentialAuthState(
	t *testing.T,
	fixture serviceFixture,
	credentialID uint,
	authState models.CredentialAuthState,
	code string,
) {
	t.Helper()
	if err := fixture.db.Model(&models.Credential{}).Where("id = ?", credentialID).
		Updates(map[string]any{"auth_state": authState, "auth_error_code": code}).Error; err != nil {
		t.Fatal(err)
	}
	if !fixture.registry.SetCredentialAuthState(credentialID, state.CredentialAuthState(authState)) {
		t.Fatalf("publish auth state %q", authState)
	}
}
