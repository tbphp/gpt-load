package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

const modelsDevCatalogCacheName = "models.dev.catalog.json"

const (
	modelsDevSyncInterval  = 24 * time.Hour
	modelsDevInitialRetry  = time.Hour
	modelsDevGroupDebounce = 500 * time.Millisecond
)

type CatalogSyncTrigger string

const (
	CatalogSyncStartup  CatalogSyncTrigger = "startup"
	CatalogSyncPeriodic CatalogSyncTrigger = "periodic"
	CatalogSyncGroup    CatalogSyncTrigger = "group_change"
	CatalogSyncSettings CatalogSyncTrigger = "settings_change"
	CatalogSyncManual   CatalogSyncTrigger = "manual"
)

type CatalogBootstrap struct {
	Runtime   *catalog.Runtime
	CachePath string
	Metadata  catalog.Metadata
	HasLKG    bool
}

func loadCatalogBootstrap(cachePath string) *CatalogBootstrap {
	bootstrap := &CatalogBootstrap{
		Runtime:   &catalog.Runtime{},
		CachePath: cachePath,
	}
	cached, err := catalog.LoadCache(cachePath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logrus.WithField("component", "models_dev_catalog").Warn(
				"Models.dev catalog cache is unavailable; starting with an empty catalog",
			)
		}
		logrus.WithFields(logrus.Fields{
			"event":   "startup.catalog_load",
			"outcome": "empty",
		}).Info("catalog runtime loaded")
		return bootstrap
	}
	bootstrap.Runtime.Publish(cached.Snapshot)
	bootstrap.Metadata = cached.Metadata
	bootstrap.HasLKG = true
	logrus.WithFields(logrus.Fields{
		"event":   "startup.catalog_load",
		"outcome": "last_known_good",
	}).Info("catalog runtime loaded")
	return bootstrap
}

func newCatalogBootstrap(dataDir string) *CatalogBootstrap {
	return loadCatalogBootstrap(filepath.Join(dataDir, modelsDevCatalogCacheName))
}

type catalogSyncClient interface {
	Sync(context.Context, catalog.Metadata) (catalog.SyncResult, error)
}

type CatalogSyncStatus struct {
	Trigger             CatalogSyncTrigger `json:"trigger"`
	CheckedAtMS         int64              `json:"checked_at_ms"`
	SuccessfulFetchAtMS int64              `json:"successful_fetch_at_ms"`
	NotModified         bool               `json:"not_modified"`
	Skipped             bool               `json:"skipped"`
	ErrorCode           string             `json:"error_code,omitempty"`
}

