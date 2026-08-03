package requestlog

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/platform/epochms"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

const requestLogTransactionCleanupTimeout = time.Second

const absoluteUsageStatUpsert = `
INSERT INTO usage_stats (
	bucket_start_ms,
	access_key_id,
	group_id,
	model,
	request_count,
	success_count,
	failure_count,
	uncached_input_tokens,
	output_tokens,
	cache_read_tokens,
	cache_write_5m_tokens,
	cache_write_1h_tokens,
	cache_write_unknown_tokens,
	estimated_cost_nano_usd,
	usage_missing_count,
	partial_count,
	unpriced_request_count,
	pricing_partial_count
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(bucket_start_ms, access_key_id, group_id, model) DO UPDATE SET
	request_count = excluded.request_count,
	success_count = excluded.success_count,
	failure_count = excluded.failure_count,
	uncached_input_tokens = excluded.uncached_input_tokens,
	output_tokens = excluded.output_tokens,
	cache_read_tokens = excluded.cache_read_tokens,
	cache_write_5m_tokens = excluded.cache_write_5m_tokens,
	cache_write_1h_tokens = excluded.cache_write_1h_tokens,
	cache_write_unknown_tokens = excluded.cache_write_unknown_tokens,
	estimated_cost_nano_usd = excluded.estimated_cost_nano_usd,
	usage_missing_count = excluded.usage_missing_count,
	partial_count = excluded.partial_count,
	unpriced_request_count = excluded.unpriced_request_count,
	pricing_partial_count = excluded.pricing_partial_count
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
	BucketStartMS int64
	AccessKeyID   uint
	GroupID       uint
	Model         string
}

type usageStatDelta struct {
	RequestCount            int64
	SuccessCount            int64
	FailureCount            int64
	UncachedInputTokens     int64
	OutputTokens            int64
	CacheReadTokens         int64
	CacheWrite5MTokens      int64
	CacheWrite1HTokens      int64
	CacheWriteUnknownTokens int64
	EstimatedCostNanoUSD    int64
	UsageMissingCount       int64
	PartialCount            int64
	UnpricedRequestCount    int64
	PricingPartialCount     int64
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
		stat.BucketStartMS = key.BucketStartMS
		stat.AccessKeyID = key.AccessKeyID
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
			stat.BucketStartMS,
			stat.AccessKeyID,
			stat.GroupID,
			stat.Model,
			stat.RequestCount,
			stat.SuccessCount,
			stat.FailureCount,
			stat.UncachedInputTokens,
			stat.OutputTokens,
			stat.CacheReadTokens,
			stat.CacheWrite5MTokens,
			stat.CacheWrite1HTokens,
			stat.CacheWriteUnknownTokens,
			stat.EstimatedCostNanoUSD,
			stat.UsageMissingCount,
			stat.PartialCount,
			stat.UnpricedRequestCount,
			stat.PricingPartialCount,
		).Error; err != nil {
			return fmt.Errorf("upsert usage stat: %w", err)
		}
	}
	return nil
}

func buildUsageStatDeltas(rows []models.RequestLog) (map[usageStatKey]usageStatDelta, error) {
	deltas := make(map[usageStatKey]usageStatDelta)
	for _, row := range rows {
		bucketStartMS, err := epochms.AlignDown(
			row.CompletedAtMS,
			epochms.MillisecondsPerHour,
		)
		if err != nil {
			return nil, fmt.Errorf("aggregate request log %q completion time: %w", row.ID, err)
		}
		key := usageStatKey{
			BucketStartMS: bucketStartMS,
			AccessKeyID:   row.AccessKeyID,
			GroupID:       row.GroupID,
			Model:         row.UpstreamModel,
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
	switch row.Status {
	case string(telemetry.RequestStatusSuccess):
		if err := checkedInt64Add(&delta.SuccessCount, 1, "success_count"); err != nil {
			return err
		}
	case string(telemetry.RequestStatusError),
		string(telemetry.RequestStatusIncomplete),
		string(telemetry.RequestStatusCanceled):
		if err := checkedInt64Add(&delta.FailureCount, 1, "failure_count"); err != nil {
			return err
		}
	default:
		return fmt.Errorf("aggregate request log %q: invalid status %q", row.ID, row.Status)
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
	case string(usage.StateComplete), string(usage.StateNotApplicable):
	default:
		return fmt.Errorf("aggregate request log %q: invalid usage state %q", row.ID, row.UsageState)
	}
	if (row.UsageState == string(usage.StateComplete) ||
		row.UsageState == string(usage.StatePartial)) &&
		row.CostState == string(pricing.CostStateUnpriced) {
		if err := checkedInt64Add(&delta.UnpricedRequestCount, 1, "unpriced_request_count"); err != nil {
			return err
		}
	}
	if row.CostState == string(pricing.CostStatePriced) &&
		row.PricingCompleteness == string(pricing.CompletenessPartial) {
		if err := checkedInt64Add(&delta.PricingPartialCount, 1, "pricing_partial_count"); err != nil {
			return err
		}
	}
	if err := validatePersistedPricingState(row); err != nil {
		return fmt.Errorf("aggregate request log %q: %w", row.ID, err)
	}

	if row.UsageState != string(usage.StateComplete) &&
		row.UsageState != string(usage.StatePartial) {
		return nil
	}
	for _, field := range []struct {
		name   string
		target *int64
		value  int64
	}{
		{name: "uncached_input_tokens", target: &delta.UncachedInputTokens, value: row.UncachedInputTokens},
		{name: "output_tokens", target: &delta.OutputTokens, value: row.OutputTokens},
		{name: "cache_read_tokens", target: &delta.CacheReadTokens, value: row.CacheReadTokens},
		{name: "cache_write_5m_tokens", target: &delta.CacheWrite5MTokens, value: row.CacheWrite5MTokens},
		{name: "cache_write_1h_tokens", target: &delta.CacheWrite1HTokens, value: row.CacheWrite1HTokens},
		{name: "cache_write_unknown_tokens", target: &delta.CacheWriteUnknownTokens, value: row.CacheWriteUnknownTokens},
	} {
		if err := checkedInt64Add(field.target, field.value, field.name); err != nil {
			return err
		}
	}
	if row.CostState != string(pricing.CostStatePriced) {
		return nil
	}
	cost, ok := pricing.CheckedAddNanoUSD(
		pricing.NanoUSD(delta.EstimatedCostNanoUSD),
		pricing.NanoUSD(row.EstimatedCostNanoUSD),
	)
	if !ok {
		return fmt.Errorf("aggregate usage stat estimated_cost_nano_usd: checked addition failed")
	}
	delta.EstimatedCostNanoUSD = int64(cost)
	return nil
}

func validatePersistedPricingState(row models.RequestLog) error {
	for _, value := range [...]int64{
		row.UncachedInputTokens,
		row.OutputTokens,
		row.CacheReadTokens,
		row.CacheWrite5MTokens,
		row.CacheWrite1HTokens,
		row.CacheWriteUnknownTokens,
	} {
		if value < 0 {
			return fmt.Errorf("negative token value")
		}
	}
	if _, ok := usage.CheckedTotal(usage.Tokens{
		UncachedInput:     row.UncachedInputTokens,
		Output:            row.OutputTokens,
		CacheRead:         row.CacheReadTokens,
		CacheWrite5M:      row.CacheWrite5MTokens,
		CacheWrite1H:      row.CacheWrite1HTokens,
		CacheWriteUnknown: row.CacheWriteUnknownTokens,
	}); !ok {
		return fmt.Errorf("token total overflows int64")
	}
	return validateFrozenPricingState(
		usage.State(row.UsageState),
		pricing.CostState(row.CostState),
		pricing.Completeness(row.PricingCompleteness),
		row.EstimatedCostNanoUSD,
	)
}

func checkedInt64Add(target *int64, value int64, field string) error {
	total, ok := usage.CheckedAdd(*target, value)
	if !ok {
		return fmt.Errorf("aggregate usage stat %s: checked addition failed", field)
	}
	*target = total
	return nil
}

func sortedUsageStatKeys(deltas map[usageStatKey]usageStatDelta) []usageStatKey {
	keys := make([]usageStatKey, 0, len(deltas))
	for key := range deltas {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].BucketStartMS != keys[right].BucketStartMS {
			return keys[left].BucketStartMS < keys[right].BucketStartMS
		}
		if keys[left].AccessKeyID != keys[right].AccessKeyID {
			return keys[left].AccessKeyID < keys[right].AccessKeyID
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
				"bucket_start_ms = ? AND access_key_id = ? AND group_id = ? AND model = ?",
				key.BucketStartMS,
				key.AccessKeyID,
				key.GroupID,
				key.Model,
			)
			continue
		}
		query = query.Or(
			"bucket_start_ms = ? AND access_key_id = ? AND group_id = ? AND model = ?",
			key.BucketStartMS,
			key.AccessKeyID,
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
			BucketStartMS: row.BucketStartMS,
			AccessKeyID:   row.AccessKeyID,
			GroupID:       row.GroupID,
			Model:         row.Model,
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
		{name: "uncached_input_tokens", left: existing.UncachedInputTokens, right: delta.UncachedInputTokens, set: func(value int64) { total.UncachedInputTokens = value }},
		{name: "output_tokens", left: existing.OutputTokens, right: delta.OutputTokens, set: func(value int64) { total.OutputTokens = value }},
		{name: "cache_read_tokens", left: existing.CacheReadTokens, right: delta.CacheReadTokens, set: func(value int64) { total.CacheReadTokens = value }},
		{name: "cache_write_5m_tokens", left: existing.CacheWrite5MTokens, right: delta.CacheWrite5MTokens, set: func(value int64) { total.CacheWrite5MTokens = value }},
		{name: "cache_write_1h_tokens", left: existing.CacheWrite1HTokens, right: delta.CacheWrite1HTokens, set: func(value int64) { total.CacheWrite1HTokens = value }},
		{name: "cache_write_unknown_tokens", left: existing.CacheWriteUnknownTokens, right: delta.CacheWriteUnknownTokens, set: func(value int64) { total.CacheWriteUnknownTokens = value }},
		{name: "usage_missing_count", left: existing.UsageMissingCount, right: delta.UsageMissingCount, set: func(value int64) { total.UsageMissingCount = value }},
		{name: "partial_count", left: existing.PartialCount, right: delta.PartialCount, set: func(value int64) { total.PartialCount = value }},
		{name: "unpriced_request_count", left: existing.UnpricedRequestCount, right: delta.UnpricedRequestCount, set: func(value int64) { total.UnpricedRequestCount = value }},
		{name: "pricing_partial_count", left: existing.PricingPartialCount, right: delta.PricingPartialCount, set: func(value int64) { total.PricingPartialCount = value }},
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
	cost, ok := pricing.CheckedAddNanoUSD(
		pricing.NanoUSD(existing.EstimatedCostNanoUSD),
		pricing.NanoUSD(delta.EstimatedCostNanoUSD),
	)
	if !ok {
		return models.UsageStat{}, fmt.Errorf(
			"calculate absolute usage stat estimated_cost_nano_usd: checked addition failed",
		)
	}
	total.EstimatedCostNanoUSD = int64(cost)
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
	rows := make([]models.RequestLog, 0, len(events))
	projectionFailures := 0
	for _, event := range events {
		row, err := mapEvent(service.redactor, event.Event)
		if err != nil {
			projectionFailures++
			continue
		}
		rows = append(rows, row)
	}
	if projectionFailures > 0 {
		service.recordPersistFailure("projection_failure", projectionFailures)
	}
	if len(rows) == 0 {
		return
	}

	if err := service.writer.WriteBatch(ctx, rows); err != nil {
		service.recordPersistFailure("write_failure", len(rows))
		return
	}
	service.persistedTotal.Add(uint64(len(rows)))
}

func (service *Service) recordPersistFailure(reason string, count int) {
	service.writeFailureTotal.Add(1)
	service.droppedPersistFailedTotal.Add(uint64(count))
	service.statsMu.Lock()
	service.lastWriteFailureAt = service.now().UTC()
	service.statsMu.Unlock()
	service.warn(reason, count)
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
