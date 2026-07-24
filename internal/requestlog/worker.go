package requestlog

import (
	"context"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
)

type realWorkerTimer struct {
	timer *time.Timer
}

func (timer *realWorkerTimer) C() <-chan time.Time {
	return timer.timer.C
}

func (timer *realWorkerTimer) Stop() bool {
	return timer.timer.Stop()
}

type gormBatchWriter struct {
	db *gorm.DB
}

func (writer *gormBatchWriter) WriteBatch(ctx context.Context, rows []models.RequestLog) error {
	return writer.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.CreateInBatches(rows, batchSize).Error
	})
}

func (service *Service) runWorker(ctx context.Context, done chan<- struct{}) {
	defer close(done)

	for {
		select {
		case <-ctx.Done():
			service.dropUnattempted(nil)
			return
		case <-service.stopRequested:
			service.drain(ctx, nil)
			return
		case event := <-service.queue:
			if !service.collectAndWrite(ctx, event) {
				return
			}
		}
	}
}

func (service *Service) collectAndWrite(ctx context.Context, first telemetry.RequestEvent) bool {
	batch := make([]telemetry.RequestEvent, 1, batchSize)
	batch[0] = first
	timer := service.timerFactory(flushDelay)

	for len(batch) < batchSize {
		select {
		case <-ctx.Done():
			timer.Stop()
			service.dropUnattempted(batch)
			return false
		case <-service.stopRequested:
			timer.Stop()
			service.drain(ctx, batch)
			return false
		case event := <-service.queue:
			batch = append(batch, event)
		case <-timer.C():
			service.writeBatch(ctx, batch)
			return true
		}
	}

	timer.Stop()
	if ctx.Err() != nil {
		service.dropUnattempted(batch)
		return false
	}
	service.writeBatch(ctx, batch)
	return true
}

func (service *Service) drain(ctx context.Context, batch []telemetry.RequestEvent) {
	for {
		if ctx.Err() != nil {
			service.dropUnattempted(batch)
			return
		}
		if len(batch) == batchSize {
			service.writeBatch(ctx, batch)
			batch = nil
			continue
		}

		select {
		case event := <-service.queue:
			batch = append(batch, event)
		default:
			if len(batch) > 0 {
				service.writeBatch(ctx, batch)
			}
			return
		}
	}
}

func (service *Service) writeBatch(ctx context.Context, events []telemetry.RequestEvent) {
	rows := make([]models.RequestLog, len(events))
	for index, event := range events {
		rows[index] = mapEvent(service.redactor, event)
	}

	if err := service.writer.WriteBatch(ctx, rows); err != nil {
		service.writeFailureTotal.Add(1)
		service.droppedPersistFailedTotal.Add(uint64(len(events)))
		service.statsMu.Lock()
		service.lastWriteFailureAt = service.now().UTC()
		service.statsMu.Unlock()
		service.warn("write_failure", len(events))
		return
	}
	service.persistedTotal.Add(uint64(len(events)))
}

func (service *Service) dropUnattempted(batch []telemetry.RequestEvent) {
	dropped := uint64(len(batch))
	for {
		select {
		case <-service.queue:
			dropped++
		default:
			service.droppedShutdownTotal.Add(dropped)
			return
		}
	}
}