func (coordinator *CatalogSyncCoordinator) readStatus() CatalogSyncStatus {
	if coordinator == nil {
		return CatalogSyncStatus{}
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	status := coordinator.last
	if status.CheckedAtMS == 0 {
		status.CheckedAtMS = coordinator.metadata.CheckedAtMillis
	}
	if status.SuccessfulFetchAtMS == 0 {
		status.SuccessfulFetchAtMS = coordinator.metadata.SuccessfulFetchAtMillis
	}
	return status
}

type catalogSyncCall struct {
	done    chan struct{}
	cancel  context.CancelFunc
	status  CatalogSyncStatus
	err     error
	waiters int
}

type catalogSyncTimer interface {
	C() <-chan time.Time
	Stop()
}

type standardCatalogSyncTimer struct {
	timer *time.Timer
}

func (timer standardCatalogSyncTimer) C() <-chan time.Time { return timer.timer.C }
func (timer standardCatalogSyncTimer) Stop()               { timer.timer.Stop() }

type catalogAutomaticResult struct {
	err error
}

type CatalogSyncCoordinator struct {
	service   *Service
	client    catalogSyncClient
	cachePath string

	mu       sync.Mutex
	metadata catalog.Metadata
	hasLKG   bool
	pending  *catalog.SyncResult
	inFlight *catalogSyncCall
	last     CatalogSyncStatus

	processCtx    context.Context
	shuttingDown  bool
	storeCache    func(string, catalog.SyncResult) error
	applySnapshot func(context.Context, *catalog.Snapshot) error
	newTicker     func(time.Duration) runtimeTicker
	newTimer      func(time.Duration) catalogSyncTimer
	now           func() time.Time
	groupWake     chan struct{}
	immediateWake chan struct{}
}

var errCatalogSyncCoordinatorStopped = errors.New("catalog sync coordinator is shutting down")

func newCatalogSyncCoordinator(
	service *Service,
	client catalogSyncClient,
	cachePath string,
	metadata catalog.Metadata,
	hasLKG bool,
) *CatalogSyncCoordinator {
	coordinator := &CatalogSyncCoordinator{
		service: service, client: client, cachePath: cachePath,
		metadata: metadata, hasLKG: hasLKG,
		storeCache: catalog.StoreCache,
		newTicker: func(interval time.Duration) runtimeTicker {
			return standardRuntimeTicker{ticker: time.NewTicker(interval)}
		},
		newTimer: func(interval time.Duration) catalogSyncTimer {
			return standardCatalogSyncTimer{timer: time.NewTimer(interval)}
		},
		now:           time.Now,
		groupWake:     make(chan struct{}, 1),
		immediateWake: make(chan struct{}, 1),
	}
	if service != nil {
		coordinator.applySnapshot = service.applyCatalogSnapshot
		service.catalogSync = coordinator
	}
	return coordinator
}

// NewCatalogBootstrap loads the durable last-known-good catalog without doing
// network I/O. Missing or invalid cache files result in an empty runtime.
func NewCatalogBootstrap(cfg *config.Config) *CatalogBootstrap {
	dataDir := "."
	if cfg != nil && cfg.DataDir != "" {
		dataDir = cfg.DataDir
	}
	return newCatalogBootstrap(dataDir)
}

// NewCatalogSyncCoordinator wires the fixed Models.dev client to the shared
// service/runtime publication boundary.
func NewCatalogSyncCoordinator(
	service *Service,
	client *catalog.Client,
	bootstrap *CatalogBootstrap,
) *CatalogSyncCoordinator {
	if bootstrap == nil {
		bootstrap = &CatalogBootstrap{Runtime: &catalog.Runtime{}}
	}
	return newCatalogSyncCoordinator(
		service,
		client,
		bootstrap.CachePath,
		bootstrap.Metadata,
		bootstrap.HasLKG,
	)
}

func (coordinator *CatalogSyncCoordinator) Run(ctx context.Context) {
	coordinator.mu.Lock()
	if coordinator.shuttingDown {
		coordinator.mu.Unlock()
		return
	}
	coordinator.processCtx = ctx
	coordinator.mu.Unlock()
	defer coordinator.shutdown()

	periodic := coordinator.newTicker(modelsDevSyncInterval)
	defer periodic.Stop()
	results := make(chan catalogAutomaticResult, 4)
	launch := func(trigger CatalogSyncTrigger) {
		go func() {
			_, err := coordinator.Sync(ctx, trigger)
			select {
			case results <- catalogAutomaticResult{err: err}:
			case <-ctx.Done():
			}
		}()
	}
	launch(CatalogSyncStartup)

	var retry catalogSyncTimer
	var retryC <-chan time.Time
	var debounce catalogSyncTimer
	var debounceC <-chan time.Time
	stopTimer := func(timer catalogSyncTimer) {
		if timer != nil {
			timer.Stop()
		}
	}
	defer func() {
		stopTimer(retry)
		stopTimer(debounce)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-periodic.C():
			launch(CatalogSyncPeriodic)
		case <-coordinator.groupWake:
			stopTimer(debounce)
			debounce = coordinator.newTimer(modelsDevGroupDebounce)
			debounceC = debounce.C()
		case <-coordinator.immediateWake:
			launch(CatalogSyncSettings)
		case <-debounceC:
			debounce = nil
			debounceC = nil
			launch(CatalogSyncGroup)
		case <-retryC:
			retry = nil
			retryC = nil
			launch(CatalogSyncPeriodic)
		case result := <-results:
			if result.err == nil || coordinator.hasLastKnownGood() {
				stopTimer(retry)
				retry = nil
				retryC = nil
				continue
			}
			if retry == nil {
				retry = coordinator.newTimer(modelsDevInitialRetry)
				retryC = retry.C()
			}
		}
	}
}

