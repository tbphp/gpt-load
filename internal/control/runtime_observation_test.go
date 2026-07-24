package control

import (
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/requestlog"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestCaptureRuntimeObservationWaitsForPublishedConfigPair(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("runtime-observation")
	var key models.UpstreamKey
	runtimeApplied := make(chan struct{})
	allowPublish := make(chan struct{})
	var releaseOnce sync.Once
	releasePublish := func() {
		releaseOnce.Do(func() { close(allowPublish) })
	}
	defer releasePublish()
	writeDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.writeConfig(t.Context(), func(tx *gorm.DB) error {
			if err := tx.Create(group).Error; err != nil {
				return err
			}
			key = models.UpstreamKey{
				GroupID: group.ID, KeyValue: "cipher-runtime-observation",
				KeyHash: "hash-runtime-observation",
				Status:  models.UpstreamKeyStatusActive,
			}
			return tx.Create(&key).Error
		}, func() error {
			if err := fixture.registry.ApplyImport(group.ID, []state.KeyEntry{{
				ID: key.ID, GroupID: group.ID, Status: state.KeyStatusActive,
				EncryptedValue: key.KeyValue,
			}}); err != nil {
				return err
			}
			close(runtimeApplied)
			<-allowPublish
			return nil
		})
		writeDone <- err
	}()

	select {
	case <-runtimeApplied:
	case <-time.After(2 * time.Second):
		t.Fatal("Registry update barrier timed out")
	}
	if fixture.service.writeMu.TryRLock() {
		fixture.service.writeMu.RUnlock()
		t.Fatal("configuration writer did not retain writeMu before Publish")
	}
	captureStarted := make(chan struct{})
	captureDone := make(chan struct {
		value runtimeObservation
		err   error
	}, 1)
	go func() {
		close(captureStarted)
		value, err := fixture.service.captureRuntimeObservation()
		captureDone <- struct {
			value runtimeObservation
			err   error
		}{value: value, err: err}
	}()
	<-captureStarted
	select {
	case result := <-captureDone:
		t.Fatalf("capture completed inside Registry/Snapshot gap: %#v", result)
	case <-time.After(50 * time.Millisecond):
	}
	releasePublish()
	if err := <-writeDone; err != nil {
		t.Fatalf("writeConfig() error = %v", err)
	}
	result := <-captureDone
	if result.err != nil {
		t.Fatalf("captureRuntimeObservation() error = %v", result.err)
	}
	if _, exists := result.value.snapshot.GroupCatalog[group.ID]; !exists {
		t.Fatalf("captured Snapshot missing Group %d", group.ID)
	}
	found := false
	for _, view := range result.value.keys {
		if view.ID == key.ID && view.GroupID == group.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("captured keys = %#v, want key %d", result.value.keys, key.ID)
	}
}

func TestRuntimeHealthReleasesReadLockBeforeRequestLogMapping(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.requestLogStats.fn = func() requestlog.Stats {
		if !fixture.service.writeMu.TryLock() {
			t.Fatal("writeMu remained read-locked during RequestLog Stats mapping")
		}
		fixture.service.writeMu.Unlock()
		return requestlog.Stats{}
	}
	if _, err := fixture.service.RuntimeHealth(); err != nil {
		t.Fatalf("RuntimeHealth() error = %v", err)
	}
}
