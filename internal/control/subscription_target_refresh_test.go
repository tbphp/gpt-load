package control

import (
	"context"
	"net/http"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/storage/models"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestOldTargetUnauthorizedObservationDoesNotForceCredentialRefresh(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	var before models.Credential
	if err := fixture.db.First(&before, credentialID).Error; err != nil {
		t.Fatal(err)
	}
	originalPrepare := fixture.service.prepareSubscriptionCredential
	forcedRefreshes := 0
	fixture.service.prepareSubscriptionCredential = func(
		ctx context.Context, channelID channel.ID, snapshot execution.CredentialSnapshot, force bool,
	) (subscriptionruntime.Credential, *execution.ErrorEvidence) {
		if force {
			forcedRefreshes++
		}
		return originalPrepare(ctx, channelID, snapshot, false)
	}
	observationCalls := 0
	fixture.service.observeSubscriptionAccount = func(
		context.Context, channel.ID, subscriptionruntime.Credential, subscriptionruntime.Target,
	) (subscriptionruntime.Observation, error) {
		observationCalls++
		if observationCalls == 1 {
			// 旧目标响应返回前已切换 URL，迟到的 401 不再具有刷新当前凭据的依据。
			setSubscriptionTestTarget(t, fixture, groupID, "https://relay.example/current")
			return subscriptionruntime.Observation{}, &subscriptionruntime.UpstreamHTTPError{StatusCode: http.StatusUnauthorized}
		}
		return subscriptionruntime.Observation{Payload: []byte(`{"quota_windows":[]}`)}, nil
	}

	result, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if err != nil || result.State != string(models.CredentialObservationUnavailable) ||
		forcedRefreshes != 0 || observationCalls != 1 {
		t.Fatalf("old target 401 result=%#v error=%v forced refreshes=%d observation calls=%d",
			result, err, forcedRefreshes, observationCalls)
	}
	var after models.Credential
	if err := fixture.db.First(&after, credentialID).Error; err != nil {
		t.Fatal(err)
	}
	if after.AuthState != before.AuthState || after.Data != before.Data || after.SecretVersion != before.SecretVersion {
		t.Fatal("old target 401 changed the current credential")
	}
}