func (coordinator *CatalogSyncCoordinator) shutdown() {
	coordinator.mu.Lock()
	coordinator.shuttingDown = true
	call := coordinator.inFlight
	if call != nil {
		call.cancel()
	}
	coordinator.mu.Unlock()
	if call != nil {
		<-call.done
	}
	coordinator.mu.Lock()
	coordinator.processCtx = nil
	coordinator.mu.Unlock()
}

func (coordinator *CatalogSyncCoordinator) RequestGroupSync() {
	if coordinator == nil || coordinator.service == nil ||
		!coordinator.service.modelsDevAutoSyncEnabled() {
		return
	}
	select {
	case coordinator.groupWake <- struct{}{}:
	default:
	}
}

func (coordinator *CatalogSyncCoordinator) RequestImmediateSync() {
	if coordinator == nil || coordinator.service == nil ||
		!coordinator.service.modelsDevAutoSyncEnabled() {
		return
	}
	select {
	case coordinator.immediateWake <- struct{}{}:
	default:
	}
}

func (coordinator *CatalogSyncCoordinator) hasLastKnownGood() bool {
	coordinator.mu.Lock()
	hasLKG := coordinator.hasLKG
	coordinator.mu.Unlock()
	return hasLKG && coordinator.service != nil &&
		coordinator.service.catalogRuntime != nil &&
		coordinator.service.catalogRuntime.HasGeneration()
}

func (coordinator *CatalogSyncCoordinator) joinedWaiterCount() int {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.inFlight == nil {
		return 0
	}
	return coordinator.inFlight.waiters
}

func (coordinator *CatalogSyncCoordinator) PendingGeneration() *catalog.SyncResult {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.pending == nil {
		return nil
	}
	copy := *coordinator.pending
	copy.RawJSON = append([]byte(nil), copy.RawJSON...)
	return &copy
}

func (coordinator *CatalogSyncCoordinator) Sync(
	ctx context.Context,
	trigger CatalogSyncTrigger,
) (CatalogSyncStatus, error) {
	if err := ctx.Err(); err != nil {
		return CatalogSyncStatus{}, err
	}
	coordinator.mu.Lock()
	if coordinator.shuttingDown {
		metadata := coordinator.metadata
		coordinator.mu.Unlock()
		return coordinator.failureStatus(trigger, metadata), errCatalogSyncCoordinatorStopped
	}
	if coordinator.inFlight != nil {
		call := coordinator.inFlight
		call.waiters++
		coordinator.mu.Unlock()
		return waitCatalogSyncCall(ctx, call)
	}
	if trigger != CatalogSyncManual && !coordinator.service.modelsDevAutoSyncEnabled() {
		status := CatalogSyncStatus{Trigger: trigger, Skipped: true}
		coordinator.last = status
		coordinator.mu.Unlock()
		coordinator.logSyncLifecycle(
			logrus.InfoLevel,
			"skipped",
			status,
			0,
			logrus.Fields{"skip_reason": "auto_sync_disabled"},
			"Models.dev catalog synchronization skipped",
		)
		return status, nil
	}
	operationParent := context.WithoutCancel(ctx)
	if coordinator.processCtx != nil {
		operationParent = coordinator.processCtx
	}
	operationCtx, cancelOperation := context.WithCancel(operationParent)
	call := &catalogSyncCall{done: make(chan struct{}), cancel: cancelOperation}
	coordinator.inFlight = call
	coordinator.mu.Unlock()

	go coordinator.execute(operationCtx, call, trigger)
	return waitCatalogSyncCall(ctx, call)
}

func waitCatalogSyncCall(ctx context.Context, call *catalogSyncCall) (CatalogSyncStatus, error) {
	select {
	case <-ctx.Done():
		return CatalogSyncStatus{}, ctx.Err()
	case <-call.done:
		return call.status, call.err
	}
}

func (coordinator *CatalogSyncCoordinator) execute(
	ctx context.Context,
	call *catalogSyncCall,
	trigger CatalogSyncTrigger,
) {
	startedAt := time.Now()
	coordinator.logSyncLifecycle(
		logrus.InfoLevel,
		"started",
		CatalogSyncStatus{Trigger: trigger},
		0,
		nil,
		"Models.dev catalog synchronization started",
	)
	status, err := coordinator.executeSync(ctx, trigger)
	duration := time.Since(startedAt)
	if err != nil {
		coordinator.logSyncLifecycle(
			logrus.WarnLevel,
			"failed",
			status,
			duration,
			nil,
			"Models.dev catalog synchronization failed",
		)
	} else {
		coordinator.logSyncLifecycle(
			logrus.InfoLevel,
			"succeeded",
			status,
			duration,
			nil,
			"Models.dev catalog synchronization completed",
		)
	}
	call.cancel()
	coordinator.mu.Lock()
	call.status = status
	call.err = err
	coordinator.last = status
	coordinator.inFlight = nil
	close(call.done)
	coordinator.mu.Unlock()
}

