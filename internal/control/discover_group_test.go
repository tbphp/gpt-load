package control

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription/providers/claude"
	"gpt-load/internal/subscription/providers/codex"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestDiscoveryUsesSingleReadSnapshot(t *testing.T) {
	t.Parallel()
	fixture, dsn := newFileServiceFixture(t)
	group := seedPersistedDiscoveryGroup(t, fixture, true, models.JSON(`{}`))
	group.Name = "discovery-snapshot-old"
	group.Params = models.JSON(`{"base_url":"https://discovery-old.example/v1"}`)
	if err := fixture.db.Save(&group).Error; err != nil {
		t.Fatalf("seed old Group version: %v", err)
	}
	seedPersistedDiscoveryCredential(
		t,
		fixture,
		group.ID,
		1,
		"discovery-key-old",
		models.CredentialStatusActive,
	)
	if err := fixture.db.Create(&models.SystemSetting{
		Key: "header_rules", Value: `{"set":{"X-Version":"old"}}`,
	}).Error; err != nil {
		t.Fatalf("seed old system settings: %v", err)
	}

	writer, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("storage.Open(writer) error = %v", err)
	}
	writerSQL, err := writer.DB()
	if err != nil {
		t.Fatalf("writer.DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := writerSQL.Close(); err != nil {
			t.Errorf("close writer database: %v", err)
		}
	})
	newCiphertext, err := fixture.encryption.Encrypt(`{"api_key":"discovery-key-new"}`)
	if err != nil {
		t.Fatalf("encrypt new key version: %v", err)
	}

	firstSelect := make(chan struct{})
	releaseReader := make(chan struct{})
	var barrierOnce sync.Once
	const callbackName = "test:discovery_single_read_snapshot"
	if err := fixture.db.Callback().Query().After("gorm:query").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement.Table != "groups" {
				return
			}
			barrierOnce.Do(func() {
				close(firstSelect)
				<-releaseReader
			})
		},
	); err != nil {
		t.Fatalf("register discovery query barrier: %v", err)
	}

	type observedTarget struct {
		baseURL string
		apiKey  string
		version string
	}
	observed := make(chan observedTarget, 1)
	fixture.service.executor = newRecordingDiscoveryExecutor(&recordingDiscoveryExecutorTarget{
		value: protocol.OpenAICompletions,
		listFn: func(
			_ context.Context,
			baseURL, apiKey string,
			rules state.HeaderRules,
		) ([]string, error) {
			observed <- observedTarget{
				baseURL: baseURL,
				apiKey:  apiKey,
				version: rules.Set["X-Version"],
			}
			return []string{"model"}, nil
		},
	})
	discoveryDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.DiscoverGroupModels(t.Context(), group.ID)
		discoveryDone <- err
	}()
	select {
	case <-firstSelect:
	case <-time.After(time.Second):
		t.Fatal("discovery did not pause after its first SELECT")
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- writer.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&models.Group{}).Where("id = ?", group.ID).
				Updates(map[string]any{
					"name":   "discovery-snapshot-new",
					"params": models.JSON(`{"base_url":"https://discovery-new.example/v1"}`),
				}).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.Credential{}).Where("group_id = ?", group.ID).
				Update("data", newCiphertext).Error; err != nil {
				return err
			}
			return tx.Model(&models.SystemSetting{}).Where("key = ?", "header_rules").
				Update("value", `{"set":{"X-Version":"new"}}`).Error
		})
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("concurrent version update error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("discovery read blocked WAL writer")
	}
	close(releaseReader)

	select {
	case err := <-discoveryDone:
		if err != nil {
			t.Fatalf("DiscoverGroupModels() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("discovery did not finish after query barrier release")
	}
	var got observedTarget
	select {
	case got = <-observed:
	default:
		t.Fatal("discovery did not reach provider mapping")
	}
	old := observedTarget{
		baseURL: "https://discovery-old.example/v1",
		apiKey:  "discovery-key-old",
		version: "old",
	}
	newVersion := observedTarget{
		baseURL: "https://discovery-new.example/v1",
		apiKey:  "discovery-key-new",
		version: "new",
	}
	if got != old && got != newVersion {
		t.Fatalf("discovery observed mixed versions: %#v", got)
	}
}

func TestDiscoveryReleasesReadSnapshotBeforeDecrypt(t *testing.T) {
	t.Parallel()
	fixture, _ := newFileServiceFixture(t)
	group := seedPersistedDiscoveryGroup(t, fixture, true, models.JSON(`{}`))
	seedPersistedDiscoveryCredential(
		t,
		fixture,
		group.ID,
		1,
		"release-snapshot-key",
		models.CredentialStatusActive,
	)
	decryptStarted := make(chan struct{})
	releaseDecrypt := make(chan struct{})
	fixture.service.encryption = blockingDecryptService{
		Service: fixture.encryption,
		started: decryptStarted,
		release: releaseDecrypt,
	}
	fixture.service.executor = newRecordingDiscoveryExecutor(&recordingDiscoveryExecutorTarget{
		value: protocol.OpenAICompletions,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			return []string{"model"}, nil
		},
	})

	discoveryDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.DiscoverGroupModels(t.Context(), group.ID)
		discoveryDone <- err
	}()
	select {
	case <-decryptStarted:
	case <-time.After(time.Second):
		t.Fatal("discovery did not reach decrypt mapping")
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- fixture.db.Model(&models.Group{}).
			Where("id = ?", group.ID).
			Update("name", "write-during-decrypt").Error
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("write during decrypt error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("decrypt mapping retained the single pooled read connection")
	}
	close(releaseDecrypt)
	select {
	case err := <-discoveryDone:
		if err != nil {
			t.Fatalf("DiscoverGroupModels() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("discovery did not finish after decrypt release")
	}
}

func TestDiscoverGroupModelsUsesDisabledGroupAndActiveCredentialsInIDOrder(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	group := seedPersistedDiscoveryGroup(t, fixture, false, models.JSON(
		`{"header_rules":{"set":{"X-Group":"group"},"remove":["X-Remove"]}}`,
	))
	seedPersistedDiscoveryCredential(t, fixture, group.ID, 1, "key-1", models.CredentialStatusActive)
	seedPersistedDiscoveryCredential(t, fixture, group.ID, 2, "key-2", models.CredentialStatusDisabled)
	seedPersistedDiscoveryCredential(t, fixture, group.ID, 3, "key-3", models.CredentialStatusActive)
	if err := fixture.db.Create(&models.SystemSetting{
		Key: "header_rules", Value: `{"set":{"X-System":"system"}}`,
	}).Error; err != nil {
		t.Fatalf("seed system HeaderRules: %v", err)
	}

	var calls []string
	newRecorder := func(value protocol.Protocol) *recordingDiscoveryExecutorTarget {
		return &recordingDiscoveryExecutorTarget{
			value: value,
			listFn: func(
				_ context.Context,
				baseURL, apiKey string,
				rules state.HeaderRules,
			) ([]string, error) {
				calls = append(calls, string(value)+":"+apiKey)
				if baseURL != "https://persisted.example.com/v1" {
					t.Fatalf("base URL = %q, want persisted channel target", baseURL)
				}
				wantRules := state.HeaderRules{
					Set: map[string]string{"X-Group": "group"},
				}
				if !reflect.DeepEqual(rules, wantRules) {
					t.Fatalf("HeaderRules = %#v, want persisted Group override %#v", rules, wantRules)
				}
				if value == protocol.OpenAICompletions && apiKey == "key-3" {
					return []string{"z-model", "a-model"}, nil
				}
				return nil, errors.New("try next candidate")
			},
		}
	}
	fixture.service.executor = newRecordingDiscoveryExecutor(
		newRecorder(protocol.OpenAICompletions),
	)

	result, err := fixture.service.DiscoverGroupModels(t.Context(), group.ID)
	if err != nil {
		t.Fatalf("DiscoverGroupModels() error = %v", err)
	}
	if !reflect.DeepEqual(result.Models, []ModelCandidate{
		{ID: "z-model", Name: "z-model", Sources: []string{"live"}, PricingStatus: PricingStatusPending},
		{ID: "a-model", Name: "a-model", Sources: []string{"live"}, PricingStatus: PricingStatusPending},
	}) {
		t.Fatalf("models = %#v, want upstream order", result.Models)
	}
	wantCalls := []string{
		"openai-completions:key-1",
		"openai-completions:key-3",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %#v, want protocol-outer active-key-ID-inner order %#v", calls, wantCalls)
	}
}

func TestDiscoverGroupModelsReturnsNotFoundAndNoActiveUpstreamKey(t *testing.T) {
	t.Parallel()
	t.Run("missing Group", func(t *testing.T) {
		fixture := newServiceFixture(t)
		_, err := fixture.service.DiscoverGroupModels(t.Context(), 999)
		if !errors.Is(err, app_errors.ErrResourceNotFound) {
			t.Fatalf("DiscoverGroupModels() error = %v, want ErrResourceNotFound", err)
		}
	})

	t.Run("no active upstream key", func(t *testing.T) {
		fixture := newServiceFixture(t)
		group := seedPersistedDiscoveryGroup(t, fixture, true, models.JSON(`{}`))
		seedPersistedDiscoveryCredential(t, fixture, group.ID, 1, "disabled", models.CredentialStatusDisabled)
		_, err := fixture.service.DiscoverGroupModels(t.Context(), group.ID)
		if !errors.Is(err, app_errors.ErrNoActiveCredential) {
			t.Fatalf("DiscoverGroupModels() error = %v, want ErrNoActiveCredential", err)
		}
	})
}

func TestDiscoverGroupModelsUsesSubscriptionCredential(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	stage := mustImportSubscriptionStage(t, fixture, "account-saved-models", "saved@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("saved subscription discovery"), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.2"}}},
		StagedCredentialIDs: []string{stage.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	setCodexModelDiscovery(t, fixture.service, func(_ context.Context, credential codex.Credential) ([]codex.Model, error) {
		if credential.AccountID != "account-saved-models" {
			t.Fatalf("credential = %#v", credential)
		}
		return []codex.Model{{ID: "gpt-5.2"}}, nil
	})
	result, err := fixture.service.DiscoverGroupModels(t.Context(), created.GroupID)
	if err != nil || len(result.Models) != 1 || result.Models[0].ID != "gpt-5.2" {
		t.Fatalf("result = %#v, %v", result, err)
	}
}

func TestClaudeGroupDiscoversModelsAndBecomesAvailableAfterSelection(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	stage, err := fixture.service.ImportCredentialStage(t.Context(), channel.Claude, []byte(
		`{"type":"claude","access_token":"claude-access","refresh_token":"claude-refresh","account_uuid":"claude-account","email":"claude@example.com","expired":"2030-01-01T00:00:00Z"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("Claude subscription discovery"), ChannelID: channel.Claude,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{}},
		StagedCredentialIDs: []string{stage.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := fixture.service.GetGroupSummary(t.Context(), created.GroupID)
	if err != nil || before.ServiceStatus != GroupCollectionStatusUnavailable || before.ModelCount != 0 {
		t.Fatalf("summary before discovery = %#v, %v", before, err)
	}

	fixture.service.discoverSubscriptionModels = func(
		_ context.Context,
		channelID channel.ID,
		credential subscriptionruntime.Credential,
	) ([]string, error) {
		if channelID != channel.Claude {
			return nil, errors.New("unexpected subscription channel")
		}
		parsed, parseErr := claude.ParseCredentialJSON(credential.Canonical())
		if parseErr != nil || parsed.AccountUUID != "claude-account" {
			t.Fatalf("Claude credential = %#v, %v", parsed, parseErr)
		}
		return []string{"claude-sonnet-4-6", "claude-opus-4-6"}, nil
	}
	discovered, err := fixture.service.DiscoverGroupModels(t.Context(), created.GroupID)
	if err != nil || len(discovered.Models) != 2 || discovered.Models[0].ID != "claude-sonnet-4-6" {
		t.Fatalf("discovered models = %#v, %v", discovered, err)
	}
	updated, err := fixture.service.UpdateGroupModels(t.Context(), created.GroupID, GroupModelsUpdateRequest{
		Models: optionalGroupModels{Set: true, Values: []GroupModel{
			{ID: discovered.Models[0].ID}, {ID: discovered.Models[1].ID},
		}},
	})
	if err != nil || updated.Total != 2 {
		t.Fatalf("updated models = %#v, %v", updated, err)
	}
	after, err := fixture.service.GetGroupSummary(t.Context(), created.GroupID)
	if err != nil || after.ServiceStatus != GroupCollectionStatusAvailable || after.ModelCount != 2 || after.CredentialCount != 1 {
		t.Fatalf("summary after selection = %#v, %v", after, err)
	}
}

func TestDiscoverGroupModelsPreparesSubscriptionCredential(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	stage := mustImportSubscriptionStage(t, fixture, "account-prepared-models", "prepared@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("prepared subscription discovery"), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.2"}}},
		StagedCredentialIDs: []string{stage.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	prepareCalls := 0
	setCodexCredentialPreparer(t, fixture.service, func(_ context.Context, snapshot execution.CredentialSnapshot) (codex.Credential, *execution.ErrorEvidence) {
		prepareCalls++
		credential, parseErr := codex.ParseCredentialJSON(snapshot.Data())
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		credential.AccessToken = "refreshed-access"
		return credential, nil
	})
	setCodexModelDiscovery(t, fixture.service, func(_ context.Context, credential codex.Credential) ([]codex.Model, error) {
		if credential.AccessToken != "refreshed-access" {
			t.Fatalf("credential = %#v", credential)
		}
		return []codex.Model{{ID: "gpt-5.2"}}, nil
	})

	if _, err := fixture.service.DiscoverGroupModels(t.Context(), created.GroupID); err != nil || prepareCalls != 1 {
		t.Fatalf("DiscoverGroupModels() error/calls = %v/%d", err, prepareCalls)
	}
}

func TestDiscoverGroupModelsRefreshesSameCredentialOnceAfterUnauthorized(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	stage := mustImportSubscriptionStage(t, fixture, "account-model-refresh", "model-refresh@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("subscription discovery auth refresh"), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.2"}}},
		StagedCredentialIDs: []string{stage.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	var forces []bool
	fixture.service.prepareSubscriptionCredential = func(_ context.Context, _ channel.ID, snapshot execution.CredentialSnapshot, force bool) (subscriptionruntime.Credential, *execution.ErrorEvidence) {
		forces = append(forces, force)
		parsed, parseErr := codex.ParseCredentialJSON(snapshot.Data())
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if force {
			parsed.AccessToken = "fresh-access"
		}
		credential, convertErr := testRuntimeCredential(fixture.service, parsed)
		if convertErr != nil {
			t.Fatal(convertErr)
		}
		return credential, nil
	}
	discoveryCalls := 0
	fixture.service.discoverSubscriptionModels = func(_ context.Context, _ channel.ID, credential subscriptionruntime.Credential) ([]string, error) {
		discoveryCalls++
		parsed, parseErr := codex.ParseCredentialJSON(credential.Canonical())
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if parsed.AccessToken != "fresh-access" {
			return nil, &subscriptionruntime.UpstreamHTTPError{StatusCode: http.StatusUnauthorized}
		}
		return []string{"gpt-5.2"}, nil
	}

	result, err := fixture.service.DiscoverGroupModels(t.Context(), created.GroupID)
	if err != nil || !reflect.DeepEqual(forces, []bool{false, true}) || discoveryCalls != 2 || len(result.Models) != 1 {
		t.Fatalf("result/error/forces/discovery = %#v/%v/%v/%d", result, err, forces, discoveryCalls)
	}
}

func TestDiscoverGroupModelsSkipsSubscriptionCredentialRequiringAuthorization(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	stage := mustImportSubscriptionStage(t, fixture, "account-reauthorize-models", "reauthorize@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("unauthorized subscription discovery"), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.2"}}},
		StagedCredentialIDs: []string{stage.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.Credential{}).Where("group_id = ?", created.GroupID).
		Update("auth_state", models.CredentialAuthStateReauthorizationRequired).Error; err != nil {
		t.Fatal(err)
	}
	calls := 0
	setCodexModelDiscovery(t, fixture.service, func(context.Context, codex.Credential) ([]codex.Model, error) {
		calls++
		return nil, nil
	})

	_, err = fixture.service.DiscoverGroupModels(t.Context(), created.GroupID)
	if !errors.Is(err, app_errors.ErrCredentialReauthorizationRequired) || calls != 0 {
		t.Fatalf("DiscoverGroupModels() error/calls = %v/%d", err, calls)
	}
}

func TestMapGroupDiscoveryTargetRejectsUnreadySubscriptionCredential(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	stage := mustImportSubscriptionStage(t, fixture, "account-route-owned", "route-owned@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("route-owned subscription discovery"), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.2"}}},
		StagedCredentialIDs: []string{stage.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.Credential{}).Where("group_id = ?", created.GroupID).
		Update("auth_state", models.CredentialAuthStateOutcomeUnknown).Error; err != nil {
		t.Fatal(err)
	}
	rows, err := fixture.service.readGroupDiscoverySnapshot(t.Context(), created.GroupID)
	if err != nil {
		t.Fatal(err)
	}

	_, err = fixture.service.mapGroupDiscoveryTarget(t.Context(), rows)
	if !errors.Is(err, app_errors.ErrCredentialAuthOutcomeUnknown) {
		t.Fatalf("mapGroupDiscoveryTarget() error = %v, want ErrCredentialAuthOutcomeUnknown", err)
	}
}

func TestMapGroupDiscoveryTargetPreparesSubscriptionCredential(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	stage := mustImportSubscriptionStage(t, fixture, "account-route-refresh", "route-refresh@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("route-owned subscription refresh"), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.2"}}},
		StagedCredentialIDs: []string{stage.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	prepareCalls := 0
	setCodexCredentialPreparer(t, fixture.service, func(_ context.Context, snapshot execution.CredentialSnapshot) (codex.Credential, *execution.ErrorEvidence) {
		prepareCalls++
		credential, parseErr := codex.ParseCredentialJSON(snapshot.Data())
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		credential.AccessToken = "route-refreshed-access"
		return credential, nil
	})
	rows, err := fixture.service.readGroupDiscoverySnapshot(t.Context(), created.GroupID)
	if err != nil {
		t.Fatal(err)
	}

	target, err := fixture.service.mapGroupDiscoveryTarget(t.Context(), rows)
	if err != nil || prepareCalls != 1 || len(target.credentials) != 1 {
		t.Fatalf("mapGroupDiscoveryTarget() target/error/calls = %#v/%v/%d", target, err, prepareCalls)
	}
	credential, err := codex.ParseCredentialJSON(target.credentials[0].snapshot.Data())
	if err != nil || credential.AccessToken != "route-refreshed-access" {
		t.Fatalf("prepared credential = %#v, %v", credential, err)
	}
}

func TestDiscoverGroupModelsDoesNotMaskAttemptedSubscriptionFailure(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	first := mustImportSubscriptionStage(t, fixture, "account-reauthorize-first", "first@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("subscription discovery failure"), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.2"}}},
		StagedCredentialIDs: []string{first.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	second := mustImportSubscriptionStage(t, fixture, "account-upstream-failure", "second@example.com")
	if _, err := fixture.service.ConnectGroupCredentials(t.Context(), created.GroupID, []string{second.StageID}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.Credential{}).
		Where("group_id = ? AND id = (SELECT MIN(id) FROM credentials WHERE group_id = ?)", created.GroupID, created.GroupID).
		Update("auth_state", models.CredentialAuthStateReauthorizationRequired).Error; err != nil {
		t.Fatal(err)
	}
	calls := 0
	setCodexModelDiscovery(t, fixture.service, func(context.Context, codex.Credential) ([]codex.Model, error) {
		calls++
		return nil, errors.New("upstream unavailable")
	})

	_, err = fixture.service.DiscoverGroupModels(t.Context(), created.GroupID)
	if !errors.Is(err, app_errors.ErrBadGateway) || errors.Is(err, app_errors.ErrCredentialReauthorizationRequired) || calls != 1 {
		t.Fatalf("DiscoverGroupModels() error/calls = %v/%d, want BadGateway/1", err, calls)
	}
}

func TestCodexPreparationAPIErrorTreatsStateCommitFailureAsUnknown(t *testing.T) {
	t.Parallel()
	err := subscriptionPreparationAPIError(&execution.ErrorEvidence{Code: "refresh_state_commit_failed"})
	if !errors.Is(err, app_errors.ErrCredentialAuthOutcomeUnknown) {
		t.Fatalf("subscriptionPreparationAPIError() = %v, want ErrCredentialAuthOutcomeUnknown", err)
	}
}

func TestDiscoverGroupModelsDecryptsEveryKeyBeforeHTTP(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	group := seedPersistedDiscoveryGroup(t, fixture, true, models.JSON(`{}`))
	seedPersistedDiscoveryCredential(t, fixture, group.ID, 1, "key-1", models.CredentialStatusActive)
	if err := fixture.db.Create(&models.Credential{
		ID: 3, GroupID: group.ID, Data: "corrupt-second-active-ciphertext",
		Fingerprint: "corrupt-hash", Status: models.CredentialStatusActive,
	}).Error; err != nil {
		t.Fatalf("seed corrupt active key: %v", err)
	}

	calls := 0
	fixture.service.executor = newRecordingDiscoveryExecutor(&recordingDiscoveryExecutorTarget{
		value: protocol.Anthropic,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			calls++
			return nil, nil
		},
	})
	_, err := fixture.service.DiscoverGroupModels(t.Context(), group.ID)
	if !errors.Is(err, app_errors.ErrInternalServer) {
		t.Fatalf("DiscoverGroupModels() error = %v, want sanitized ErrInternalServer", err)
	}
	if calls != 0 {
		t.Fatalf("ListModels calls = %d, want zero before every key decrypts", calls)
	}
	for _, secret := range []string{"key-1", "corrupt-second-active-ciphertext", "corrupt-hash"} {
		if strings.Contains(fmt.Sprint(err), secret) {
			t.Fatalf("error exposes %q: %v", secret, err)
		}
	}
}

func TestDiscoverGroupModelsDoesNotMutateDatabaseSnapshotOrRegistry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		listFn  func(context.Context, context.CancelFunc) ([]string, error)
		wantErr error
	}{
		{
			name: "success",
			listFn: func(context.Context, context.CancelFunc) ([]string, error) {
				return []string{"remote-only"}, nil
			},
		},
		{
			name: "all candidates failed",
			listFn: func(context.Context, context.CancelFunc) ([]string, error) {
				return nil, errors.New("upstream failure")
			},
			wantErr: app_errors.ErrBadGateway,
		},
		{
			name: "parent cancellation",
			listFn: func(_ context.Context, cancel context.CancelFunc) ([]string, error) {
				cancel()
				return nil, context.Canceled
			},
			wantErr: context.Canceled,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
				ChannelID:   channel.OpenAICompatible,
				Params:      []byte(`{"base_url":"https://state.example.com/v1"}`),
				Credentials: "state-key",
				Models: optionalGroupModels{
					Set: true, Values: []GroupModel{{ID: "persisted-model"}},
				}, ConnectionType: "api_key",
			})
			if err != nil {
				t.Fatalf("seed CreateGroup() error = %v", err)
			}
			before := captureGroupDiscoveryState(t, fixture, created.GroupID)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fixture.service.executor = newRecordingDiscoveryExecutor(&recordingDiscoveryExecutorTarget{
				value: protocol.OpenAICompletions,
				listFn: func(ctx context.Context, _, _ string, _ state.HeaderRules) ([]string, error) {
					return test.listFn(ctx, cancel)
				},
			})

			_, err = fixture.service.DiscoverGroupModels(ctx, created.GroupID)
			if test.wantErr == nil && err != nil {
				t.Fatalf("DiscoverGroupModels() error = %v", err)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("DiscoverGroupModels() error = %v, want %v", err, test.wantErr)
			}
			after := captureGroupDiscoveryState(t, fixture, created.GroupID)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("discovery mutated persistent/runtime state\nbefore=%#v\nafter=%#v", before, after)
			}
		})
	}
}

func TestDiscoverGroupModelsDoesNotAcquireWriteMuOrBlockWrites(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ChannelID:   channel.OpenAICompatible,
		Params:      []byte(`{"base_url":"https://discovery-lock.example.com/v1"}`),
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Credentials: "key-1", ConnectionType: "api_key",
	})
	if err != nil {
		t.Fatalf("seed CreateGroup() error = %v", err)
	}
	fixture.service.modelDiscoveryTimeout = 3 * time.Second
	fixture.service.executor = newRecordingDiscoveryExecutor(&recordingDiscoveryExecutorTarget{
		value: protocol.OpenAICompletions,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			return []string{"model"}, nil
		},
	})

	fixture.service.writeMu.Lock()
	lockCheckDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.DiscoverGroupModels(t.Context(), created.GroupID)
		lockCheckDone <- err
	}()
	select {
	case err := <-lockCheckDone:
		if err != nil {
			t.Fatalf("DiscoverGroupModels() error while writeMu locked = %v", err)
		}
	case <-time.After(time.Second):
		fixture.service.writeMu.Unlock()
		t.Fatal("DiscoverGroupModels() waited for writeMu")
	}
	fixture.service.writeMu.Unlock()

	entered := make(chan struct{})
	release := make(chan struct{})
	fixture.service.executor = newRecordingDiscoveryExecutor(&recordingDiscoveryExecutorTarget{
		value: protocol.OpenAICompletions,
		listFn: func(ctx context.Context, _, _ string, _ state.HeaderRules) ([]string, error) {
			close(entered)
			select {
			case <-release:
				return []string{"model"}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	})
	discoveryDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.DiscoverGroupModels(t.Context(), created.GroupID)
		discoveryDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("discovery did not reach HTTP before timeout")
	}

	groupWriteDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
			ChannelID:   channel.Anthropic,
			Params:      []byte(`{"base_url":"https://concurrent-group-write.example.com"}`),
			Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
			Credentials: "concurrent-group-key", ConnectionType: "api_key",
		})
		groupWriteDone <- err
	}()
	select {
	case err := <-groupWriteDone:
		if err != nil {
			t.Fatalf("concurrent CreateGroup() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("long discovery blocked Group write")
	}

	keyWriteDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.ImportGroupCredentials(t.Context(), created.GroupID, CredentialImportRequest{
			Credentials: "concurrent-imported-key",
		})
		keyWriteDone <- err
	}()
	select {
	case err := <-keyWriteDone:
		if err != nil {
			t.Fatalf("concurrent ImportGroupKeys() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("long discovery blocked key write")
	}

	close(release)
	select {
	case err := <-discoveryDone:
		if err != nil {
			t.Fatalf("DiscoverGroupModels() error after release = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("discovery did not finish after release")
	}
}

type groupDiscoveryState struct {
	rowCounts        [3]int64
	models           string
	config           string
	snapshot         *state.ConfigSnapshot
	snapshotRevision uint64
	registryKeys     []state.CredentialMeta
	registryValues   map[uint]string
}

func captureGroupDiscoveryState(t *testing.T, fixture serviceFixture, groupID uint) groupDiscoveryState {
	t.Helper()
	var group models.Group
	if err := fixture.db.First(&group, groupID).Error; err != nil {
		t.Fatalf("load Group state: %v", err)
	}
	var credentialRows []models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Order("id ASC").Find(&credentialRows).Error; err != nil {
		t.Fatalf("load credential state: %v", err)
	}
	registryValues := make(map[uint]string, len(credentialRows))
	for _, credentialRow := range credentialRows {
		if ciphertext, ok := fixture.registry.EncryptedCredentialData(credentialRow.ID); ok {
			registryValues[credentialRow.ID] = ciphertext
		}
	}
	snapshot := fixture.manager.Current()
	return groupDiscoveryState{
		rowCounts:        discoveryRowCounts(t, fixture.db),
		models:           string(group.Models),
		config:           string(group.Overrides),
		snapshot:         snapshot,
		snapshotRevision: snapshot.Revision,
		registryKeys:     fixture.registry.CollectCredentialCandidates([]uint{groupID}, nil, time.Time{}),
		registryValues:   registryValues,
	}
}

func seedPersistedDiscoveryGroup(
	t *testing.T,
	fixture serviceFixture,
	enabled bool,
	overrides models.JSON,
) models.Group {
	t.Helper()
	group := models.Group{
		Name: "persisted-discovery", ChannelID: string(channel.OpenAICompatible),
		Params:    models.JSON(`{"base_url":"https://persisted.example.com/v1"}`),
		Models:    models.JSON(`[{"id":"persisted-only"}]`),
		Overrides: overrides, Enabled: enabled,
	}
	if err := fixture.db.Create(&group).Error; err != nil {
		t.Fatalf("seed persisted discovery Group: %v", err)
	}
	return group
}

func seedPersistedDiscoveryCredential(
	t *testing.T,
	fixture serviceFixture,
	groupID, credentialID uint,
	plaintext string,
	status models.CredentialStatus,
) {
	t.Helper()
	canonical := `{"api_key":` + fmt.Sprintf("%q", plaintext) + `}`
	ciphertext, err := fixture.encryption.Encrypt(canonical)
	if err != nil {
		t.Fatalf("encrypt seeded discovery credential: %v", err)
	}
	if err := fixture.db.Create(&models.Credential{
		ID: credentialID, GroupID: groupID, Data: ciphertext,
		Fingerprint: fixture.encryption.Hash(canonical), Status: status,
	}).Error; err != nil {
		t.Fatalf("seed persisted discovery credential: %v", err)
	}
}
