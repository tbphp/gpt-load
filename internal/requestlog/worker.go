package requestlog

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

const requestLogTransactionCleanupTimeout = time.Second

const absoluteUsageStatUpsert = `
INSERT INTO usage_stats (
	hour_bucket,
	group_id,
	model,
	request_count,
	success_count,
	failure_count,
	input_tokens,
	output_tokens,
	cache_read_tokens,
	cache_write_5m_tokens,
	cache_write_1h_tokens,
	cost,
	usage_missing_count,
	partial_count,
	unpriced_request_count
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(hour_bucket, group_id, model) DO UPDATE SET
	request_count = excluded.request_count,
	success_count = excluded.success_count,
	failure_count = excluded.failure_count,
	input_tokens = excluded.input_tokens,
	output_tokens = excluded.output_tokens,
	cache_read_tokens = excluded.cache_read_tokens,
	cache_write_5m_tokens = excluded.cache_write_5m_tokens,
	cache_write_1h_tokens = excluded.cache_write_1h_tokens,
	cost = excluded.cost,
	usage_missing_count = excluded.usage_missing_count,
	partial_count = excluded.partial_count,
	unpriced_request_count = excluded.unpriced_request_count
`

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
	if writer == nil || writer.db == nil {
		return fmt.Errorf("write request log batch: database is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	return writer.db.WithContext(ctx).Connection(func(connection *gorm.DB) error {
		sqlConn, ok := connection.Statement.ConnPool.(*sql.Conn)
		if !ok {
			return fmt.Errorf("pin request log transaction connection")
		}
		transaction := connection.Session(&gorm.Session{
			NewDB: true, SkipDefaultTransaction: true, Context: ctx,
		})
		if err := transaction.Exec("BEGIN IMMEDIATE").Error; err != nil {
			return fmt.Errorf("begin request log transaction: %w", err)
		}

		active := true
		defer func() {
			if active {
				_ = rollbackRequestLogTransaction(connection, sqlConn, false)
			}
		}()
		if err := writeRequestLogBatch(transaction, rows); err != nil {
			cleanupErr := rollbackRequestLogTransaction(connection, sqlConn, false)
			active = false
			return errors.Join(err, cleanupErr)
		}
		if err := transaction.Exec("COMMIT").Error; err != nil {
			commitErr := fmt.Errorf("commit request log transaction: %w", err)
			cleanupErr := rollbackRequestLogTransaction(connection, sqlConn, true)
			active = false
			return errors.Join(commitErr, cleanupErr)
		}
		active = false
		return nil
	})
}

type usageStatKey struct {
	HourBucket time.Time
	GroupID    uint
	Model      string
}

type usageStatDelta struct {
	RequestCount         int64
	SuccessCount         int64
	FailureCount         int64
	InputTokens          int64
	OutputTokens         int64
	CacheReadTokens      int64
	CacheWrite5MTokens   int64
	CacheWrite1HTokens   int64
	Cost                 float64
	UsageMissingCount    int64
	PartialCount         int64
	UnpricedRequestCount int64
}

func writeRequestLogBatch(tx *gorm.DB, rows []models.RequestLog) error {
	uniqueRows := make([]models.RequestLog, 0, len(rows))
	ids := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, exists := seen[row.ID]; exists {
			continue
		}
		seen[row.ID] = struct{}{}
		uniqueRows = append(uniqueRows, row)
		ids = append(ids, row.ID)
	}
	if len(uniqueRows) == 0 {
		return nil
	}

	var existingIDs []string
	if err := tx.Model(&models.RequestLog{}).
		Where("id IN ?", ids).
		Pluck("id", &existingIDs).Error; err != nil {
		return fmt.Errorf("query existing request log IDs: %w", err)
	}
	existingSet := make(map[string]struct{}, len(existingIDs))
	for _, id := range existingIDs {
		existingSet[id] = struct{}{}
	}
	newRows := make([]models.RequestLog, 0, len(uniqueRows)-len(existingIDs))
	for _, row := range uniqueRows {
		if _, exists := existingSet[row.ID]; !exists {
			newRows = append(newRows, row)
		}
	}
	if len(newRows) == 0 {
		return nil
	}
	if err := tx.CreateInBatches(newRows, batchSize).Error; err != nil {
		return fmt.Errorf("insert request logs: %w", err)
	}

	deltas, err := buildUsageStatDeltas(newRows)
	if err != nil {
		return err
	}
	if len(deltas) == 0 {
		return nil
	}
	keys := sortedUsageStatKeys(deltas)
	existingStats, err := queryExistingUsageStats(tx, keys)
	if err != nil {
		return err
	}

	absolute := make([]models.UsageStat, 0, len(keys))
	for _, key := range keys {
		stat := existingStats[key]
		stat.HourBucket = key.HourBucket
		stat.GroupID = key.GroupID
		stat.Model = key.Model
		total, err := checkedUsageStatTotal(stat, deltas[key])
		if err != nil {
			return err
		}
		absolute = append(absolute, total)
	}

	for _, stat := range absolute {
		if err := tx.Exec(
			absoluteUsageStatUpsert,
			stat.HourBucket,
			stat.GroupID,
			stat.Model,
			stat.RequestCount,
			stat.SuccessCount,
			stat.FailureCount,
			stat.InputTokens,
			stat.OutputTokens,
			stat.CacheReadTokens,
			stat.CacheWrite5MTokens,
			stat.CacheWrite1HTokens,
			stat.Cost,
			stat.UsageMissingCount,
			stat.PartialCount,
			stat.UnpricedRequestCount,
		).Error; err != nil {
			return fmt.Errorf("upsert usage stat: %w", err)
		}
	}
	return nil
}