func (coordinator *CatalogSyncCoordinator) logSyncLifecycle(
	level logrus.Level,
	outcome string,
	status CatalogSyncStatus,
	duration time.Duration,
	extra logrus.Fields,
	message string,
) {
	fields := logrus.Fields{
		"event":   "models_dev_catalog_sync",
		"trigger": status.Trigger,
		"outcome": outcome,
	}
	if duration > 0 {
		fields["duration_ms"] = duration.Milliseconds()
	}
	if status.CheckedAtMS != 0 {
		fields["checked_at_ms"] = status.CheckedAtMS
	}
	if status.SuccessfulFetchAtMS != 0 {
		fields["successful_fetch_at_ms"] = status.SuccessfulFetchAtMS
	}
	if status.NotModified {
		fields["not_modified"] = true
	}
	if status.ErrorCode != "" {
		fields["error_code"] = status.ErrorCode
	}
	for name, value := range extra {
		fields[name] = value
	}
	utils.LogPlaneBestEffort(
		logrus.StandardLogger(),
		level,
		utils.LogPlaneControl,
		fields,
		message,
	)
}

func (coordinator *CatalogSyncCoordinator) executeSync(
	ctx context.Context,
	trigger CatalogSyncTrigger,
) (CatalogSyncStatus, error) {
	coordinator.mu.Lock()
	pending := coordinator.pending
	metadata := coordinator.metadata
	coordinator.mu.Unlock()
	if pending != nil {
		if err := ctx.Err(); err != nil {
			return coordinator.failureStatus(trigger, metadata), err
		}
		if coordinator.applySnapshot == nil {
			return coordinator.failureStatus(trigger, metadata), fmt.Errorf("catalog snapshot application is unavailable")
		}
		if err := coordinator.applySnapshot(ctx, pending.Snapshot); err != nil {
			return coordinator.failureStatus(trigger, metadata), err
		}
		coordinator.mu.Lock()
		coordinator.metadata = pending.Metadata
		coordinator.hasLKG = true
		coordinator.pending = nil
		coordinator.mu.Unlock()
		return catalogSyncSuccessStatus(trigger, pending.Metadata, false), nil
	}

	if coordinator.client == nil {
		return coordinator.failureStatus(trigger, metadata), fmt.Errorf("catalog sync client is unavailable")
	}
	result, err := coordinator.client.Sync(ctx, metadata)
	if err != nil {
		return coordinator.failureStatus(trigger, metadata), err
	}
	if err := ctx.Err(); err != nil {
		return coordinator.failureStatus(trigger, metadata), err
	}
	if result.NotModified {
		if !coordinator.hasLastKnownGood() {
			return coordinator.failureStatus(trigger, metadata), fmt.Errorf("catalog sync returned not modified without a last-known-good generation")
		}
		coordinator.mu.Lock()
		coordinator.metadata = result.Metadata
		coordinator.mu.Unlock()
		return catalogSyncSuccessStatus(trigger, result.Metadata, true), nil
	}
	if result.Snapshot == nil || len(result.RawJSON) == 0 {
		return coordinator.failureStatus(trigger, metadata), fmt.Errorf("catalog sync returned an invalid successful result")
	}
	if err := coordinator.storeCache(coordinator.cachePath, result); err != nil {
		return coordinator.failureStatus(trigger, metadata), err
	}
	if err := ctx.Err(); err != nil {
		return coordinator.failureStatus(trigger, metadata), err
	}

	coordinator.mu.Lock()
	coordinator.pending = &result
	coordinator.mu.Unlock()
	if coordinator.applySnapshot == nil {
		return coordinator.failureStatus(trigger, metadata), fmt.Errorf("catalog snapshot application is unavailable")
	}
	if err := coordinator.applySnapshot(ctx, result.Snapshot); err != nil {
		return coordinator.failureStatus(trigger, metadata), err
	}
	coordinator.mu.Lock()
	coordinator.metadata = result.Metadata
	coordinator.hasLKG = true
	coordinator.pending = nil
	coordinator.mu.Unlock()
	return catalogSyncSuccessStatus(trigger, result.Metadata, false), nil
}

