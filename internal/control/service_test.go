package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/gateway"
	"gpt-load/internal/health"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

func TestReadSnapshotKeepsOneVersionWhileWALWriterCommits(t *testing.T) {
	fixture, dsn := newFileServiceFixture(t)
	group := validControlGroup("read-snapshot-old")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
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

	firstRead := make(chan struct{})
	releaseRead := make(chan struct{})
	readDone := make(chan error, 1)
	var firstName, secondName string
	go func() {
		readDone <- fixture.service.withReadSnapshot(t.Context(), func(tx *gorm.DB) error {
			var first models.Group
			if err := tx.First(&first, group.ID).Error; err != nil {
				return err
			}
			firstName = first.Name
			close(firstRead)
			<-releaseRead
			var second models.Group
			if err := tx.First(&second, group.ID).Error; err != nil {
				return err
			}
			secondName = second.Name
			return nil
		})
	}()
	select {
	case <-firstRead:
	case <-time.After(time.Second):
		t.Fatal("read snapshot did not complete its first SELECT")
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- writer.Model(&models.Group{}).
			Where("id = ?", group.ID).
			Update("name", "read-snapshot-new").Error
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("WAL writer update error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("read snapshot blocked WAL writer")
	}
	close(releaseRead)
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("withReadSnapshot() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("read snapshot did not finish after release")
	}
	if firstName != "read-snapshot-old" || secondName != firstName {
		t.Fatalf("snapshot names = %q/%q, want one old version", firstName, secondName)
	}
}

func TestReadSnapshotCancellationTakesPrecedenceAndReleasesConnection(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	const callbackCause = "callback detail must not win over cancellation"

	err := fixture.service.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&models.Group{}).Count(&count).Error; err != nil {
			return err
		}
		cancel()
		return errors.New(callbackCause)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("withReadSnapshot() error = %v, want context.Canceled", err)
	}
	if strings.Contains(err.Error(), callbackCause) {
		t.Fatalf("cancellation error exposed lower-priority callback detail: %v", err)
	}

	var count int64
	if err := fixture.db.Model(&models.Group{}).Count(&count).Error; err != nil {
		t.Fatalf("query after canceled snapshot error = %v", err)
	}
}

func TestWriteConfigDiscardsConnectionAfterCommitBusy(t *testing.T) {
	fixture, dsn := newFileServiceFixture(t)
	beforeRevision := fixture.manager.Current().Revision
	releaseReader := holdRollbackJournalReadLock(t, fixture.db, dsn)

	callbackRan := false
	_, err := fixture.service.writeConfig(t.Context(), func(tx *gorm.DB) error {
		callbackRan = true
		return tx.Create(validControlGroup("commit-busy")).Error
	}, nil)
	if err == nil || !callbackRan {
		t.Fatalf("writeConfig() error/callback = %v/%t, want COMMIT failure", err, callbackRan)
	}
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrDatabase.Code {
		t.Fatalf("writeConfig() error = %#v, want DATABASE_ERROR", err)
	}
	if fixture.manager.Current().Revision != beforeRevision {
		t.Fatal("failed COMMIT published Snapshot")
	}

	releaseReader()
	var failedCount int64
	if err := fixture.db.Model(&models.Group{}).
		Where("name = ?", "commit-busy").Count(&failedCount).Error; err != nil {
		t.Fatalf("query failed transaction: %v", err)
	}
	if failedCount != 0 {
		t.Fatalf("ghost group count = %d, want 0", failedCount)
	}
	var mode string
	if err := fixture.db.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("reopened journal_mode = %q, want wal", mode)
	}

	_, err = fixture.service.writeConfig(t.Context(), func(tx *gorm.DB) error {
		return tx.Create(validControlGroup("after-commit-busy")).Error
	}, nil)
	if err != nil {
		t.Fatalf("next writeConfig() error = %v", err)
	}
	assertGroupCount(t, fixture.db, 1)
}

