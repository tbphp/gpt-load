package subscription

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
	subscriptionproviders "gpt-load/internal/subscription/providers"
	"gpt-load/internal/subscription/providers/codex"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
	"gpt-load/internal/testutil/encryptiontest"
)

type failingEncryptService struct {
	encryption.Service
}

func (failingEncryptService) Encrypt(string) (string, error) {
	return "", errors.New("encrypt failed")
}

func TestCredentialManagerRefreshesExpiringCredentialAndPublishesVersion(t *testing.T) {
	manager, db, registry, keyService, row := newCredentialManagerFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	refreshCalls := 0
	manager.refresh = adaptCodexRefresh(func(_ context.Context, current codex.Credential) (codex.Credential, error) {
		refreshCalls++
		current.AccessToken = "new-access"
		current.RefreshToken = "new-refresh"
		current.Expire = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		return current, nil
	})

	credential, evidence := manager.Prepare(t.Context(), channel.Codex, credentialSnapshot(t, row, keyService), false)
	if evidence != nil || refreshCalls != 1 || mustCodexCredential(t, credential).AccessToken != "new-access" {
		t.Fatalf("credential=%#v evidence=%#v refresh=%d", credential, evidence, refreshCalls)
	}
	var stored models.Credential
	if err := db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.SecretVersion != 2 || stored.IdentityFingerprint != row.IdentityFingerprint || stored.AuthState != models.CredentialAuthStateReady {
		t.Fatalf("stored credential = %#v", stored)
	}
	entry, ok := registry.CredentialRef(row.ID)
	if !ok || entry.Version != 2 || entry.IdentityGeneration != stateloader.CredentialIdentityGeneration(row.IdentityFingerprint, "codex", "subscription", json.RawMessage(`{}`)) {
		t.Fatalf("registry entry = %#v", entry)
	}
}

func TestCredentialManagerForceRefreshesAfterExplicitRejection(t *testing.T) {
	manager, _, _, keyService, row := newCredentialManagerFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Hour)))
	refreshCalls := 0
	manager.refresh = adaptCodexRefresh(func(_ context.Context, current codex.Credential) (codex.Credential, error) {
		refreshCalls++
		current.AccessToken = "new-access"
		current.RefreshToken = "new-refresh"
		current.Expire = time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
		return current, nil
	})

	credential, evidence := manager.Prepare(t.Context(), channel.Codex, credentialSnapshot(t, row, keyService), true)
	if evidence != nil || refreshCalls != 1 || mustCodexCredential(t, credential).AccessToken != "new-access" {
		t.Fatalf("credential=%#v evidence=%#v refresh=%d", credential, evidence, refreshCalls)
	}
}

func TestCredentialManagerForceRefreshUsesNewerDurableSecret(t *testing.T) {
	manager, _, _, keyService, row := newCredentialManagerFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Hour)))
	refreshCalls := 0
	manager.refresh = adaptCodexRefresh(func(_ context.Context, current codex.Credential) (codex.Credential, error) {
		refreshCalls++
		current.AccessToken = "new-access"
		current.RefreshToken = "new-refresh"
		current.Expire = time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
		return current, nil
	})
	snapshot := credentialSnapshot(t, row, keyService)

	first, firstEvidence := manager.Prepare(t.Context(), channel.Codex, snapshot, true)
	second, secondEvidence := manager.Prepare(t.Context(), channel.Codex, snapshot, true)
	if firstEvidence != nil || secondEvidence != nil || refreshCalls != 1 ||
		mustCodexCredential(t, first).AccessToken != "new-access" || mustCodexCredential(t, second).AccessToken != "new-access" {
		t.Fatalf("first=%#v second=%#v evidence=%#v/%#v refresh=%d", first, second, firstEvidence, secondEvidence, refreshCalls)
	}
}

func TestCredentialManagerSerializesConcurrentRefresh(t *testing.T) {
	manager, _, _, keyService, row := newCredentialManagerFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	var mu sync.Mutex
	refreshCalls := 0
	manager.refresh = adaptCodexRefresh(func(_ context.Context, current codex.Credential) (codex.Credential, error) {
		mu.Lock()
		refreshCalls++
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		current.AccessToken = "new-access"
		current.Expire = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		return current, nil
	})
	snapshot := credentialSnapshot(t, row, keyService)
	var wait sync.WaitGroup
	wait.Add(2)
	for range 2 {
		go func() {
			defer wait.Done()
			_, _ = manager.Prepare(context.Background(), channel.Codex, snapshot, false)
		}()
	}
	wait.Wait()
	mu.Lock()
	defer mu.Unlock()
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls)
	}
}

