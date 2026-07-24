package requestlog

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
)

const (
	queueCapacity = 4096
	batchSize     = 500
	flushDelay    = time.Second

	warningInterval = time.Minute
)

type batchWriter interface {
	WriteBatch(context.Context, []models.RequestLog) error
}

type workerTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type workerTimerFactory func(time.Duration) workerTimer

var _ telemetry.RequestLogSink = (*Service)(nil)

type Service struct {
	db           *gorm.DB
	queue        chan telemetry.RequestEvent
	writer       batchWriter
	redactor     *redact.Redactor
	timerFactory workerTimerFactory
	logger       *logrus.Logger
	now          func() time.Time
	startErr     error

	stateMu       sync.Mutex
	state         lifecycleState
	workerCancel  context.CancelFunc
	workerDone    chan struct{}
	stopRequested chan struct{}
	shutdownDone  chan struct{}
	shutdownErr   error

	enqueuedTotal             atomic.Uint64
	persistedTotal            atomic.Uint64
	droppedNotRunningTotal    atomic.Uint64
	droppedQueueFullTotal     atomic.Uint64
	droppedStoppingTotal      atomic.Uint64
	droppedPersistFailedTotal atomic.Uint64
	droppedShutdownTotal      atomic.Uint64
	writeFailureTotal         atomic.Uint64
	retentionInvalidTotal     atomic.Uint64
	retentionDeleteTotal      atomic.Uint64

	statsMu                sync.Mutex
	lastWriteFailureAt     time.Time
	lastRetentionFailureAt time.Time

	warningMu     sync.Mutex
	lastWarningAt time.Time
}

func NewService(db *gorm.DB, redactor *redact.Redactor) *Service {
	service := newService(
		&gormBatchWriter{db: db},
		redactor,
		func(delay time.Duration) workerTimer {
			return &realWorkerTimer{timer: time.NewTimer(delay)}
		},
	)
	service.db = db
	if db == nil {
		service.startErr = fmt.Errorf("request log database is nil")
	}
	return service
}

func newService(writer batchWriter, redactor *redact.Redactor, timerFactory workerTimerFactory) *Service {
	if redactor == nil {
		redactor = redact.New()
	}
	return &Service{
		queue:         make(chan telemetry.RequestEvent, queueCapacity),
		writer:        writer,
		redactor:      redactor,
		timerFactory:  timerFactory,
		logger:        logrus.StandardLogger(),
		now:           time.Now,
		state:         lifecycleNew,
		stopRequested: make(chan struct{}),
	}
}

func (service *Service) Start() error {
	service.stateMu.Lock()
	defer service.stateMu.Unlock()

	switch service.state {
	case lifecycleRunning:
		return ErrAlreadyStarted
	case lifecycleStopping, lifecycleStopped:
		return ErrNotRestartable
	}
	if service.startErr != nil {
		service.state = lifecycleStopped
		return fmt.Errorf("start request log service: %w", service.startErr)
	}
	if service.writer == nil || service.timerFactory == nil {
		service.state = lifecycleStopped
		return fmt.Errorf("start request log service: incomplete worker configuration")
	}

	workerContext, cancel := context.WithCancel(context.Background())
	service.workerCancel = cancel
	service.workerDone = make(chan struct{})
	service.state = lifecycleRunning
	go service.runWorker(workerContext, service.workerDone)
	return nil
}

func (service *Service) Emit(event telemetry.RequestEvent) {
	service.stateMu.Lock()
	switch service.state {
	case lifecycleNew:
		service.droppedNotRunningTotal.Add(1)
		service.stateMu.Unlock()
		return
	case lifecycleStopping, lifecycleStopped:
		service.droppedStoppingTotal.Add(1)
		service.stateMu.Unlock()
		return
	}

	cloned := cloneEvent(event)
	select {
	case service.queue <- cloned:
		service.enqueuedTotal.Add(1)
		service.stateMu.Unlock()
	default:
		service.droppedQueueFullTotal.Add(1)
		service.stateMu.Unlock()
		service.warn("queue_full", 0)
	}
}