func catalogSyncSuccessStatus(
	trigger CatalogSyncTrigger,
	metadata catalog.Metadata,
	notModified bool,
) CatalogSyncStatus {
	return CatalogSyncStatus{
		Trigger: trigger, CheckedAtMS: metadata.CheckedAtMillis,
		SuccessfulFetchAtMS: metadata.SuccessfulFetchAtMillis,
		NotModified:         notModified,
	}
}

func (coordinator *CatalogSyncCoordinator) failureStatus(
	trigger CatalogSyncTrigger,
	metadata catalog.Metadata,
) CatalogSyncStatus {
	checkedAt := coordinator.now().UnixMilli()
	return CatalogSyncStatus{
		Trigger:             trigger,
		CheckedAtMS:         checkedAt,
		SuccessfulFetchAtMS: metadata.SuccessfulFetchAtMillis,
		ErrorCode:           "catalog_sync_failed",
	}
}

func (s *Service) modelsDevAutoSyncEnabled() bool {
	if s == nil {
		return false
	}
	if s.modelsDevAutoSyncOverride != nil {
		return *s.modelsDevAutoSyncOverride
	}
	snapshot := s.manager.Current()
	if snapshot == nil {
		return true
	}
	return snapshot.Settings.ModelsDevAutoSyncEnabled
}

func (s *Service) applyCatalogSnapshot(ctx context.Context, snapshot *catalog.Snapshot) error {
	if snapshot == nil {
		return fmt.Errorf("catalog snapshot is required: %w", app_errors.ErrInternalServer)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.enforceOperationRecoveryBarrierLocked(ctx, 0); err != nil {
		return err
	}

	var table *pricing.Table
	err := s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		if err := reconcileCatalogAutomaticPrices(tx, snapshot); err != nil {
			return err
		}
		if err := reconcileReferencedPrices(tx, snapshot); err != nil {
			return err
		}
		if err := cleanupUnreferencedAutomaticPrices(tx); err != nil {
			return err
		}
		var err error
		table, err = loadPriceTable(ctx, tx)
		return err
	})
	if err != nil {
		return err
	}
	s.priceRuntime.Publish(table)
	s.catalogRuntime.Publish(snapshot)
	logMissingAutomaticPricePriorityProviders(snapshot)
	return nil
}

func logMissingAutomaticPricePriorityProviders(snapshot *catalog.Snapshot) {
	if snapshot == nil {
		return
	}
	priority := catalog.AutomaticPriceProviderPriority()
	missing := make([]string, 0, len(priority))
	for _, providerID := range priority {
		if _, exists := snapshot.Providers[providerID]; !exists {
			missing = append(missing, providerID)
		}
	}
	if len(missing) == 0 {
		return
	}
	utils.LogPlaneBestEffort(
		logrus.StandardLogger(),
		logrus.WarnLevel,
		utils.LogPlaneControl,
		logrus.Fields{
			"event":                "models_dev_catalog_price_priority_missing",
			"missing_provider_ids": missing,
		},
		"Catalog missing automatic price priority providers",
	)
}