func TestCredentialManagerSuppressesConcurrentRetryableRefresh(t *testing.T) {
	testCredentialManagerSuppressesConcurrentRetryableRefresh(t, false)
}

func TestCredentialManagerSuppressesConcurrentForcedRetryableRefresh(t *testing.T) {
	testCredentialManagerSuppressesConcurrentRetryableRefresh(t, true)
}

func testCredentialManagerSuppressesConcurrentRetryableRefresh(t *testing.T, forceRefresh bool) {
	t.Helper()
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	manager, _, registry, keyService, row := newCredentialManagerFixture(
		t,
		credentialJSON("old-access", "old-refresh", now.Add(time.Minute)),
	)
	manager.now = func() time.Time { return now }
	entered := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	refreshCalls := 0
	manager.refresh = adaptCodexRefresh(func(context.Context, codex.Credential) (codex.Credential, error) {
		mu.Lock()
		refreshCalls++
		call := refreshCalls
		mu.Unlock()
		if call == 1 {
			close(entered)
			<-release
		}
		return codex.Credential{}, &codex.TokenEndpointError{
			StatusCode: http.StatusTooManyRequests,
			Code:       "rate_limit_exceeded",
			RetryAfter: 30 * time.Minute,
		}
	})
	snapshot := credentialSnapshot(t, row, keyService)
	type prepareResult struct {
		evidence *execution.ErrorEvidence
	}
	results := make(chan prepareResult, 2)
	prepare := func() {
		_, evidence := manager.Prepare(context.Background(), channel.Codex, snapshot, forceRefresh)
		results <- prepareResult{evidence: evidence}
	}
	go prepare()
	<-entered
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		prepare()
	}()
	<-secondStarted
	close(release)

	for range 2 {
		result := <-results
		if result.evidence == nil || result.evidence.Code != "refresh_temporarily_unavailable" ||
			result.evidence.RetryAfter != 30*time.Minute {
			t.Fatalf("evidence = %#v", result.evidence)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls)
	}
	views := registry.Snapshot()
	if len(views) != 1 || views[0].AuthState != state.CredentialAuthStateReady ||
		!views[0].CooldownUntil.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("runtime views = %#v", views)
	}
}

func TestCredentialManagerControlRefreshBypassesDataPlaneCooldown(t *testing.T) {
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	manager, _, _, keyService, row := newCredentialManagerFixture(
		t,
		credentialJSON("old-access", "old-refresh", now.Add(time.Minute)),
	)
	manager.now = func() time.Time { return now }
	refreshCalls := 0
	manager.refresh = adaptCodexRefresh(func(_ context.Context, current codex.Credential) (codex.Credential, error) {
		refreshCalls++
		if refreshCalls == 1 {
			return codex.Credential{}, &codex.TokenEndpointError{
				StatusCode: http.StatusTooManyRequests,
				Code:       "rate_limit_exceeded",
				RetryAfter: 30 * time.Minute,
			}
		}
		current.AccessToken = "new-access"
		current.Expire = now.Add(time.Hour).Format(time.RFC3339)
		return current, nil
	})
	snapshot := credentialSnapshot(t, row, keyService)

	if _, evidence := manager.Prepare(t.Context(), channel.Codex, snapshot, false); evidence == nil ||
		evidence.Code != "refresh_temporarily_unavailable" {
		t.Fatalf("data-plane evidence = %#v", evidence)
	}
	credential, evidence := manager.PrepareForControl(t.Context(), channel.Codex, snapshot, true)
	if evidence != nil || refreshCalls != 2 || mustCodexCredential(t, credential).AccessToken != "new-access" {
		t.Fatalf("credential/evidence/refresh calls = %#v / %#v / %d", credential, evidence, refreshCalls)
	}
}

