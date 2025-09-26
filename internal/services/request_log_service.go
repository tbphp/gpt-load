package services

import (
	"context"
	"encoding/json"
	"fmt"
	"gpt-load/internal/config"
	"gpt-load/internal/models"
	"gpt-load/internal/store"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	RequestLogCachePrefix    = "request_log:"
	PendingLogKeysSet        = "pending_log_keys"
	DefaultLogFlushBatchSize = 200
)

// RequestLogService is responsible for managing request logs.
type RequestLogService struct {
	db              *gorm.DB
	store           store.Store
	settingsManager *config.SystemSettingsManager
	stopChan        chan struct{}
	wg              sync.WaitGroup
	ticker          *time.Ticker
}

// NewRequestLogService creates a new RequestLogService instance
func NewRequestLogService(db *gorm.DB, store store.Store, sm *config.SystemSettingsManager) *RequestLogService {
	return &RequestLogService{
		db:              db,
		store:           store,
		settingsManager: sm,
		stopChan:        make(chan struct{}),
	}
}

// Start initializes the service and starts the periodic flush routine
func (s *RequestLogService) Start() {
	s.wg.Add(1)
	go s.runLoop()
}

func (s *RequestLogService) runLoop() {
	defer s.wg.Done()

	// Initial flush on start
	s.flush()

	interval := time.Duration(s.settingsManager.GetSettings().RequestLogWriteIntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = time.Minute
	}
	s.ticker = time.NewTicker(interval)
	defer s.ticker.Stop()

	for {
		select {
		case <-s.ticker.C:
			newInterval := time.Duration(s.settingsManager.GetSettings().RequestLogWriteIntervalMinutes) * time.Minute
			if newInterval <= 0 {
				newInterval = time.Minute
			}
			if newInterval != interval {
				s.ticker.Reset(newInterval)
				interval = newInterval
				logrus.Debugf("Request log write interval updated to: %v", interval)
			}
			s.flush()
		case <-s.stopChan:
			return
		}
	}
}

// Stop gracefully stops the RequestLogService
func (s *RequestLogService) Stop(ctx context.Context) {
	close(s.stopChan)

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.flush()
		logrus.Info("RequestLogService stopped gracefully.")
	case <-ctx.Done():
		logrus.Warn("RequestLogService stop timed out.")
	}
}

// Record logs a request to the database and cache
func (s *RequestLogService) Record(log *models.RequestLog) error {
	log.ID = uuid.NewString()
	log.Timestamp = time.Now()

	if s.settingsManager.GetSettings().RequestLogWriteIntervalMinutes == 0 {
		return s.writeLogsToDB([]*models.RequestLog{log})
	}

	cacheKey := RequestLogCachePrefix + log.ID

	logBytes, err := json.Marshal(log)
	if err != nil {
		return fmt.Errorf("failed to marshal request log: %w", err)
	}

	ttl := time.Duration(s.settingsManager.GetSettings().RequestLogWriteIntervalMinutes*5) * time.Minute
	if err := s.store.Set(cacheKey, logBytes, ttl); err != nil {
		return err
	}

	return s.store.SAdd(PendingLogKeysSet, cacheKey)
}

// flush data from cache to database
func (s *RequestLogService) flush() {
	if s.settingsManager.GetSettings().RequestLogWriteIntervalMinutes == 0 {
		logrus.Debug("Sync mode enabled, skipping scheduled log flush.")
		return
	}

	logrus.Debug("Master starting to flush request logs...")

	for {
		keys, err := s.store.SPopN(PendingLogKeysSet, DefaultLogFlushBatchSize)
		if err != nil {
			logrus.Errorf("Failed to pop pending log keys from store: %v", err)
			return
		}

		if len(keys) == 0 {
			return
		}

		logrus.Debugf("Popped %d request logs to flush.", len(keys))

		var logs []*models.RequestLog
		var processedKeys []string
		for _, key := range keys {
			logBytes, err := s.store.Get(key)
			if err != nil {
				if err == store.ErrNotFound {
					logrus.Warnf("Log key %s found in set but not in store, skipping.", key)
				} else {
					logrus.Warnf("Failed to get log for key %s: %v", key, err)
				}
				continue
			}
			var log models.RequestLog
			if err := json.Unmarshal(logBytes, &log); err != nil {
				logrus.Warnf("Failed to unmarshal log for key %s: %v", key, err)
				continue
			}
			logs = append(logs, &log)
			processedKeys = append(processedKeys, key)
		}

		if len(logs) == 0 {
			continue
		}

		err = s.writeLogsToDB(logs)

		if err != nil {
			logrus.Errorf("Failed to flush request logs batch, will retry next time. Error: %v", err)
			if len(keys) > 0 {
				keysToRetry := make([]any, len(keys))
				for i, k := range keys {
					keysToRetry[i] = k
				}
				if saddErr := s.store.SAdd(PendingLogKeysSet, keysToRetry...); saddErr != nil {
					logrus.Errorf("CRITICAL: Failed to re-add failed log keys to set: %v", saddErr)
				}
			}
			return
		}

		if len(processedKeys) > 0 {
			if err := s.store.Del(processedKeys...); err != nil {
				logrus.Errorf("Failed to delete flushed log bodies from store: %v", err)
			}
		}
		logrus.Infof("Successfully flushed %d request logs.", len(logs))
	}
}