func TestWriteConfigRollsBackWhenCompileRejectsCandidate(t *testing.T) {
	fixture := newServiceFixture(t)
	before := fixture.manager.Current().Revision

	_, err := fixture.service.writeConfig(context.Background(), func(tx *gorm.DB) error {
		return tx.Create(&models.Group{
			Name: "invalid", ChannelID: "unknown", Params: models.JSON(`{}`),
			Models: models.JSON(`[]`), Overrides: models.JSON(`{}`), Enabled: true,
		}).Error
	}, nil)
	if err == nil {
		t.Fatal("writeConfig() error = nil, want Compile rejection")
	}
	assertGroupCount(t, fixture.db, 0)
	if got := fixture.manager.Current().Revision; got != before {
		t.Fatalf("revision = %d, want %d", got, before)
	}
}

func TestWriteConfigAppliesRuntimeBeforePublishingSnapshot(t *testing.T) {
	fixture := newServiceFixture(t)
	beforeSnapshot := fixture.manager.Current()
	group := validControlGroup("registry-before-snapshot")
	var credential models.Credential

	snapshot, err := fixture.service.writeConfig(t.Context(), func(tx *gorm.DB) error {
		if err := tx.Create(group).Error; err != nil {
			return err
		}
		credential = models.Credential{
			GroupID: group.ID, Data: "ciphertext-runtime-order",
			Fingerprint: "hash-runtime-order", Status: models.CredentialStatusActive,
		}
		return tx.Create(&credential).Error
	}, func() error {
		if fixture.service.writeMu.TryLock() {
			fixture.service.writeMu.Unlock()
			return fmt.Errorf("writeMu was not held")
		}
		if fixture.manager.Current() != beforeSnapshot {
			return fmt.Errorf("Snapshot published before Registry update")
		}
		return fixture.registry.ApplyCredentialImport(group.ID, []state.CredentialEntry{{
			ID: credential.ID, GroupID: group.ID,
			Version:            groupCollectionCredentialVersion(credential.SecretVersion),
			IdentityGeneration: groupCollectionCredentialIdentity(credential.IdentityFingerprint, *group),
			Fingerprint:        credential.Fingerprint, Status: state.CredentialStatusActive,
			EncryptedValue: credential.Data,
		}})
	})
	if err != nil {
		t.Fatalf("writeConfig() error = %v", err)
	}
	if snapshot.Revision != beforeSnapshot.Revision+1 {
		t.Fatalf("revision = %d", snapshot.Revision)
	}
	if _, ok := snapshot.Groups[group.ID]; !ok {
		t.Fatalf("Snapshot missing Group %d", group.ID)
	}
	if got, ok := fixture.registry.EncryptedCredentialData(credential.ID); !ok || got != credential.Data {
		t.Fatalf("Registry key = %q, %t", got, ok)
	}
}