func TestCredentialManagerReconcilesRegistryAfterIncrementalPublicationMiss(t *testing.T) {
	manager, db, registry, keyService, row := newCredentialManagerFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	encodedProxy, err := outboundproxy.Encode(outboundproxy.Config{
		Mode: outboundproxy.ModeCustom,
		URL:  "socks5://proxy-user:proxy-password@127.0.0.1:1080",
	})
	if err != nil {
		t.Fatal(err)
	}
	encryptedProxy, err := keyService.Encrypt(encodedProxy)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Credential{}).Where("id = ?", row.ID).
		Update("proxy_config", encryptedProxy).Error; err != nil {
		t.Fatal(err)
	}
	manager.refresh = adaptCodexRefresh(refreshedCredential)
	manager.replaceSecret = func(uint, uint64, uint64, string, string) bool { return false }

	credential, evidence := manager.Prepare(t.Context(), channel.Codex, credentialSnapshot(t, row, keyService), false)
	if evidence != nil || mustCodexCredential(t, credential).AccessToken != "new-access" {
		t.Fatalf("credential=%#v evidence=%#v", credential, evidence)
	}
	var stored models.Credential
	if err := db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	plaintext, err := keyService.Decrypt(stored.Data)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := codex.ParseCredentialJSON([]byte(plaintext))
	if err != nil || durable.AccessToken != "new-access" {
		t.Fatalf("durable credential = %#v, %v", durable, err)
	}
	ref, ok := registry.CredentialRef(row.ID)
	if !ok || ref.Version != row.SecretVersion+1 || ref.Fingerprint != stored.Fingerprint ||
		ref.EncryptedValue != stored.Data || ref.EncryptedProxy != encryptedProxy ||
		ref.ProxyFingerprint != keyService.Hash(encodedProxy) {
		t.Fatalf("registry ref = %#v, ok = %t", ref, ok)
	}
}

func TestCredentialManagerFailsClosedWhenRefreshedSecretCannotReachRegistry(t *testing.T) {
	manager, db, registry, keyService, row := newCredentialManagerFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	manager.refresh = adaptCodexRefresh(refreshedCredential)
	manager.replaceSecret = func(uint, uint64, uint64, string, string) bool { return false }
	manager.reconcileGroup = func(uint, []state.CredentialEntry) (bool, error) {
		return false, errors.New("registry unavailable")
	}

	_, evidence := manager.Prepare(t.Context(), channel.Codex, credentialSnapshot(t, row, keyService), false)
	if evidence == nil || evidence.Code != "refresh_registry_mismatch" {
		t.Fatalf("evidence = %#v", evidence)
	}
	var stored models.Credential
	if err := db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.SecretVersion != row.SecretVersion+1 || stored.AuthState != models.CredentialAuthStateOutcomeUnknown {
		t.Fatalf("stored credential = %#v", stored)
	}
	views := registry.Snapshot()
	if len(views) != 1 || views[0].ID != row.ID || views[0].AuthState != state.CredentialAuthStateOutcomeUnknown {
		t.Fatalf("registry views = %#v", views)
	}
}

func TestCredentialManagerRestoresReadyWhenRefreshStartCannotPublish(t *testing.T) {
	manager, db, registry, keyService, row := newCredentialManagerFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	registry.RemoveCredential(row.ID)
	manager.reconcileGroup = func(uint, []state.CredentialEntry) (bool, error) {
		return false, errors.New("registry unavailable")
	}
	refreshCalls := 0
	manager.refresh = adaptCodexRefresh(func(context.Context, codex.Credential) (codex.Credential, error) {
		refreshCalls++
		return codex.Credential{}, errors.New("must not be called")
	})

	_, evidence := manager.Prepare(t.Context(), channel.Codex, credentialSnapshot(t, row, keyService), false)
	if evidence == nil || evidence.Code != "refresh_registry_mismatch" || refreshCalls != 0 {
		t.Fatalf("evidence = %#v, refresh calls = %d", evidence, refreshCalls)
	}
	var stored models.Credential
	if err := db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.SecretVersion != row.SecretVersion || stored.AuthState != models.CredentialAuthStateReady {
		t.Fatalf("stored credential = %#v", stored)
	}
}

