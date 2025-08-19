package keypool

import (
	"context"
	"fmt"
	"gpt-load/internal/models"
	"gpt-load/internal/store"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// MemoryBatchProcessor 内存批量操作处理器
type MemoryBatchProcessor struct {
	pool         *MemoryLayeredPool
	batchSize    int
	maxWaitTime  time.Duration
	workerCount  int

	// 批量队列
	operationQueue chan *BatchOperation
	resultQueue    chan *BatchResult

	// 控制
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// BatchResult 批量操作结果
type BatchResult struct {
	Operation *BatchOperation
	Error     error
	Duration  time.Duration
	Processed int
}

// NewMemoryBatchProcessor 创建内存批量处理器
func NewMemoryBatchProcessor(pool *MemoryLayeredPool, config *BatchProcessorConfig) *MemoryBatchProcessor {
	if config == nil {
		config = DefaultBatchProcessorConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	processor := &MemoryBatchProcessor{
		pool:           pool,
		batchSize:      config.BatchSize,
		maxWaitTime:    config.MaxWaitTime,
		workerCount:    config.WorkerCount,
		operationQueue: make(chan *BatchOperation, config.QueueSize),
		resultQueue:    make(chan *BatchResult, config.QueueSize),
		ctx:            ctx,
		cancel:         cancel,
	}

	// 启动工作协程
	for i := 0; i < processor.workerCount; i++ {
		processor.wg.Add(1)
		go processor.worker(i)
	}

	return processor
}

// BatchProcessorConfig 批量处理器配置
type BatchProcessorConfig struct {
	BatchSize    int           `json:"batch_size"`
	MaxWaitTime  time.Duration `json:"max_wait_time"`
	WorkerCount  int           `json:"worker_count"`
	QueueSize    int           `json:"queue_size"`
}

// DefaultBatchProcessorConfig 默认批量处理器配置
func DefaultBatchProcessorConfig() *BatchProcessorConfig {
	return &BatchProcessorConfig{
		BatchSize:   100,
		MaxWaitTime: 100 * time.Millisecond,
		WorkerCount: 4,
		QueueSize:   1000,
	}
}

// worker 工作协程
func (p *MemoryBatchProcessor) worker(workerID int) {
	defer p.wg.Done()

	logrus.WithField("workerID", workerID).Debug("Batch processor worker started")

	for {
		select {
		case <-p.ctx.Done():
			return
		case operation := <-p.operationQueue:
			result := p.processOperation(operation)

			select {
			case p.resultQueue <- result:
			case <-p.ctx.Done():
				return
			}
		}
	}
}

// processOperation 处理单个批量操作
func (p *MemoryBatchProcessor) processOperation(operation *BatchOperation) *BatchResult {
	startTime := time.Now()

	result := &BatchResult{
		Operation: operation,
		Duration:  0,
		Processed: 0,
	}

	switch operation.Type {
	case BatchOpAddKeys:
		result.Error = p.processBatchAddKeys(operation)
		result.Processed = len(operation.KeyIDs)

	case BatchOpRemoveKeys:
		result.Error = p.processBatchRemoveKeys(operation)
		result.Processed = len(operation.KeyIDs)

	case BatchOpMoveKeys:
		result.Error = p.processBatchMoveKeys(operation)
		result.Processed = len(operation.KeyIDs)

	case BatchOpSyncKeys:
		result.Error = p.processBatchSyncKeys(operation)
		result.Processed = len(operation.KeyIDs)

	default:
		result.Error = NewPoolError(ErrorTypeValidation, "UNKNOWN_BATCH_OP", "Unknown batch operation type")
	}

	result.Duration = time.Since(startTime)
	return result
}

// processBatchAddKeys 批量添加密钥
func (p *MemoryBatchProcessor) processBatchAddKeys(operation *BatchOperation) error {
	if len(operation.KeyIDs) == 0 {
		return nil
	}

	// 按分片分组密钥
	shardGroups := p.groupKeysByShards(operation.KeyIDs)

	// 并发处理每个分片
	var wg sync.WaitGroup
	errors := make(chan error, len(shardGroups))

	for shardIndex, keyIDs := range shardGroups {
		wg.Add(1)
		go func(shard int, keys []uint) {
			defer wg.Done()

			if err := p.addKeysToShard(operation.GroupID, keys, operation.ToPool, shard); err != nil {
				errors <- err
			}
		}(shardIndex, keyIDs)
	}

	wg.Wait()
	close(errors)

	// 检查是否有错误
	for err := range errors {
		if err != nil {
			return err
		}
	}

	return nil
}

// processBatchRemoveKeys 批量移除密钥
func (p *MemoryBatchProcessor) processBatchRemoveKeys(operation *BatchOperation) error {
	if len(operation.KeyIDs) == 0 {
		return nil
	}

	// 按分片分组密钥
	shardGroups := p.groupKeysByShards(operation.KeyIDs)

	// 并发处理每个分片
	var wg sync.WaitGroup
	errors := make(chan error, len(shardGroups))

	for shardIndex, keyIDs := range shardGroups {
		wg.Add(1)
		go func(shard int, keys []uint) {
			defer wg.Done()

			if err := p.removeKeysFromShard(operation.GroupID, keys, shard); err != nil {
				errors <- err
			}
		}(shardIndex, keyIDs)
	}

	wg.Wait()
	close(errors)

	// 检查是否有错误
	for err := range errors {
		if err != nil {
			return err
		}
	}

	return nil
}

// processBatchMoveKeys 批量移动密钥
func (p *MemoryBatchProcessor) processBatchMoveKeys(operation *BatchOperation) error {
	if len(operation.KeyIDs) == 0 || operation.FromPool == operation.ToPool {
		return nil
	}

	// 按分片分组密钥
	shardGroups := p.groupKeysByShards(operation.KeyIDs)

	// 并发处理每个分片
	var wg sync.WaitGroup
	errors := make(chan error, len(shardGroups))

	for shardIndex, keyIDs := range shardGroups {
		wg.Add(1)
		go func(shard int, keys []uint) {
			defer wg.Done()

			if err := p.moveKeysInShard(operation.GroupID, keys, operation.FromPool, operation.ToPool, shard); err != nil {
				errors <- err
			}
		}(shardIndex, keyIDs)
	}

	wg.Wait()
	close(errors)

	// 检查是否有错误
	for err := range errors {
		if err != nil {
			return err
		}
	}

	return nil
}

// processBatchSyncKeys 批量同步密钥
func (p *MemoryBatchProcessor) processBatchSyncKeys(operation *BatchOperation) error {
	if len(operation.KeyIDs) == 0 {
		return nil
	}

	// 从数据库批量查询密钥
	var keys []models.APIKey
	if err := p.pool.db.Where("id IN ? AND group_id = ?", operation.KeyIDs, operation.GroupID).Find(&keys).Error; err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "DB_QUERY_FAILED", "Failed to query keys from database", err)
	}

	// 按分片分组密钥
	shardGroups := make(map[int][]models.APIKey)
	for _, key := range keys {
		shardIndex := p.getShardIndex(key.ID)
		shardGroups[shardIndex] = append(shardGroups[shardIndex], key)
	}

	// 并发同步每个分片
	var wg sync.WaitGroup
	errors := make(chan error, len(shardGroups))

	for shardIndex, shardKeys := range shardGroups {
		wg.Add(1)
		go func(shard int, keys []models.APIKey) {
			defer wg.Done()

			if err := p.syncKeysInShard(keys, shard); err != nil {
				errors <- err
			}
		}(shardIndex, shardKeys)
	}

	wg.Wait()
	close(errors)

	// 检查是否有错误
	for err := range errors {
		if err != nil {
			return err
		}
	}

	return nil
}

// groupKeysByShards 按分片分组密钥
func (p *MemoryBatchProcessor) groupKeysByShards(keyIDs []uint) map[int][]uint {
	shardGroups := make(map[int][]uint)

	for _, keyID := range keyIDs {
		shardIndex := p.getShardIndex(keyID)
		shardGroups[shardIndex] = append(shardGroups[shardIndex], keyID)
	}

	return shardGroups
}

// getShardIndex 获取密钥对应的分片索引
func (p *MemoryBatchProcessor) getShardIndex(keyID uint) int {
	// 使用简单的模运算来确定分片
	return int(keyID) % p.pool.memoryConfig.ShardCount
}

// addKeysToShard 向指定分片添加密钥
func (p *MemoryBatchProcessor) addKeysToShard(groupID uint, keyIDs []uint, poolType PoolType, shardIndex int) error {
	// 获取池键名
	poolKey := p.pool.getRedisKey(groupID, poolType)

	// 根据池类型执行不同的添加逻辑
	switch poolType {
	case PoolTypeValidation:
		// 批量添加到SET
		members := make([]interface{}, len(keyIDs))
		for i, keyID := range keyIDs {
			members[i] = keyID
		}
		return p.pool.shardedStore.SAdd(poolKey, members...)

	case PoolTypeReady, PoolTypeActive:
		// 批量添加到LIST
		for _, keyID := range keyIDs {
			if err := p.pool.shardedStore.LPush(poolKey, keyID); err != nil {
				return err
			}
		}
		return nil

	case PoolTypeCooling:
		return NewPoolError(ErrorTypeValidation, "INVALID_POOL_TYPE", "Cannot directly add keys to cooling pool")

	default:
		return NewPoolError(ErrorTypeValidation, "UNKNOWN_POOL_TYPE", "Unknown pool type")
	}
}

// removeKeysFromShard 从指定分片移除密钥
func (p *MemoryBatchProcessor) removeKeysFromShard(groupID uint, keyIDs []uint, shardIndex int) error {
	// 从所有池类型中移除
	poolTypes := []PoolType{PoolTypeValidation, PoolTypeReady, PoolTypeActive, PoolTypeCooling}

	for _, poolType := range poolTypes {
		poolKey := p.pool.getRedisKey(groupID, poolType)

		switch poolType {
		case PoolTypeValidation:
			// 从SET中批量移除
			members := make([]interface{}, len(keyIDs))
			for i, keyID := range keyIDs {
				members[i] = keyID
			}
			if err := p.pool.shardedStore.SRem(poolKey, members...); err != nil {
				logrus.WithFields(logrus.Fields{
					"poolType": poolType,
					"error":    err,
				}).Warn("Failed to remove keys from validation pool")
			}

		case PoolTypeReady, PoolTypeActive:
			// 从LIST中移除
			for _, keyID := range keyIDs {
				if err := p.pool.shardedStore.LRem(poolKey, 0, keyID); err != nil {
					logrus.WithFields(logrus.Fields{
						"poolType": poolType,
						"keyID":    keyID,
						"error":    err,
					}).Warn("Failed to remove key from list pool")
				}
			}

		case PoolTypeCooling:
			// 从ZSET中移除
			members := make([]interface{}, len(keyIDs))
			for i, keyID := range keyIDs {
				members[i] = keyID
			}
			if err := p.pool.shardedStore.ZRem(poolKey, members...); err != nil {
				logrus.WithFields(logrus.Fields{
					"poolType": poolType,
					"error":    err,
				}).Warn("Failed to remove keys from cooling pool")
			}
		}
	}

	// 删除密钥详情
	for _, keyID := range keyIDs {
		detailsKey := p.pool.getKeyDetailsKey(keyID)
		if err := p.pool.shardedStore.Delete(detailsKey); err != nil {
			logrus.WithFields(logrus.Fields{"keyID": keyID, "error": err}).Warn("Failed to delete key details")
		}

		// 从本地缓存移除
		if p.pool.localCache != nil {
			p.pool.localCache.Remove(keyID)
		}
	}

	return nil
}

// moveKeysInShard 在指定分片中移动密钥
func (p *MemoryBatchProcessor) moveKeysInShard(groupID uint, keyIDs []uint, fromPool, toPool PoolType, shardIndex int) error {
	// 先从源池移除
	if err := p.removeKeysFromPool(groupID, keyIDs, fromPool); err != nil {
		return err
	}

	// 再添加到目标池
	if err := p.addKeysToShard(groupID, keyIDs, toPool, shardIndex); err != nil {
		// 移动失败，尝试回滚
		if rollbackErr := p.addKeysToShard(groupID, keyIDs, fromPool, shardIndex); rollbackErr != nil {
			logrus.WithFields(logrus.Fields{
				"keyIDs":      keyIDs,
				"fromPool":    fromPool,
				"toPool":      toPool,
				"rollbackErr": rollbackErr,
			}).Error("Failed to rollback key move operation")
		}
		return err
	}

	return nil
}

// removeKeysFromPool 从指定池移除密钥
func (p *MemoryBatchProcessor) removeKeysFromPool(groupID uint, keyIDs []uint, poolType PoolType) error {
	poolKey := p.pool.getRedisKey(groupID, poolType)

	switch poolType {
	case PoolTypeValidation:
		members := make([]interface{}, len(keyIDs))
		for i, keyID := range keyIDs {
			members[i] = keyID
		}
		return p.pool.shardedStore.SRem(poolKey, members...)

	case PoolTypeReady, PoolTypeActive:
		for _, keyID := range keyIDs {
			if err := p.pool.shardedStore.LRem(poolKey, 0, keyID); err != nil {
				return err
			}
		}
		return nil

	case PoolTypeCooling:
		members := make([]interface{}, len(keyIDs))
		for i, keyID := range keyIDs {
			members[i] = keyID
		}
		return p.pool.shardedStore.ZRem(poolKey, members...)

	default:
		return NewPoolError(ErrorTypeValidation, "UNKNOWN_POOL_TYPE", "Unknown pool type")
	}
}

// syncKeysInShard 在指定分片中同步密钥
func (p *MemoryBatchProcessor) syncKeysInShard(keys []models.APIKey, shardIndex int) error {
	for _, key := range keys {
		if err := p.pool.syncKeyDetailsToRedis(key.ID, key.GroupID); err != nil {
			logrus.WithFields(logrus.Fields{"keyID": key.ID, "error": err}).Warn("Failed to sync key details to memory store")
		}
	}
	return nil
}

// SubmitBatchOperation 提交批量操作
func (p *MemoryBatchProcessor) SubmitBatchOperation(operation *BatchOperation) error {
	select {
	case p.operationQueue <- operation:
		return nil
	case <-p.ctx.Done():
		return NewPoolError(ErrorTypeInternal, "PROCESSOR_STOPPED", "Batch processor is stopped")
	default:
		return NewPoolError(ErrorTypeCapacity, "QUEUE_FULL", "Batch operation queue is full")
	}
}

// GetResult 获取批量操作结果
func (p *MemoryBatchProcessor) GetResult() (*BatchResult, error) {
	select {
	case result := <-p.resultQueue:
		return result, nil
	case <-p.ctx.Done():
		return nil, NewPoolError(ErrorTypeInternal, "PROCESSOR_STOPPED", "Batch processor is stopped")
	}
}

// Stop 停止批量处理器
func (p *MemoryBatchProcessor) Stop() error {
	p.cancel()
	p.wg.Wait()

	close(p.operationQueue)
	close(p.resultQueue)

	return nil
}