// writeLogsToDB writes a batch of request logs to the database
func (s *RequestLogService) writeLogsToDB(logs []*models.RequestLog) error {
	if len(logs) == 0 {
		return nil
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.CreateInBatches(logs, len(logs)).Error; err != nil {
			return fmt.Errorf("failed to batch insert request logs: %w", err)
		}

		keyStats := make(map[string]int64)
		for _, log := range logs {
			if log.IsSuccess && log.KeyHash != "" {
				keyStats[log.KeyHash]++
			}
		}

		if len(keyStats) > 0 {
			var caseStmt strings.Builder
			var keyHashes []string
			caseStmt.WriteString("CASE key_hash ")
			for keyHash, count := range keyStats {
				caseStmt.WriteString(fmt.Sprintf("WHEN '%s' THEN request_count + %d ", keyHash, count))
				keyHashes = append(keyHashes, keyHash)
			}
			caseStmt.WriteString("END")

			if err := tx.Model(&models.APIKey{}).Where("key_hash IN ?", keyHashes).
				Updates(map[string]any{
					"request_count": gorm.Expr(caseStmt.String()),
					"last_used_at":  time.Now(),
				}).Error; err != nil {
				return fmt.Errorf("failed to batch update api_key stats: %w", err)
			}
		}

		// 更新统计表
		hourlyStats := make(map[struct {
			Time    time.Time
			GroupID uint
		}]struct{ Success, Failure int64 })
		for _, log := range logs {
			if log.RequestType == models.RequestTypeRetry {
				continue
			}
			hourlyTime := log.Timestamp.Truncate(time.Hour)
			key := struct {
				Time    time.Time
				GroupID uint
			}{Time: hourlyTime, GroupID: log.GroupID}

			counts := hourlyStats[key]
			if log.IsSuccess {
				counts.Success++
			} else {
				counts.Failure++
			}
			hourlyStats[key] = counts

			if log.ParentGroupID > 0 {
				parentKey := struct {
					Time    time.Time
					GroupID uint
				}{Time: hourlyTime, GroupID: log.ParentGroupID}

				parentCounts := hourlyStats[parentKey]
				if log.IsSuccess {
					parentCounts.Success++
				} else {
					parentCounts.Failure++
				}
				hourlyStats[parentKey] = parentCounts
			}
		}

		if len(hourlyStats) > 0 {
			for key, counts := range hourlyStats {
				err := tx.Clauses(clause.OnConflict{
					Columns: []clause.Column{{Name: "time"}, {Name: "group_id"}},
					DoUpdates: clause.Assignments(map[string]any{
						"success_count": gorm.Expr("group_hourly_stats.success_count + ?", counts.Success),
						"failure_count": gorm.Expr("group_hourly_stats.failure_count + ?", counts.Failure),
						"updated_at":    time.Now(),
					}),
				}).Create(&models.GroupHourlyStat{
					Time:         key.Time,
					GroupID:      key.GroupID,
					SuccessCount: counts.Success,
					FailureCount: counts.Failure,
				}).Error

				if err != nil {
					return fmt.Errorf("failed to upsert group hourly stat: %w", err)
				}
			}
		}

		return nil
	})
}