func TestCredentialManagerDefinitiveRejectionRequiresReauthorization(t *testing.T) {
	manager, db, _, keyService, row := newCredentialManagerFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	manager.refresh = adaptCodexRefresh(func(context.Context, codex.Credential) (codex.Credential, error) {
		return codex.Credential{}, &codex.TokenEndpointError{StatusCode: http.StatusBadRequest, Code: "invalid_grant"}
	})
	_, evidence := manager.Prepare(t.Context(), channel.Codex, credentialSnapshot(t, row, keyService), false)
	if evidence == nil || evidence.Hint != execution.FailureHintReauthorizationRequired ||
		evidence.Summary != "subscription account requires reauthorization" {
		t.Fatalf("evidence = %#v", evidence)
	}
	assertStoredAuthState(t, db, row.ID, models.CredentialAuthStateReauthorizationRequired, "refresh_rejected")
}

func TestCredentialManagerIdentityChangeRequiresReauthorization(t *testing.T) {
	manager, db, _, keyService, row := newCredentialManagerFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	manager.refresh = adaptCodexRefresh(func(context.Context, codex.Credential) (codex.Credential, error) {
		return codex.Credential{}, codex.ErrCredentialIdentityChanged
	})
	_, evidence := manager.Prepare(t.Context(), channel.Codex, credentialSnapshot(t, row, keyService), false)
	if evidence == nil || evidence.Code != "refresh_identity_changed" {
		t.Fatalf("evidence = %#v", evidence)
	}
	assertStoredAuthState(t, db, row.ID, models.CredentialAuthStateReauthorizationRequired, "refresh_identity_changed")
}

func TestCredentialManagerRetryableTokenEndpointFailureRestoresReady(t *testing.T) {
	now := time.Date(2026, time.August, 30, 8, 0, 0, 0, time.UTC)
	manager, db, registry, keyService, row := newCredentialManagerFixture(t, credentialJSON("old-access", "old-refresh", now.Add(time.Minute)))
	manager.now = func() time.Time { return now }
	manager.refresh = adaptCodexRefresh(func(context.Context, codex.Credential) (codex.Credential, error) {
		return codex.Credential{}, &codex.TokenEndpointError{
			StatusCode: http.StatusTooManyRequests, Code: "rate_limit_exceeded",
			RetryAfter: 30 * time.Minute,
		}
	})
	logger := logrus.StandardLogger()
	previousOutput, previousFormatter, previousLevel := logger.Out, logger.Formatter, logger.Level
	var logs bytes.Buffer
	logrus.SetOutput(&logs)
	logrus.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	logrus.SetLevel(logrus.WarnLevel)
	t.Cleanup(func() {
		logrus.SetOutput(previousOutput)
		logrus.SetFormatter(previousFormatter)
		logrus.SetLevel(previousLevel)
	})

	_, evidence := manager.Prepare(t.Context(), channel.Codex, credentialSnapshot(t, row, keyService), false)
	if evidence == nil || evidence.Kind != execution.ErrorKindHTTP ||
		evidence.Code != "refresh_temporarily_unavailable" ||
		evidence.StatusCode != http.StatusTooManyRequests || evidence.RetryAfter != 30*time.Minute {
		t.Fatalf("evidence = %#v", evidence)
	}
	assertStoredAuthState(t, db, row.ID, models.CredentialAuthStateReady, "")
	views := registry.Snapshot()
	if len(views) != 1 || views[0].AuthState != state.CredentialAuthStateReady ||
		!views[0].CooldownUntil.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("runtime views = %#v", views)
	}
	logged := logs.String()
	for _, expected := range []string{
		`"event":"subscription.credential_refresh_failed"`,
		`"classification":"retryable"`,
		`"stage":"provider_refresh"`,
		`"http_status":429`,
		`"oauth_error_code":"rate_limit_exceeded"`,
	} {
		if !strings.Contains(logged, expected) {
			t.Fatalf("refresh log missing %s: %s", expected, logged)
		}
	}
	for _, secret := range []string{"old-access", "old-refresh"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("refresh log exposed credential secret: %s", logged)
		}
	}
}