func TestWriteConfigMakesCreatedGroupAndFirstKeyAtomicallyVisibleToDataPlane(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.random = strings.NewReader(strings.Repeat("\x01", 16))
	accessKey, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "client"})
	if err != nil {
		t.Fatalf("CreateAccessKey() error = %v", err)
	}

	type upstreamRequest struct {
		path          string
		authorization string
	}
	upstreamRequests := make(chan upstreamRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		upstreamRequests <- upstreamRequest{
			path: request.URL.Path, authorization: request.Header.Get("Authorization"),
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"response"}`))
	}))
	defer upstream.Close()

	dialects := dialect.NewSet(dialect.NewOpenAI())
	handler := gateway.NewHandler(
		fixture.manager,
		fixture.registry,
		fixture.encryption,
		gateway.NewExecutionForwarder(controlHTTPExecutor{}),
		dialects,
		health.NewStatsStore(),
		health.NewMutationCoordinator(),
		nil,
		nil,
		nil,
	)
	engine := gin.New()
	registerGatewayRoutes(t, engine, handler)
	performRequest := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/chat/completions",
			strings.NewReader(`{"model":"gpt-4o"}`),
		)
		request.Header.Set("Authorization", "Bearer "+accessKey.Key)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		return recorder
	}

	group := validControlGroup("atomic-runtime-publication")
	group.ChannelID = string(channel.OpenAICompatible)
	group.Params = models.JSON(`{"base_url":"` + upstream.URL + `/v1"}`)
	const providerKey = "sk-atomic-runtime-publication"
	credentialData := `{"api_key":"` + providerKey + `"}`
	ciphertext, err := fixture.encryption.Encrypt(credentialData)
	if err != nil {
		t.Fatalf("Encrypt(provider key) error = %v", err)
	}
	var credential models.Credential
	runtimeApplied := make(chan struct{})
	allowPublish := make(chan struct{})
	var releaseOnce sync.Once
	releasePublish := func() { releaseOnce.Do(func() { close(allowPublish) }) }
	defer releasePublish()

	type writeResult struct {
		snapshot *state.ConfigSnapshot
		err      error
	}
	writeDone := make(chan writeResult, 1)
	go func() {
		snapshot, writeErr := fixture.service.writeConfig(t.Context(), func(tx *gorm.DB) error {
			if createErr := tx.Create(group).Error; createErr != nil {
				return createErr
			}
			credential = models.Credential{
				GroupID: group.ID, Data: ciphertext,
				Fingerprint: fixture.encryption.Hash(credentialData), Status: models.CredentialStatusActive,
			}
			return tx.Create(&credential).Error
		}, func() error {
			entries, buildErr := stateloader.BuildGroupCredentialEntries(t.Context(), fixture.db, group.ID)
			if buildErr != nil {
				return buildErr
			}
			if applyErr := fixture.registry.ApplyCredentialImport(group.ID, entries); applyErr != nil {
				return applyErr
			}
			close(runtimeApplied)
			<-allowPublish
			return nil
		})
		writeDone <- writeResult{snapshot: snapshot, err: writeErr}
	}()

	select {
	case <-runtimeApplied:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Registry publication barrier")
	}
	beforePublish := performRequest()
	if beforePublish.Code != http.StatusServiceUnavailable ||
		!strings.Contains(beforePublish.Body.String(), "no_available_candidate") {
		t.Fatalf("request before Snapshot publication = %d %s, want no candidate", beforePublish.Code, beforePublish.Body.String())
	}
	select {
	case request := <-upstreamRequests:
		t.Fatalf("request before Snapshot publication reached upstream: %#v", request)
	default:
	}

	releasePublish()
	var result writeResult
	select {
	case result = <-writeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Snapshot publication")
	}
	if result.err != nil {
		t.Fatalf("writeConfig() error = %v", result.err)
	}
	if _, ok := result.snapshot.Groups[group.ID]; !ok {
		t.Fatalf("published Snapshot missing Group %d", group.ID)
	}

	afterPublish := performRequest()
	if afterPublish.Code != http.StatusOK || afterPublish.Body.String() != `{"id":"response"}` {
		t.Fatalf("request after Snapshot publication = %d %s", afterPublish.Code, afterPublish.Body.String())
	}
	select {
	case request := <-upstreamRequests:
		if request.path != "/v1/chat/completions" || request.authorization != "Bearer "+providerKey {
			t.Fatalf("upstream request = %#v", request)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("published Group and first key did not reach upstream")
	}
}

func TestWriteConfigRuntimeFailureReloadsCommittedDatabaseTruth(t *testing.T) {
	fixture := newServiceFixture(t)
	beforeSnapshot := fixture.manager.Current()
	const secretCause = "forced Registry publication failure"
	_, err := fixture.service.writeConfig(t.Context(), func(tx *gorm.DB) error {
		return tx.Create(validControlGroup("runtime-failure")).Error
	}, func() error {
		return errors.New(secretCause)
	})
	if err == nil {
		t.Fatal("writeConfig() error = nil")
	}
	if !errors.Is(err, app_errors.ErrInternalServer) {
		t.Fatalf("writeConfig() error = %v, want ErrInternalServer", err)
	}
	var operationErr *controlOperationError
	if !errors.As(err, &operationErr) || operationErr.stage != stageApplyCommittedRegistryMutation {
		t.Fatalf("writeConfig() operation error = %#v", operationErr)
	}

	var logs bytes.Buffer
	logger := logrus.StandardLogger()
	previousOutput, previousFormatter := logger.Out, logger.Formatter
	logrus.SetOutput(&logs)
	logrus.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	t.Cleanup(func() {
		logrus.SetOutput(previousOutput)
		logrus.SetFormatter(previousFormatter)
	})
	logServiceError(
		"update_group",
		withControlOperationContext(err, 91, 7),
		app_errors.ErrInternalServer.Code,
	)
	logText := logs.String()
	for _, required := range []string{
		`"stage":"apply_committed_registry_mutation"`,
		`"group_id":91`,
		`"credential_id":7`,
	} {
		if !strings.Contains(logText, required) {
			t.Fatalf("log output missing %q: %s", required, logText)
		}
	}
	if strings.Contains(logText, secretCause) {
		t.Fatalf("log output leaked secret cause: %s", logText)
	}
	assertGroupCount(t, fixture.db, 1)
	afterSnapshot := fixture.manager.Current()
	if afterSnapshot == beforeSnapshot || afterSnapshot.Revision != beforeSnapshot.Revision+1 {
		t.Fatalf("recovered snapshot revision = %d, want %d", afterSnapshot.Revision, beforeSnapshot.Revision+1)
	}
	if len(afterSnapshot.Groups) != 1 {
		t.Fatalf("recovered snapshot groups = %#v, want committed database truth", afterSnapshot.Groups)
	}
	want, compileErr := state.Compile(mustBuildCompileInput(t, fixture.db))
	if compileErr != nil {
		t.Fatal(compileErr)
	}
	want.Revision = afterSnapshot.Revision
	if !reflect.DeepEqual(afterSnapshot, want) {
		t.Fatalf("recovered snapshot differs from database\ngot=%#v\nwant=%#v", afterSnapshot, want)
	}
}

func TestWriteConfigSnapshotFailureReloadsCommittedDatabaseTruth(t *testing.T) {
	fixture := newServiceFixture(t)
	beforeRevision := fixture.manager.Current().Revision
	fixture.service.publishSnapshot = func(state.CompileInput) (*state.ConfigSnapshot, error) {
		return nil, errors.New("forced snapshot publication failure")
	}

	_, err := fixture.service.writeConfig(t.Context(), func(tx *gorm.DB) error {
		return tx.Create(validControlGroup("snapshot-recovery")).Error
	}, nil)
	if err == nil {
		t.Fatal("writeConfig() error = nil")
	}
	var operationErr *controlOperationError
	if !errors.As(err, &operationErr) || operationErr.stage != stagePublishCommittedSnapshot {
		t.Fatalf("writeConfig() operation error = %#v", operationErr)
	}
	after := fixture.manager.Current()
	if after == nil || after.Revision != beforeRevision+1 || len(after.Groups) != 1 {
		t.Fatalf("recovered snapshot = %#v, want committed database truth", after)
	}
}

func TestWriteConfigRecoveryPreservesCredentialRuntimeState(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("runtime-health-preserved")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	credential := models.Credential{
		GroupID: group.ID, Data: "ciphertext-runtime-health",
		Fingerprint: "secret-runtime-health", IdentityFingerprint: "identity-runtime-health",
		SecretVersion: 1, AuthState: models.CredentialAuthStateReady,
		Status: models.CredentialStatusActive,
	}
	if err := fixture.db.Create(&credential).Error; err != nil {
		t.Fatal(err)
	}
	entries, err := stateloader.BuildCredentialEntries(t.Context(), fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.registry.ReplaceCredentials(entries); err != nil {
		t.Fatal(err)
	}
	cooldownUntil := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	if !fixture.registry.SetCooldown(credential.ID, cooldownUntil) {
		t.Fatal("SetCooldown() = false")
	}
	fixture.service.publishSnapshot = func(state.CompileInput) (*state.ConfigSnapshot, error) {
		return nil, errors.New("forced snapshot publication failure")
	}

	if _, err := fixture.service.writeConfig(t.Context(), func(*gorm.DB) error { return nil }, nil); err == nil {
		t.Fatal("writeConfig() error = nil")
	}
	views := fixture.registry.Snapshot()
	if len(views) != 1 || !views[0].CooldownUntil.Equal(cooldownUntil) {
		t.Fatalf("registry views = %#v", views)
	}
}

func TestWriteConfigSerializesConcurrentDatabaseAndSnapshotPublication(t *testing.T) {
	fixture := newServiceFixture(t)
	before := fixture.manager.Current().Revision
	start := make(chan struct{})
	errors := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)

	for _, name := range []string{"first", "second"} {
		name := name
		go func() {
			ready.Done()
			<-start
			_, err := fixture.service.writeConfig(context.Background(), func(tx *gorm.DB) error {
				return tx.Create(validControlGroup(name)).Error
			}, nil)
			errors <- err
		}()
	}
	ready.Wait()
	close(start)
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatalf("concurrent writeConfig() error = %v", err)
		}
	}

	assertGroupCount(t, fixture.db, 2)
	snapshot := fixture.manager.Current()
	if snapshot.Revision != before+2 {
		t.Fatalf("revision = %d, want %d", snapshot.Revision, before+2)
	}
	if len(snapshot.Groups) != 2 {
		t.Fatalf("snapshot groups = %#v, want two", snapshot.Groups)
	}
}

func TestConcurrentCreateGroupsPublishDatabaseTruth(t *testing.T) {
	fixture := newServiceFixture(t)
	before := fixture.manager.Current().Revision
	requests := []GroupCreateRequest{
		{ChannelID: channel.OpenAICompatible, Params: json.RawMessage(`{"base_url":"https://shared.example.com/v1"}`), Models: optionalGroupModels{Set: true, Values: []GroupModel{}}, Credentials: "sk-shared-a", ConfirmSameTarget: true},
		{ChannelID: channel.OpenAICompatible, Params: json.RawMessage(`{"base_url":"https://shared.example.com/v1"}`), Models: optionalGroupModels{Set: true, Values: []GroupModel{}}, Credentials: "sk-shared-b", ConfirmSameTarget: true},
		{ChannelID: channel.OpenAICompatible, Params: json.RawMessage(`{"base_url":"https://one.example.com/v1"}`), Models: optionalGroupModels{Set: true, Values: []GroupModel{}}, Credentials: "sk-one"},
		{ChannelID: channel.OpenAICompatible, Params: json.RawMessage(`{"base_url":"https://two.example.com/v1"}`), Models: optionalGroupModels{Set: true, Values: []GroupModel{}}, Credentials: "sk-two"},
		{ChannelID: channel.OpenAICompatible, Params: json.RawMessage(`{"base_url":"https://three.example.com/v1"}`), Models: optionalGroupModels{Set: true, Values: []GroupModel{}}, Credentials: "sk-three"},
		{ChannelID: channel.OpenAICompatible, Params: json.RawMessage(`{"base_url":"https://four.example.com/v1"}`), Models: optionalGroupModels{Set: true, Values: []GroupModel{}}, Credentials: "sk-four"},
	}

	start := make(chan struct{})
	errors := make(chan error, len(requests))
	var ready sync.WaitGroup
	ready.Add(len(requests))
	for _, request := range requests {
		request := request
		go func() {
			ready.Done()
			<-start
			_, err := fixture.service.CreateGroup(context.Background(), request)
			errors <- err
		}()
	}
	ready.Wait()
	close(start)
	for range requests {
		if err := <-errors; err != nil {
			t.Fatalf("concurrent CreateGroup() error = %v", err)
		}
	}

	input, err := stateloader.BuildCompileInput(context.Background(), fixture.db)
	if err != nil {
		t.Fatalf("BuildCompileInput() error = %v", err)
	}
	want, err := state.Compile(input)
	if err != nil {
		t.Fatalf("Compile(DB input) error = %v", err)
	}
	got := fixture.manager.Current()
	want.Revision = got.Revision
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("published snapshot differs from DB compile\ngot=%#v\nwant=%#v", got, want)
	}
	if got.Revision != before+uint64(len(requests)) {
		t.Fatalf("revision = %d, want %d", got.Revision, before+uint64(len(requests)))
	}
}

func validControlGroup(name string) *models.Group {
	baseURL := "https://" + name + ".example/v1"
	return &models.Group{
		Name: name, ChannelID: "openai_compatible", Params: models.JSON(`{"base_url":"` + baseURL + `"}`),
		Models: models.JSON(`[{"id":"gpt-4o"}]`), Overrides: models.JSON(`{}`), Enabled: true,
	}
}
