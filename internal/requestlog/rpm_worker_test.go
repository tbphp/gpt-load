package requestlog

import (
	"context"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/rpm"
)

func TestRPMRecordingDoesNotWaitForPersistence(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	store := rpm.NewStore()
	service.SetRPMStore(store)
	timers := newManualTimerFactory()
	service.timerFactory = timers.New
	started, release := make(chan struct{}), make(chan struct{})
	var announced, released sync.Once
	defer released.Do(func() { close(release) })
	if err := db.Callback().Create().Before("gorm:create").Register("test:rpm_blocked_write", func(tx *gorm.DB) {
		if tx.Statement.Table == "rpm_stats" {
			announced.Do(func() { close(started) })
			<-release
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		released.Do(func() { close(release) })
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := service.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()
	store.Record(rpm.AccessKey, 1, false, time.Now())
	receiveValue(t, timers.created).Fire()
	receiveValue(t, started)
	recorded := make(chan struct{})
	go func() { store.Record(rpm.AccessKey, 1, true, time.Now()); close(recorded) }()
	select {
	case <-recorded:
	case <-time.After(time.Second):
		t.Fatal("request counter waited for database I/O")
	}
	if current := store.Current(rpm.AccessKey, 1, time.Now()); current.Requests != 2 || current.Rejected != 1 {
		t.Fatalf("in-memory observation stalled: %+v", current)
	}
}