func TestCredentialManagerRetryableTokenEndpointFailureUsesDefaultCooldown(t *testing.T) {
	now := time.Date(2026, time.August, 30, 8, 0, 0, 0, time.UTC)
	manager, _, registry, keyService, row := newCredentialManagerFixture(
		t,
		credentialJSON("old-access", "old-refresh", now.Add(time.Minute)),
	)
	manager.now = func() time.Time { return now }
	manager.refresh = adaptCodexRefresh(func(context.Context, codex.Credential) (codex.Credential, error) {
		return codex.Credential{}, &codex.TokenEndpointError{StatusCode: http.StatusServiceUnavailable}
	})

	_, evidence := manager.Prepare(t.Context(), channel.Codex, credentialSnapshot(t, row, keyService), false)
	if evidence == nil || evidence.Code != "refresh_temporarily_unavailable" || evidence.RetryAfter != 0 {
		t.Fatalf("evidence = %#v", evidence)
	}
	views := registry.Snapshot()
	if len(views) != 1 || !views[0].CooldownUntil.Equal(
		now.Add(subscriptionruntime.DefaultRefreshFailureCooldown),
	) {
		t.Fatalf("runtime views = %#v", views)
	}
}

func TestRefreshTemporarilyUnavailableEvidenceBoundsRetryAfter(t *testing.T) {
	tests := []struct {
		name string
		give time.Duration
		want time.Duration
	}{
		{name: "valid", give: 30 * time.Minute, want: 30 * time.Minute},
		{name: "negative", give: -time.Second},
		{name: "exact limit", give: time.Hour, want: time.Hour},
		{name: "over limit", give: time.Hour + time.Second, want: time.Hour},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := refreshTemporarilyUnavailableEvidence(
				subscriptionruntime.RefreshFailureDecision{RetryAfter: test.give},
			)
			if evidence.RetryAfter != test.want {
				t.Fatalf("RetryAfter = %s, want %s", evidence.RetryAfter, test.want)
			}
		})
	}
}

func TestCredentialManagerPersistentTokenEndpointFailureFailsClosed(t *testing.T) {
	manager, db, _, keyService, row := newCredentialManagerFixture(
		t,
		credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)),
	)
	manager.refresh = adaptCodexRefresh(func(context.Context, codex.Credential) (codex.Credential, error) {
		return codex.Credential{}, &codex.TokenEndpointError{
			StatusCode: http.StatusBadRequest, Code: "invalid_client",
		}
	})

	_, evidence := manager.Prepare(
		t.Context(),
		channel.Codex,
		credentialSnapshot(t, row, keyService),
		false,
	)
	if evidence == nil || evidence.Code != "refresh_outcome_unknown" {
		t.Fatalf("evidence = %#v", evidence)
	}
	assertStoredAuthState(
		t,
		db,
		row.ID,
		models.CredentialAuthStateOutcomeUnknown,
		"refresh_outcome_unknown",
	)
}

func TestCredentialManagerAmbiguousFailureStopsWithoutReplay(t *testing.T) {
	manager, db, _, keyService, row := newCredentialManagerFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	manager.refresh = adaptCodexRefresh(func(context.Context, codex.Credential) (codex.Credential, error) {
		return codex.Credential{}, errors.New("connection reset")
	})
	_, evidence := manager.Prepare(t.Context(), channel.Codex, credentialSnapshot(t, row, keyService), false)
	if evidence == nil || evidence.Hint != execution.FailureHintReauthorizationRequired ||
		evidence.Summary != "subscription credential refresh outcome is unknown" {
		t.Fatalf("evidence = %#v", evidence)
	}
	assertStoredAuthState(t, db, row.ID, models.CredentialAuthStateOutcomeUnknown, "refresh_outcome_unknown")
}

func TestCredentialManagerMarksOutcomeUnknownWhenRotatedTokenCannotBeEncrypted(t *testing.T) {
	manager, db, registry, keyService, row := newCredentialManagerFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	manager.encryption = failingEncryptService{Service: keyService}
	manager.refresh = adaptCodexRefresh(refreshedCredential)

	_, evidence := manager.Prepare(t.Context(), channel.Codex, credentialSnapshot(t, row, keyService), false)
	if evidence == nil || evidence.Code != "refresh_persist_failed" {
		t.Fatalf("evidence = %#v", evidence)
	}
	var stored models.Credential
	if err := db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.SecretVersion != row.SecretVersion || stored.AuthState != models.CredentialAuthStateOutcomeUnknown {
		t.Fatalf("stored credential = %#v", stored)
	}
	views := registry.Snapshot()
	if len(views) != 1 || views[0].AuthState != state.CredentialAuthStateOutcomeUnknown {
		t.Fatalf("registry views = %#v", views)
	}
}