func (service *Service) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	service.stateMu.Lock()
	switch service.state {
	case lifecycleNew:
		service.state = lifecycleStopped
		service.shutdownDone = make(chan struct{})
		close(service.shutdownDone)
		service.stateMu.Unlock()
		return nil
	case lifecycleRunning:
		service.state = lifecycleStopping
		close(service.stopRequested)
		service.shutdownDone = make(chan struct{})
		shutdownDone := service.shutdownDone
		workerDone := service.workerDone
		service.stateMu.Unlock()

		go service.finishShutdown(workerDone, shutdownDone)
		return service.waitForShutdown(ctx, shutdownDone)
	case lifecycleStopping:
		shutdownDone := service.shutdownDone
		service.stateMu.Unlock()
		return service.waitForShutdown(ctx, shutdownDone)
	case lifecycleStopped:
		err := service.shutdownErr
		service.stateMu.Unlock()
		return err
	default:
		service.stateMu.Unlock()
		return nil
	}
}

func (service *Service) Stats() Stats {
	service.statsMu.Lock()
	lastWriteFailureAt := service.lastWriteFailureAt
	lastRetentionFailureAt := service.lastRetentionFailureAt
	service.statsMu.Unlock()

	stats := Stats{
		EnqueuedTotal:                service.enqueuedTotal.Load(),
		PersistedTotal:               service.persistedTotal.Load(),
		DroppedNotRunningTotal:       service.droppedNotRunningTotal.Load(),
		DroppedQueueFullTotal:        service.droppedQueueFullTotal.Load(),
		DroppedStoppingTotal:         service.droppedStoppingTotal.Load(),
		DroppedPersistFailedTotal:    service.droppedPersistFailedTotal.Load(),
		DroppedShutdownTotal:         service.droppedShutdownTotal.Load(),
		WriteFailureTotal:            service.writeFailureTotal.Load(),
		RetentionInvalidSettingTotal: service.retentionInvalidTotal.Load(),
		RetentionDeleteFailureTotal:  service.retentionDeleteTotal.Load(),
		QueueDepth:                   len(service.queue),
		QueueCapacity:                cap(service.queue),
		LastWriteFailureAt:           lastWriteFailureAt,
		LastRetentionFailureAt:       lastRetentionFailureAt,
	}
	stats.DroppedTotal = stats.DroppedNotRunningTotal +
		stats.DroppedQueueFullTotal +
		stats.DroppedStoppingTotal +
		stats.DroppedPersistFailedTotal +
		stats.DroppedShutdownTotal
	return stats
}

func (service *Service) waitForShutdown(ctx context.Context, shutdownDone <-chan struct{}) error {
	select {
	case <-shutdownDone:
		return service.shutdownResult()
	default:
	}

	select {
	case <-shutdownDone:
		return service.shutdownResult()
	case <-ctx.Done():
		select {
		case <-shutdownDone:
			return service.shutdownResult()
		default:
			return service.cancelShutdown(ctx.Err())
		}
	}
}

func (service *Service) cancelShutdown(cause error) error {
	service.stateMu.Lock()
	if service.state != lifecycleStopping {
		err := service.shutdownErr
		service.stateMu.Unlock()
		return err
	}
	if service.shutdownErr == nil {
		service.shutdownErr = fmt.Errorf("stop request log service: %w", cause)
		cancel := service.workerCancel
		err := service.shutdownErr
		service.stateMu.Unlock()
		cancel()
		return err
	}
	err := service.shutdownErr
	service.stateMu.Unlock()
	return err
}

func (service *Service) finishShutdown(workerDone <-chan struct{}, shutdownDone chan struct{}) {
	<-workerDone

	service.stateMu.Lock()
	service.state = lifecycleStopped
	close(shutdownDone)
	service.stateMu.Unlock()
}

func (service *Service) shutdownResult() error {
	service.stateMu.Lock()
	defer service.stateMu.Unlock()
	return service.shutdownErr
}

func (service *Service) warn(failureType string, failedBatchSize int) {
	now := service.now()
	service.warningMu.Lock()
	if !service.lastWarningAt.IsZero() && now.Before(service.lastWarningAt.Add(warningInterval)) {
		service.warningMu.Unlock()
		return
	}
	service.lastWarningAt = now
	service.warningMu.Unlock()

	stats := service.Stats()
	service.logger.WithFields(logrus.Fields{
		"failure_type":        failureType,
		"batch_size":          failedBatchSize,
		"queue_depth":         stats.QueueDepth,
		"dropped_total":       stats.DroppedTotal,
		"write_failure_total": stats.WriteFailureTotal,
	}).Warn("Request log event loss")
}

func cloneEvent(event telemetry.RequestEvent) telemetry.RequestEvent {
	cloned := event
	if event.Attempts != nil {
		cloned.Attempts = append([]telemetry.Attempt(nil), event.Attempts...)
	}
	return cloned
}
