package requestlog

import (
	"context"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
)

type staticRetentionPolicy struct{ days int }

func (policy staticRetentionPolicy) RequestLogRetentionDays() int {
	return policy.days
}

func newRequestLogTestService(db *gorm.DB) *Service {
	return NewService(
		db,
		redact.New(),
		staticRetentionPolicy{days: 7},
		newStaticPriceTableProvider(),
	)
}

type staticPriceTableProvider struct {
	table *pricing.Table
}

func (provider *staticPriceTableProvider) Load() *pricing.Table {
	if provider == nil {
		return nil
	}
	return provider.table
}

func newStaticPriceTableProvider() *staticPriceTableProvider {
	table, err := pricing.Compile(pricing.BuiltinRules())
	if err != nil {
		panic(err)
	}
	return &staticPriceTableProvider{table: table}
}

type batchWriterFunc func(context.Context, []models.RequestLog) error

func (fn batchWriterFunc) WriteBatch(ctx context.Context, rows []models.RequestLog) error {
	return fn(ctx, rows)
}

type manualTimer struct {
	ch   chan time.Time
	once sync.Once
}

func newManualTimer() *manualTimer {
	return &manualTimer{ch: make(chan time.Time, 1)}
}

func (timer *manualTimer) C() <-chan time.Time {
	return timer.ch
}

func (timer *manualTimer) Stop() bool {
	stopped := false
	timer.once.Do(func() {
		stopped = true
	})
	return stopped
}

func (timer *manualTimer) Fire() {
	timer.ch <- time.Unix(1, 0)
}

type manualTimerFactory struct {
	created chan *manualTimer
}

func newManualTimerFactory() *manualTimerFactory {
	return &manualTimerFactory{created: make(chan *manualTimer, 32)}
}

func (factory *manualTimerFactory) New(time.Duration) workerTimer {
	timer := newManualTimer()
	factory.created <- timer
	return timer
}

func receiveValue[T any](t *testing.T, ch <-chan T) T {
	t.Helper()

	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for test coordination")
		var zero T
		return zero
	}
}

func waitGroupDone(t *testing.T, group *sync.WaitGroup) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	receiveValue(t, done)
}

func testEvent(id string) telemetry.RequestEvent {
	return telemetry.RequestEvent{
		RequestID:     id,
		CompletedAt:   time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
		AccessKeyID:   42,
		ClientModel:   "client-model",
		UpstreamModel: "upstream-model",
		Status:        telemetry.RequestStatusSuccess,
		StatusCode:    200,
		DurationMs:    25,
		Attempts: []telemetry.Attempt{{
			Sequence:        1,
			GroupID:         7,
			GroupName:       "primary",
			KeyID:           8,
			KeyMask:         "sk-...mask",
			UpstreamModel:   "upstream-model",
			StatusCode:      200,
			DurationMs:      20,
			FailureCategory: telemetry.FailureCategoryOK,
			Action:          telemetry.ActionTerminate,
		}},
	}
}