func buildUsageStatDeltas(rows []models.RequestLog) (map[usageStatKey]usageStatDelta, error) {
	deltas := make(map[usageStatKey]usageStatDelta)
	for _, row := range rows {
		if row.GroupID == 0 || row.UpstreamModel == "" {
			continue
		}
		key := usageStatKey{
			HourBucket: row.CreatedAt.UTC().Truncate(time.Hour),
			GroupID:    row.GroupID,
			Model:      row.UpstreamModel,
		}
		delta := deltas[key]
		if err := delta.addRow(row); err != nil {
			return nil, err
		}
		deltas[key] = delta
	}
	return deltas, nil
}

func (delta *usageStatDelta) addRow(row models.RequestLog) error {
	if err := checkedInt64Add(&delta.RequestCount, 1, "request_count"); err != nil {
		return err
	}
	if row.Status == string(telemetry.RequestStatusSuccess) {
		if err := checkedInt64Add(&delta.SuccessCount, 1, "success_count"); err != nil {
			return err
		}
	} else if err := checkedInt64Add(&delta.FailureCount, 1, "failure_count"); err != nil {
		return err
	}

	switch row.UsageState {
	case string(usage.StateMissing):
		if err := checkedInt64Add(&delta.UsageMissingCount, 1, "usage_missing_count"); err != nil {
			return err
		}
	case string(usage.StatePartial):
		if err := checkedInt64Add(&delta.PartialCount, 1, "partial_count"); err != nil {
			return err
		}
	}
	if row.CostState == string(pricing.CostStateUnpriced) {
		if err := checkedInt64Add(&delta.UnpricedRequestCount, 1, "unpriced_request_count"); err != nil {
			return err
		}
	}

	if row.UsageState != string(usage.StateComplete) ||
		row.CostState != string(pricing.CostStatePriced) {
		return nil
	}
	for _, field := range []struct {
		name   string
		target *int64
		value  int64
	}{
		{name: "input_tokens", target: &delta.InputTokens, value: row.InputTokens},
		{name: "output_tokens", target: &delta.OutputTokens, value: row.OutputTokens},
		{name: "cache_read_tokens", target: &delta.CacheReadTokens, value: row.CacheReadTokens},
		{name: "cache_write_5m_tokens", target: &delta.CacheWrite5MTokens, value: row.CacheWrite5MTokens},
		{name: "cache_write_1h_tokens", target: &delta.CacheWrite1HTokens, value: row.CacheWrite1HTokens},
	} {
		if err := checkedInt64Add(field.target, field.value, field.name); err != nil {
			return err
		}
	}
	cost, ok := checkedCostAdd(delta.Cost, row.Cost)
	if !ok {
		return fmt.Errorf("aggregate usage stat cost: checked addition failed")
	}
	delta.Cost = cost
	return nil
}

func checkedInt64Add(target *int64, value int64, field string) error {
	total, ok := usage.CheckedAdd(*target, value)
	if !ok {
		return fmt.Errorf("aggregate usage stat %s: checked addition failed", field)
	}
	*target = total
	return nil
}

func checkedCostAdd(left, right float64) (float64, bool) {
	if left < 0 || right < 0 ||
		math.IsNaN(left) || math.IsNaN(right) ||
		math.IsInf(left, 0) || math.IsInf(right, 0) {
		return 0, false
	}
	total := left + right
	if total < 0 || math.IsNaN(total) || math.IsInf(total, 0) {
		return 0, false
	}
	return total, true
}

func sortedUsageStatKeys(deltas map[usageStatKey]usageStatDelta) []usageStatKey {
	keys := make([]usageStatKey, 0, len(deltas))
	for key := range deltas {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		if !keys[left].HourBucket.Equal(keys[right].HourBucket) {
			return keys[left].HourBucket.Before(keys[right].HourBucket)
		}
		if keys[left].GroupID != keys[right].GroupID {
			return keys[left].GroupID < keys[right].GroupID
		}
		return keys[left].Model < keys[right].Model
	})
	return keys
}

