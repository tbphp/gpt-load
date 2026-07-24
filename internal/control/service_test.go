package control

import (
	"context"
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
	"gorm.io/gorm"

	"gpt-load/internal/dialect"
	"gpt-load/internal/gateway"
	"gpt-load/internal/health"
	app_errors "gpt-load/internal/platform/errors"
	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

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
			Name: "invalid", UpstreamURL: "https://invalid.example",
			Protocols: models.JSON(`[]`),
			Models:    models.JSON(`[]`), Config: models.JSON(`{}`), Enabled: true,
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
	var key models.UpstreamKey

	snapshot, err := fixture.service.writeConfig(t.Context(), func(tx *gorm.DB) error {
		if err := tx.Create(group).Error; err != nil {
			return err
		}
		key = models.UpstreamKey{
			GroupID: group.ID, KeyValue: "ciphertext-runtime-order",
			KeyHash: "hash-runtime-order", Status: models.UpstreamKeyStatusActive,
		}
		return tx.Create(&key).Error
	}, func() error {
		if fixture.service.writeMu.TryLock() {
			fixture.service.writeMu.Unlock()
			return fmt.Errorf("writeMu was not held")
		}
		if fixture.manager.Current() != beforeSnapshot {
			return fmt.Errorf("Snapshot published before Registry update")
		}
		return fixture.registry.ApplyImport(group.ID, []state.KeyEntry{{
			ID: key.ID, GroupID: group.ID, Status: state.KeyStatusActive,
			EncryptedValue: key.KeyValue,
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
	if got, ok := fixture.registry.EncryptedValue(key.ID); !ok || got != key.KeyValue {
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

	dialects := dialect.NewSet(dialect.NewOpenAI(http.DefaultClient))
	handler := gateway.NewHandler(
		fixture.manager,
		fixture.registry,
		fixture.encryption,
		gateway.NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()),
		dialects,
		health.NewStatsStore(),
		nil,
		nil,
	)
	engine := gin.New()
	handler.RegisterRoutes(engine)
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
	group.UpstreamURL = upstream.URL
	const providerKey = "sk-atomic-runtime-publication"
	ciphertext, err := fixture.encryption.Encrypt(providerKey)
	if err != nil {
		t.Fatalf("Encrypt(provider key) error = %v", err)
	}
	var key models.UpstreamKey
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
			key = models.UpstreamKey{
				GroupID: group.ID, KeyValue: ciphertext,
				KeyHash: fixture.encryption.Hash(providerKey), Status: models.UpstreamKeyStatusActive,
			}
			return tx.Create(&key).Error
		}, func() error {
			if applyErr := fixture.registry.ApplyImport(group.ID, []state.KeyEntry{{
				ID: key.ID, GroupID: group.ID, Status: state.KeyStatusActive,
				EncryptedValue: key.KeyValue,
			}}); applyErr != nil {
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

func TestWriteConfigRuntimeFailureKeepsOldSnapshot(t *testing.T) {
	fixture := newServiceFixture(t)
	beforeSnapshot := fixture.manager.Current()
	_, err := fixture.service.writeConfig(t.Context(), func(tx *gorm.DB) error {
		return tx.Create(validControlGroup("runtime-failure")).Error
	}, func() error {
		return errors.New("forced Registry publication failure")
	})
	if err == nil {
		t.Fatal("writeConfig() error = nil")
	}
	if fixture.manager.Current() != beforeSnapshot {
		t.Fatal("runtime failure published Snapshot")
	}
	assertGroupCount(t, fixture.db, 1)
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
		{UpstreamURL: "https://shared.example.com/v1", Protocols: []protocol.Protocol{protocol.OpenAI}, Keys: "sk-shared-a", ConfirmSameUpstreamURL: true},
		{UpstreamURL: "https://shared.example.com/v1", Protocols: []protocol.Protocol{protocol.Anthropic}, Keys: "sk-shared-b", ConfirmSameUpstreamURL: true},
		{UpstreamURL: "https://one.example.com/v1", Protocols: []protocol.Protocol{protocol.OpenAI}, Keys: "sk-one"},
		{UpstreamURL: "https://two.example.com/v1", Protocols: []protocol.Protocol{protocol.Gemini}, Keys: "sk-two"},
		{UpstreamURL: "https://three.example.com/v1", Protocols: []protocol.Protocol{protocol.OpenAI}, Keys: "sk-three"},
		{UpstreamURL: "https://four.example.com/v1", Protocols: []protocol.Protocol{protocol.Anthropic}, Keys: "sk-four"},
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
	return &models.Group{
		Name: name, UpstreamURL: "https://" + name + ".example/v1",
		Protocols: models.JSON(`["openai"]`),
		Models:    models.JSON(`[{"id":"gpt-4o"}]`), Config: models.JSON(`{}`), Enabled: true,
	}
}
