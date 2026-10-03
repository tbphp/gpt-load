package control

import (
	"context"
	"sync"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/health"
	"gpt-load/internal/state"
)

type waitingValidationCoordinator struct {
	delegate *health.MutationCoordinator
	entered  chan struct{}
}

func (c *waitingValidationCoordinator) Do(id uint, fn func()) {
	close(c.entered)
	c.delegate.Do(id, fn)
}

func TestValidationWaitingForMutationDoesNotBlockPublication(t *testing.T) {
	manager := state.NewManager()
	if _, err := manager.Publish(validationManagerCompileInput("https://upstream.example.com")); err != nil {
		t.Fatal(err)
	}
	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 7, GroupID: 1, Version: 1, IdentityGeneration: 7, Fingerprint: "test-7",
		Status: state.CredentialStatusActive, Blacklisted: true, FailureCount: 3, EncryptedValue: "key-7",
	}}); err != nil {
		t.Fatal(err)
	}
	coordinator := health.NewMutationCoordinator()
	held := make(chan struct{})
	release := make(chan struct{})
	lockDone := make(chan struct{})
	var releaseOnce sync.Once
	unlock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unlock()
	go func() {
		coordinator.Do(7, func() {
			close(held)
			<-release
		})
		close(lockDone)
	}()
	awaitSignal(t, held)
	waiting := &waitingValidationCoordinator{delegate: coordinator, entered: make(chan struct{})}
	worker := &validationWorker{
		snapshots: manager, registry: registry, stats: health.NewStatsStore(), mutations: waiting,
		decryptor: validationDecryptor{}, channels: channel.NewRegistry(),
		executor: &validationTestExecutor{probes: &validationProbeRecorder{}},
	}
	validationDone := make(chan struct{})
	go func() {
		worker.Validate(context.Background())
		close(validationDone)
	}()
	awaitSignal(t, waiting.entered)
	publishDone := make(chan error, 1)
	go func() {
		_, err := manager.Publish(validationManagerCompileInput("https://changed.example.com"))
		publishDone <- err
	}()
	blocked := false
	select {
	case err := <-publishDone:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		blocked = true
	}
	unlock()
	awaitSignal(t, lockDone)
	awaitValidationDone(t, validationDone)
	if blocked {
		if err := awaitValue(t, publishDone); err != nil {
			t.Error(err)
		}
		t.Fatal("validation held publication lock while waiting for credential mutation")
	}
	if refs := registry.BlacklistedCredentials(); len(refs) != 1 {
		t.Fatalf("validation recovered with a stale target signature: %+v", refs)
	}
}
