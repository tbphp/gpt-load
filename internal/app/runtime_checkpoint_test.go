package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"gpt-load/internal/health"
	"gpt-load/internal/state"
	"gpt-load/internal/storage"

	"github.com/sirupsen/logrus"
)

type runtimeCheckpointFake struct {
	restore func(context.Context) error
	save    func(context.Context) error
}

type checkpointLifecycleLogHook struct {
	entries []*logrus.Entry
}

func (hook *checkpointLifecycleLogHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (hook *checkpointLifecycleLogHook) Fire(entry *logrus.Entry) error {
	if entry.Data["event"] == "startup.checkpoint_restore" {
		hook.entries = append(hook.entries, entry)
	}
	return nil
}

func (fake runtimeCheckpointFake) Restore(ctx context.Context) error {
	if fake.restore == nil {
		return nil
	}
	return fake.restore(ctx)
}

func (fake runtimeCheckpointFake) Save(ctx context.Context) error {
	if fake.save == nil {
		return nil
	}
	return fake.save(ctx)
}

func TestAppRestoresCheckpointAfterRuntimeRecovery(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	var order []string
	checkpoint := runtimeCheckpointFake{
		restore: func(context.Context) error {
			order = append(order, "checkpoint")
			return nil
		},
	}
	application := NewApp(AppParams{
		Engine: mustNewEngine(t),
		Config: testConfig(t),
		DB:     db,
		StartupBootstrap: startupBootstrapFunc(func(context.Context) error {
			order = append(order, "bootstrap")
			return nil
		}),
		RuntimeState: runtimeStateLoaderFunc(func(context.Context) error {
			order = append(order, "runtime")
			return nil
		}),
		StartupRecovery: startupRecoveryFunc(func(context.Context) error {
			order = append(order, "recovery")
			return nil
		}),
		RuntimeCheckpoint: checkpoint,
		ControlRuntime:    newControlRuntimeFake(nil, false),
		RequestLogs:       newRequestLogRuntimeFake(nil, nil),
	})
	cleanupApp(t, application)

	if err := application.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if want := []string{"bootstrap", "runtime", "recovery", "checkpoint"}; !slices.Equal(order, want) {
		t.Fatalf("startup order = %#v, want %#v", order, want)
	}
}

func TestAppLogsCheckpointRestoreFailureOnce(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, runtimeStateCheckpointFileName)
	if err := os.WriteFile(path, []byte(`{"credentials":[`), 0o600); err != nil {
		t.Fatalf("write malformed checkpoint fixture: %v", err)
	}

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	application := NewApp(AppParams{
		Engine:            mustNewEngine(t),
		Config:            testConfig(t),
		DB:                db,
		StartupBootstrap:  startupBootstrapFunc(noopStartupBootstrap),
		RuntimeState:      runtimeStateLoaderFunc(func(context.Context) error { return nil }),
		RuntimeCheckpoint: NewFileRuntimeStateCheckpoint(dataDir, nil, nil),
		ControlRuntime:    newControlRuntimeFake(nil, false),
		RequestLogs:       newRequestLogRuntimeFake(nil, nil),
	})
	cleanupApp(t, application)

	hook := &checkpointLifecycleLogHook{}
	logger := logrus.StandardLogger()
	previousHooks := make(logrus.LevelHooks, len(logger.Hooks))
	for level, hooks := range logger.Hooks {
		previousHooks[level] = append([]logrus.Hook(nil), hooks...)
	}
	logger.AddHook(hook)
	t.Cleanup(func() { logger.ReplaceHooks(previousHooks) })

	if err := application.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(hook.entries) != 1 {
		t.Fatalf("checkpoint restore lifecycle log count = %d, want 1", len(hook.entries))
	}
	if got := hook.entries[0].Level; got != logrus.WarnLevel {
		t.Fatalf("checkpoint restore lifecycle log level = %s, want warning", got)
	}
}

func TestAppSavesCheckpointBeforeRequestLogsAndUsesIndependentContext(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	var order []string
	checkpoint := runtimeCheckpointFake{
		save: func(ctx context.Context) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			order = append(order, "checkpoint")
			return nil
		},
	}
	requestLogs := newRequestLogRuntimeFake(nil, func(context.Context) error {
		order = append(order, "request-logs")
		return nil
	})
	controlRuntime := newControlRuntimeFake(nil, true)
	application := NewApp(AppParams{
		Engine:            mustNewEngine(t),
		Config:            testConfig(t),
		DB:                db,
		StartupBootstrap:  startupBootstrapFunc(noopStartupBootstrap),
		RuntimeState:      runtimeStateLoaderFunc(func(context.Context) error { return nil }),
		RuntimeCheckpoint: checkpoint,
		ControlRuntime:    controlRuntime,
		RequestLogs:       requestLogs,
	})
	cleanupApp(t, application)
	t.Cleanup(controlRuntime.Release)

	if err := application.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := application.Stop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop() error = %v, want context cancellation from forced drain", err)
	}
	if want := []string{"checkpoint", "request-logs"}; !slices.Equal(order, want) {
		t.Fatalf("shutdown order = %#v, want %#v", order, want)
	}
}