func newCredentialManagerFixture(
	t *testing.T,
	canonical []byte,
) (*CredentialManager, *gorm.DB, *state.CredentialRegistry, encryption.Service, models.Credential) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Group{}, &models.Credential{}); err != nil {
		t.Fatal(err)
	}
	keyService := encryptiontest.Service(t, "subscription-manager-test-encryption-key-material")
	ciphertext, err := keyService.Encrypt(string(canonical))
	if err != nil {
		t.Fatal(err)
	}
	group := models.Group{
		Name: "subscription", ChannelID: "codex", ConnectionType: models.ConnectionTypeSubscription,
		Params: models.JSON(`{}`), Models: models.JSON(`[]`), Overrides: models.JSON(`{}`), Enabled: true,
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	credential, err := codex.ParseCredentialJSON(canonical)
	if err != nil {
		t.Fatal(err)
	}
	row := models.Credential{
		GroupID: group.ID, Data: ciphertext, Fingerprint: keyService.Hash(string(canonical)),
		IdentityFingerprint: keyService.Hash("identity|" + credential.AccountID), SecretVersion: 1,
		AuthState: models.CredentialAuthStateReady, Status: models.CredentialStatusActive,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	registry := state.NewCredentialRegistry()
	identityGeneration := stateloader.CredentialIdentityGeneration(
		row.IdentityFingerprint, group.ChannelID, string(group.ConnectionType), json.RawMessage(group.Params),
	)
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: row.ID, GroupID: group.ID, Version: 1, IdentityGeneration: identityGeneration,
		Fingerprint: row.Fingerprint, Status: state.CredentialStatusActive,
		WeightAuto: state.DefaultWeight, EncryptedValue: row.Data,
	}}); err != nil {
		t.Fatal(err)
	}
	subscriptions, err := subscriptionruntime.NewRuntime(channel.NewRegistry(), subscriptionproviders.Implementations()...)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewCredentialManager(db, keyService, registry, health.NewMutationCoordinator(), subscriptions)
	return manager, db, registry, keyService, row
}

func credentialSnapshot(t *testing.T, row models.Credential, keyService encryption.Service) execution.CredentialSnapshot {
	t.Helper()
	plaintext, err := keyService.Decrypt(row.Data)
	if err != nil {
		t.Fatal(err)
	}
	return execution.NewCredentialSnapshot(row.ID, row.SecretVersion, 1, []byte(plaintext))
}

func credentialJSON(access, refresh string, expires time.Time) []byte {
	value, _ := codex.MarshalCredential(codex.Credential{
		Type: codex.Provider, AccessToken: access, RefreshToken: refresh,
		AccountID: "account-1", Email: "a@example.com", Expire: expires.UTC().Format(time.RFC3339),
	})
	return value
}

func refreshedCredential(_ context.Context, current codex.Credential) (codex.Credential, error) {
	current.AccessToken = "new-access"
	current.RefreshToken = "new-refresh"
	current.Expire = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	return current, nil
}

func adaptCodexRefresh(
	refresh func(context.Context, codex.Credential) (codex.Credential, error),
) func(context.Context, subscriptionruntime.Driver, subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
	return func(ctx context.Context, driver subscriptionruntime.Driver, current subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
		parsed, err := codex.ParseCredentialJSON(current.Canonical())
		if err != nil {
			return subscriptionruntime.Credential{}, err
		}
		refreshed, err := refresh(ctx, parsed)
		if err != nil {
			return subscriptionruntime.Credential{}, err
		}
		canonical, err := codex.MarshalCredential(refreshed)
		if err != nil {
			return subscriptionruntime.Credential{}, err
		}
		return driver.Parse(canonical)
	}
}

func mustCodexCredential(t *testing.T, credential subscriptionruntime.Credential) codex.Credential {
	t.Helper()
	parsed, err := codex.ParseCredentialJSON(credential.Canonical())
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func assertStoredAuthState(
	t *testing.T,
	db *gorm.DB,
	credentialID uint,
	wantState models.CredentialAuthState,
	wantCode string,
) {
	t.Helper()
	var stored models.Credential
	if err := db.First(&stored, credentialID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.AuthState != wantState || stored.AuthErrorCode != wantCode {
		t.Fatalf("stored credential = %#v", stored)
	}
}