func queryExistingUsageStats(
	tx *gorm.DB,
	keys []usageStatKey,
) (map[usageStatKey]models.UsageStat, error) {
	query := tx.Model(&models.UsageStat{})
	for index, key := range keys {
		if index == 0 {
			query = query.Where(
				"hour_bucket = ? AND group_id = ? AND model = ?",
				key.HourBucket,
				key.GroupID,
				key.Model,
			)
			continue
		}
		query = query.Or(
			"hour_bucket = ? AND group_id = ? AND model = ?",
			key.HourBucket,
			key.GroupID,
			key.Model,
		)
	}
	var rows []models.UsageStat
	if err := query.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query existing usage stats: %w", err)
	}
	existing := make(map[usageStatKey]models.UsageStat, len(rows))
	for _, row := range rows {
		key := usageStatKey{
			HourBucket: row.HourBucket.UTC(),
			GroupID:    row.GroupID,
			Model:      row.Model,
		}
		existing[key] = row
	}
	return existing, nil
}

func checkedUsageStatTotal(
	existing models.UsageStat,
	delta usageStatDelta,
) (models.UsageStat, error) {
	total := existing
	for _, field := range []struct {
		name  string
		left  int64
		right int64
		set   func(int64)
	}{
		{name: "request_count", left: existing.RequestCount, right: delta.RequestCount, set: func(value int64) { total.RequestCount = value }},
		{name: "success_count", left: existing.SuccessCount, right: delta.SuccessCount, set: func(value int64) { total.SuccessCount = value }},
		{name: "failure_count", left: existing.FailureCount, right: delta.FailureCount, set: func(value int64) { total.FailureCount = value }},
		{name: "input_tokens", left: existing.InputTokens, right: delta.InputTokens, set: func(value int64) { total.InputTokens = value }},
		{name: "output_tokens", left: existing.OutputTokens, right: delta.OutputTokens, set: func(value int64) { total.OutputTokens = value }},
		{name: "cache_read_tokens", left: existing.CacheReadTokens, right: delta.CacheReadTokens, set: func(value int64) { total.CacheReadTokens = value }},
		{name: "cache_write_5m_tokens", left: existing.CacheWrite5MTokens, right: delta.CacheWrite5MTokens, set: func(value int64) { total.CacheWrite5MTokens = value }},
		{name: "cache_write_1h_tokens", left: existing.CacheWrite1HTokens, right: delta.CacheWrite1HTokens, set: func(value int64) { total.CacheWrite1HTokens = value }},
		{name: "usage_missing_count", left: existing.UsageMissingCount, right: delta.UsageMissingCount, set: func(value int64) { total.UsageMissingCount = value }},
		{name: "partial_count", left: existing.PartialCount, right: delta.PartialCount, set: func(value int64) { total.PartialCount = value }},
		{name: "unpriced_request_count", left: existing.UnpricedRequestCount, right: delta.UnpricedRequestCount, set: func(value int64) { total.UnpricedRequestCount = value }},
	} {
		value, ok := usage.CheckedAdd(field.left, field.right)
		if !ok {
			return models.UsageStat{}, fmt.Errorf(
				"calculate absolute usage stat %s: checked addition failed",
				field.name,
			)
		}
		field.set(value)
	}
	cost, ok := checkedCostAdd(existing.Cost, delta.Cost)
	if !ok {
		return models.UsageStat{}, fmt.Errorf(
			"calculate absolute usage stat cost: checked addition failed",
		)
	}
	total.Cost = cost
	return total, nil
}

func rollbackRequestLogTransaction(
	connection *gorm.DB,
	sqlConn *sql.Conn,
	discardAlways bool,
) error {
	cleanupCtx, cancel := context.WithTimeout(
		context.Background(),
		requestLogTransactionCleanupTimeout,
	)
	defer cancel()
	cleanupDB := connection.Session(&gorm.Session{
		NewDB: true, SkipDefaultTransaction: true, Context: cleanupCtx,
	})
	rollbackErr := cleanupDB.Exec("ROLLBACK").Error
	var discardErr error
	if rollbackErr != nil || discardAlways {
		discardErr = discardRequestLogConnection(sqlConn)
	}
	if rollbackErr != nil {
		rollbackErr = fmt.Errorf("rollback request log transaction: %w", rollbackErr)
	}
	return errors.Join(rollbackErr, discardErr)
}

func discardRequestLogConnection(sqlConn *sql.Conn) error {
	err := sqlConn.Raw(func(any) error { return driver.ErrBadConn })
	if err == nil || errors.Is(err, driver.ErrBadConn) {
		return nil
	}
	return fmt.Errorf("discard request log database connection: %w", err)
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

func (service *Service) collectAndWrite(ctx context.Context, first queuedEvent) bool {
	batch := make([]queuedEvent, 1, batchSize)
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

func (service *Service) drain(ctx context.Context, batch []queuedEvent) {
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

func (service *Service) writeBatch(ctx context.Context, events []queuedEvent) {
	rows := make([]models.RequestLog, len(events))
	for index, event := range events {
		rows[index] = mapEvent(service.redactor, event.Event, event.Prices)
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

func (service *Service) dropUnattempted(batch []queuedEvent) {
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