func TestFileRuntimeStateCheckpointRestoresAndConsumesFile(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, runtimeStateCheckpointFileName)

	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 10, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: state.CredentialStatusActive, WeightAuto: 37,
		EncryptedValue: "cipher-one",
	}}); err != nil {
		t.Fatalf("replace registry: %v", err)
	}
	registry.SetCooldown(1, time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC))
	registry.SetBlacklisted(1)
	registry.IncrFailure(1)
	stats := health.NewStatsStore()
	stats.RecordFailure(1, health.FailureCategoryUpstreamHostError, 503, time.Date(2026, 8, 7, 11, 59, 0, 0, time.UTC))

	checkpoint := NewFileRuntimeStateCheckpoint(dataDir, registry, stats)
	if err := checkpoint.Save(context.Background()); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("checkpoint file stat error = %v", err)
	}

	loadedRegistry := state.NewCredentialRegistry()
	if err := loadedRegistry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 10, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: state.CredentialStatusActive, WeightAuto: 1,
		EncryptedValue: "cipher-one",
	}, {
		ID: 2, GroupID: 20, Version: 1, IdentityGeneration: 2, Fingerprint: "test-2", Status: state.CredentialStatusActive, WeightAuto: 2,
		EncryptedValue: "cipher-two",
	}}); err != nil {
		t.Fatalf("replace loaded registry: %v", err)
	}
	loadedStats := health.NewStatsStore()
	loader := NewFileRuntimeStateCheckpoint(dataDir, loadedRegistry, loadedStats)
	if err := loader.Restore(context.Background()); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("checkpoint file still exists after Restore(), stat error = %v", err)
	}

	entry := loadedRegistry.Snapshot()[0]
	if entry.ID != 1 || !entry.CooldownUntil.Equal(time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)) ||
		!entry.Blacklisted || entry.FailureCount != 1 || entry.WeightAuto != 37 {
		t.Fatalf("restored key runtime state = %#v", entry)
	}
	gotStats := loadedStats.Snapshot(1, time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC))
	if gotStats.Failure != 1 || gotStats.Problem != 1 || gotStats.LastStatusCode != 503 {
		t.Fatalf("restored key stats = %#v", gotStats)
	}
}

func TestFileRuntimeStateCheckpointReturnsErrorWhenDeleteFails(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, runtimeStateCheckpointFileName)
	raw, err := json.Marshal(runtimeStateCheckpointDocument{
		Credentials: []state.CredentialRuntimeCheckpoint{{ID: 1, GroupID: 10, WeightAuto: 37}},
	})
	if err != nil {
		t.Fatalf("marshal checkpoint fixture: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write checkpoint fixture: %v", err)
	}

	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 10, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: state.CredentialStatusActive, WeightAuto: 1,
		EncryptedValue: "cipher-one",
	}}); err != nil {
		t.Fatalf("replace registry: %v", err)
	}
	checkpoint := NewFileRuntimeStateCheckpoint(dataDir, registry, health.NewStatsStore())
	// The normal file implementation removes the file successfully. This test
	// documents that a failed removal must prevent applying stale data through
	// the injectable filesystem hook used by the implementation.
	checkpoint.removeFile = func(string) error { return os.ErrPermission }
	if err := checkpoint.Restore(context.Background()); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("Restore() error = %v, want permission error", err)
	}
	if got := registry.Snapshot()[0].WeightAuto; got != 1 {
		t.Fatalf("weight after failed checkpoint removal = %d, want 1", got)
	}
}

func TestFileRuntimeStateCheckpointConsumesMalformedFileAndReturnsError(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, runtimeStateCheckpointFileName)
	if err := os.WriteFile(path, []byte(`{"credentials":[`), 0o600); err != nil {
		t.Fatalf("write malformed checkpoint fixture: %v", err)
	}
	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 10, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: state.CredentialStatusActive, WeightAuto: 1,
		EncryptedValue: "cipher-one",
	}}); err != nil {
		t.Fatalf("replace registry: %v", err)
	}
	checkpoint := NewFileRuntimeStateCheckpoint(dataDir, registry, health.NewStatsStore())
	if err := checkpoint.Restore(context.Background()); err == nil {
		t.Fatal("Restore() error = nil, want malformed checkpoint error")
	}
	if got := registry.Snapshot()[0].WeightAuto; got != 1 {
		t.Fatalf("weight after malformed checkpoint = %d, want 1", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("malformed checkpoint file was not consumed, stat error = %v", err)
	}
}
