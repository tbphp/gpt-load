package control

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription/providers/muse"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

type museAuthorizationTransport func(*http.Request) (*http.Response, error)

func (transport museAuthorizationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestMuseDeviceAuthorizationResumesAfterMintFailure(t *testing.T) {
	fixture := newServiceFixture(t)
	now := time.Now().UTC()
	fixture.service.now = func() time.Time { return now }
	var tokenCalls, mintCalls int
	previous := http.DefaultTransport
	http.DefaultTransport = museAuthorizationTransport(func(request *http.Request) (*http.Response, error) {
		status, payload := http.StatusOK, ""
		header := make(http.Header)
		switch request.URL.Path {
		case "/oidc/device/authorization/":
			payload = `{"device_code":"device-secret","user_code":"ABCD","verification_uri":"https://auth.meta.com/device","expires_in":600,"interval":5}`
		case "/oidc/device/token/":
			tokenCalls++
			payload = `{"access_token":"dca:authorized-secret","expires_in":600}`
		case "/muse-code/key":
			mintCalls++
			if request.Header.Get("Authorization") != "Bearer dca:authorized-secret" {
				t.Error("mint lost the checkpointed DCA token")
			}
			if mintCalls == 1 {
				status = http.StatusServiceUnavailable
				header.Set("Retry-After", "20")
				payload = `{"error":"temporarily_unavailable"}`
			} else {
				payload = `{"api_key":"minted-secret","user_email":"account","is_subs_active":true}`
			}
		default:
			t.Fatalf("unexpected authorization endpoint %s", request.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(payload)), Request: request}, nil
	})
	defer func() { http.DefaultTransport = previous }()
	driver := muse.Implementations().Drivers[0].(subscriptionruntime.DeviceAuthorizationDriver)
	// 真实 Muse 驱动通过内存 transport 访问固定端点；本例只验证授权状态机。
	fixture.service.beginDeviceAuthorization = func(context.Context, channel.ID) (subscriptionruntime.DeviceAuthorization, error) {
		return driver.BeginDeviceAuthorization(t.Context())
	}
	fixture.service.pollDeviceAuthorization = func(_ context.Context, _ channel.ID, raw []byte) (subscriptionruntime.DeviceAuthorizationPoll, error) {
		return driver.PollDeviceAuthorization(t.Context(), raw)
	}
	started, err := fixture.service.BeginCredentialAuthorization(t.Context(), channel.Muse)
	if err != nil {
		t.Fatal(err)
	}
	now = time.UnixMilli(started.NextPollAtMS)
	checkpoint, err := fixture.service.PollCredentialDeviceAuthorization(t.Context(), started.StageID)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Status != string(models.CredentialStagePendingAuthorization) || tokenCalls != 1 || mintCalls != 0 {
		t.Fatal("DCA was not checkpointed before minting")
	}
	now = time.UnixMilli(checkpoint.NextPollAtMS)
	pending, err := fixture.service.PollCredentialDeviceAuthorization(t.Context(), started.StageID)
	if err != nil {
		t.Fatalf("temporary mint failure ended authorization: %v", err)
	}
	if pending.Status != string(models.CredentialStagePendingAuthorization) || pending.NextPollAtMS != now.Add(20*time.Second).UnixMilli() || pending.ExpiresAtMS != started.ExpiresAtMS {
		t.Fatalf("retry stage = %+v", pending)
	}
	var stored models.CredentialStage
	if err := fixture.db.Take(&stored, "id = ?", started.StageID).Error; err != nil {
		t.Fatal(err)
	}
	plaintext, err := fixture.encryption.Decrypt(stored.EncryptedPayload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plaintext, "dca:authorized-secret") || strings.Contains(string(stored.SafeSummaryJSON), "dca:authorized-secret") {
		t.Fatal("checkpointed DCA was lost or exposed")
	}
	now = now.Add(19 * time.Second)
	if _, err := fixture.service.PollCredentialDeviceAuthorization(t.Context(), started.StageID); err != nil {
		t.Fatal(err)
	}
	if mintCalls != 1 {
		t.Fatal("mint retried before Retry-After")
	}
	now = time.UnixMilli(pending.NextPollAtMS)
	ready, err := fixture.service.PollCredentialDeviceAuthorization(t.Context(), started.StageID)
	if err != nil {
		t.Fatal(err)
	}
	if ready.Status != string(models.CredentialStageReady) || tokenCalls != 1 || mintCalls != 2 {
		t.Fatalf("status=%s token_calls=%d mint_calls=%d", ready.Status, tokenCalls, mintCalls)
	}
}