func reconcileCatalogAutomaticPrices(tx *gorm.DB, snapshot *catalog.Snapshot) error {
	var rows []models.ModelPrice
	if err := tx.Where("is_manual = ?", false).Order("id ASC").Find(&rows).Error; err != nil {
		return fmt.Errorf("load automatic model prices: %w", app_errors.ParseDBError(err))
	}
	for _, row := range rows {
		cost, _, ok := resolveAutomaticPrice(snapshot, row.ModelID)
		desired := models.ModelPrice{}
		if ok {
			var err error
			desired, err = automaticCatalogValues(cost)
			if err != nil {
				return fmt.Errorf("normalize catalog price: %w", app_errors.ErrInternalServer)
			}
		}
		if catalogPriceValuesEqual(row, desired) {
			continue
		}
		if err := tx.Model(&models.ModelPrice{}).
			Where("id = ? AND is_manual = ?", row.ID, false).
			Updates(map[string]any{
				"input_price_nano_usd_per_million_tokens":       desired.InputPriceNanoUSDPerMillionTokens,
				"output_price_nano_usd_per_million_tokens":      desired.OutputPriceNanoUSDPerMillionTokens,
				"cache_read_price_nano_usd_per_million_tokens":  desired.CacheReadPriceNanoUSDPerMillionTokens,
				"cache_write_price_nano_usd_per_million_tokens": desired.CacheWritePriceNanoUSDPerMillionTokens,
				"context_price_tiers":                           desired.ContextPriceTiers,
			}).Error; err != nil {
			return fmt.Errorf("update automatic model price: %w", app_errors.ParseDBError(err))
		}
	}
	return nil
}

func automaticCatalogValues(cost *catalog.ModelCost) (models.ModelPrice, error) {
	row := models.ModelPrice{}
	if cost == nil {
		return row, nil
	}
	row.InputPriceNanoUSDPerMillionTokens = priceStoragePointer(cost.Prices.Input)
	row.OutputPriceNanoUSDPerMillionTokens = priceStoragePointer(cost.Prices.Output)
	row.CacheReadPriceNanoUSDPerMillionTokens = priceStoragePointer(cost.Prices.CacheRead)
	row.CacheWritePriceNanoUSDPerMillionTokens = priceStoragePointer(cost.Prices.CacheWrite)
	if len(cost.ContextTiers) == 0 {
		return row, nil
	}
	tiers := make([]models.ContextPriceTier, 0, len(cost.ContextTiers))
	for _, tier := range cost.ContextTiers {
		tiers = append(tiers, models.ContextPriceTier{
			ThresholdTokens:                        tier.InputThresholdTokens,
			InputPriceNanoUSDPerMillionTokens:      priceStoragePointer(tier.Prices.Input),
			OutputPriceNanoUSDPerMillionTokens:     priceStoragePointer(tier.Prices.Output),
			CacheReadPriceNanoUSDPerMillionTokens:  priceStoragePointer(tier.Prices.CacheRead),
			CacheWritePriceNanoUSDPerMillionTokens: priceStoragePointer(tier.Prices.CacheWrite),
		})
	}
	encoded, err := json.Marshal(tiers)
	if err != nil {
		return models.ModelPrice{}, err
	}
	row.ContextPriceTiers, err = models.NormalizeContextPriceTiers(models.JSON(encoded))
	return row, err
}

func catalogPriceValuesEqual(left, right models.ModelPrice) bool {
	return pricePointerEqual(left.InputPriceNanoUSDPerMillionTokens, right.InputPriceNanoUSDPerMillionTokens) &&
		pricePointerEqual(left.OutputPriceNanoUSDPerMillionTokens, right.OutputPriceNanoUSDPerMillionTokens) &&
		pricePointerEqual(left.CacheReadPriceNanoUSDPerMillionTokens, right.CacheReadPriceNanoUSDPerMillionTokens) &&
		pricePointerEqual(left.CacheWritePriceNanoUSDPerMillionTokens, right.CacheWritePriceNanoUSDPerMillionTokens) &&
		reflect.DeepEqual([]byte(left.ContextPriceTiers), []byte(right.ContextPriceTiers))
}

func pricePointerEqual(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func cleanupUnreferencedAutomaticPrices(tx *gorm.DB) error {
	references, err := loadReferencedPrices(tx)
	if err != nil {
		return err
	}
	var rows []models.ModelPrice
	if err := tx.Where("is_manual = ?", false).Order("id ASC").Find(&rows).Error; err != nil {
		return fmt.Errorf("load automatic prices for cleanup: %w", app_errors.ParseDBError(err))
	}
	ids := make([]uint, 0)
	for _, row := range rows {
		identity := pricing.Identity{ModelID: row.ModelID}
		if _, referenced := references[identity]; !referenced {
			ids = append(ids, row.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	if err := tx.Where("is_manual = ? AND id IN ?", false, ids).Delete(&models.ModelPrice{}).Error; err != nil {
		return fmt.Errorf("remove unreferenced automatic prices: %w", app_errors.ParseDBError(err))
	}
	return nil
}
