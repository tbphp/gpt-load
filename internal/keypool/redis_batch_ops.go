package keypool

import (
	"fmt"
	"gpt-load/internal/models"
	"gpt-load/internal/store"
	"time"

	"github.com/sirupsen/logrus"
)

// BatchOperation 定义批量操作类型
type BatchOperation struct {
	Type     BatchOpType
	GroupID  uint
	KeyIDs   []uint
	FromPool PoolType
	ToPool   PoolType
	Metadata interface{}
}

// BatchOpType 定义批量操作类型
type BatchOpType string

const (
	BatchOpAddKeys    BatchOpType = "add_keys"
	BatchOpRemoveKeys BatchOpType = "remove_keys"
	BatchOpMoveKeys   BatchOpType = "move_keys"
	BatchOpSyncKeys   BatchOpType = "sync_keys"
)

// ExecuteBatchOperations 执行批量操作
func (p *RedisLayeredPool) ExecuteBatchOperations(operations []BatchOperation) error {
	if len(operations) == 0 {
		return nil
	}
	
	// 检查是否支持Pipeline
	pipeliner, supportsPipeline := p.store.(store.RedisPipeliner)
	if !supportsPipeline {
		// 回退到逐个执行
		return p.executeBatchSequentially(operations)
	}
	
	// 使用Pipeline批量执行
	return p.executeBatchWithPipeline(pipeliner, operations)
}

// executeBatchWithPipeline 使用Pipeline执行批量操作
func (p *RedisLayeredPool) executeBatchWithPipeline(pipeliner store.RedisPipeliner, operations []BatchOperation) error {
	pipe := pipeliner.Pipeline()
	
	// 构建Pipeline命令
	for _, op := range operations {
		if err := p.addOperationToPipeline(pipe, op); err != nil {
			return NewPoolErrorWithCause(ErrorTypeInternal, "PIPELINE_BUILD_FAILED", 
				"Failed to build pipeline operation", err)
		}
	}
	
	// 执行Pipeline
	startTime := time.Now()
	if err := pipe.Exec(); err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "PIPELINE_EXEC_FAILED", 
			"Failed to execute pipeline operations", err)
	}
	
	duration := time.Since(startTime)
	logrus.WithFields(logrus.Fields{
		"operations": len(operations),
		"duration":   duration,
	}).Debug("Batch operations executed via pipeline")
	
	// 执行后处理
	return p.postProcessBatchOperations(operations)
}

// addOperationToPipeline 将操作添加到Pipeline
func (p *RedisLayeredPool) addOperationToPipeline(pipe store.Pipeliner, op BatchOperation) error {
	switch op.Type {
	case BatchOpAddKeys:
		return p.addAddKeysOperationToPipeline(pipe, op)
	case BatchOpRemoveKeys:
		return p.addRemoveKeysOperationToPipeline(pipe, op)
	case BatchOpMoveKeys:
		return p.addMoveKeysOperationToPipeline(pipe, op)
	case BatchOpSyncKeys:
		return p.addSyncKeysOperationToPipeline(pipe, op)
	default:
		return NewPoolError(ErrorTypeValidation, "UNKNOWN_BATCH_OP", "Unknown batch operation type")
	}
}

// addAddKeysOperationToPipeline 添加密钥添加操作到Pipeline
func (p *RedisLayeredPool) addAddKeysOperationToPipeline(pipe store.Pipeliner, op BatchOperation) error {
	if len(op.KeyIDs) == 0 {
		return nil
	}
	
	// 默认添加到验证池
	poolType := PoolTypeValidation
	if op.ToPool != "" {
		poolType = op.ToPool
	}
	
	switch poolType {
	case PoolTypeValidation:
		validationKey := p.getRedisKey(op.GroupID, PoolTypeValidation)
		members := make([]interface{}, len(op.KeyIDs))
		for i, keyID := range op.KeyIDs {
			members[i] = keyID
		}
		pipe.SAdd(validationKey, members...)
		
	case PoolTypeReady:
		readyKey := p.getRedisKey(op.GroupID, PoolTypeReady)
		for _, keyID := range op.KeyIDs {
			pipe.LPush(readyKey, keyID)
		}
		
	case PoolTypeActive:
		activeKey := p.getRedisKey(op.GroupID, PoolTypeActive)
		for _, keyID := range op.KeyIDs {
			pipe.LPush(activeKey, keyID)
		}
		
	case PoolTypeCooling:
		// 冷却池需要特殊处理，不应该直接添加
		return NewPoolError(ErrorTypeValidation, "INVALID_POOL_TYPE", "Cannot directly add keys to cooling pool")
	}
	
	return nil
}

// addRemoveKeysOperationToPipeline 添加密钥移除操作到Pipeline
func (p *RedisLayeredPool) addRemoveKeysOperationToPipeline(pipe store.Pipeliner, op BatchOperation) error {
	if len(op.KeyIDs) == 0 {
		return nil
	}
	
	// 从所有池中移除
	poolTypes := []PoolType{PoolTypeValidation, PoolTypeReady, PoolTypeActive}
	
	for _, poolType := range poolTypes {
		switch poolType {
		case PoolTypeValidation:
			validationKey := p.getRedisKey(op.GroupID, PoolTypeValidation)
			members := make([]interface{}, len(op.KeyIDs))
			for i, keyID := range op.KeyIDs {
				members[i] = keyID
			}
			pipe.SRem(validationKey, members...)
			
		case PoolTypeReady, PoolTypeActive:
			poolKey := p.getRedisKey(op.GroupID, poolType)
			for _, keyID := range op.KeyIDs {
				pipe.LRem(poolKey, 0, keyID)
			}
		}
	}
	
	// 删除密钥详情
	for _, keyID := range op.KeyIDs {
		detailsKey := p.getKeyDetailsKey(keyID)
		// 注意：Pipeline接口需要扩展以支持DEL操作
		// 暂时在后处理中删除
	}
	
	return nil
}

// addMoveKeysOperationToPipeline 添加密钥移动操作到Pipeline
func (p *RedisLayeredPool) addMoveKeysOperationToPipeline(pipe store.Pipeliner, op BatchOperation) error {
	if len(op.KeyIDs) == 0 || op.FromPool == op.ToPool {
		return nil
	}
	
	// 从源池移除
	switch op.FromPool {
	case PoolTypeValidation:
		validationKey := p.getRedisKey(op.GroupID, PoolTypeValidation)
		members := make([]interface{}, len(op.KeyIDs))
		for i, keyID := range op.KeyIDs {
			members[i] = keyID
		}
		pipe.SRem(validationKey, members...)
		
	case PoolTypeReady, PoolTypeActive:
		fromKey := p.getRedisKey(op.GroupID, op.FromPool)
		for _, keyID := range op.KeyIDs {
			pipe.LRem(fromKey, 0, keyID)
		}
	}
	
	// 添加到目标池
	switch op.ToPool {
	case PoolTypeValidation:
		validationKey := p.getRedisKey(op.GroupID, PoolTypeValidation)
		members := make([]interface{}, len(op.KeyIDs))
		for i, keyID := range op.KeyIDs {
			members[i] = keyID
		}
		pipe.SAdd(validationKey, members...)
		
	case PoolTypeReady, PoolTypeActive:
		toKey := p.getRedisKey(op.GroupID, op.ToPool)
		for _, keyID := range op.KeyIDs {
			pipe.LPush(toKey, keyID)
		}
		
	case PoolTypeCooling:
		// 冷却池需要特殊处理
		if coolData, ok := op.Metadata.(map[uint]time.Time); ok {
			coolingKey := p.getRedisKey(op.GroupID, PoolTypeCooling)
			for _, keyID := range op.KeyIDs {
				if resetAt, exists := coolData[keyID]; exists {
					members := []store.ZMember{{
						Score:  float64(resetAt.Unix()),
						Member: keyID,
					}}
					pipe.ZAdd(coolingKey, members...)
				}
			}
		}
	}
	
	return nil
}

// addSyncKeysOperationToPipeline 添加密钥同步操作到Pipeline
func (p *RedisLayeredPool) addSyncKeysOperationToPipeline(pipe store.Pipeliner, op BatchOperation) error {
	// 密钥详情同步需要在后处理中进行，因为需要查询数据库
	return nil
}

// executeBatchSequentially 逐个执行批量操作（回退方案）
func (p *RedisLayeredPool) executeBatchSequentially(operations []BatchOperation) error {
	for i, op := range operations {
		if err := p.executeSingleOperation(op); err != nil {
			return NewPoolErrorWithCause(ErrorTypeStorage, "SEQUENTIAL_EXEC_FAILED", 
				fmt.Sprintf("Failed to execute operation %d", i), err)
		}
	}
	
	return nil
}

// executeSingleOperation 执行单个操作
func (p *RedisLayeredPool) executeSingleOperation(op BatchOperation) error {
	switch op.Type {
	case BatchOpAddKeys:
		return p.addKeysToPool(op.GroupID, op.KeyIDs, op.ToPool)
	case BatchOpRemoveKeys:
		return p.RemoveKeys(op.GroupID, op.KeyIDs)
	case BatchOpMoveKeys:
		for _, keyID := range op.KeyIDs {
			if err := p.MoveKey(keyID, op.FromPool, op.ToPool); err != nil {
				return err
			}
		}
		return nil
	case BatchOpSyncKeys:
		return p.syncKeysFromDatabase(op.GroupID, op.KeyIDs)
	default:
		return NewPoolError(ErrorTypeValidation, "UNKNOWN_BATCH_OP", "Unknown batch operation type")
	}
}

// postProcessBatchOperations 批量操作后处理
func (p *RedisLayeredPool) postProcessBatchOperations(operations []BatchOperation) error {
	for _, op := range operations {
		switch op.Type {
		case BatchOpRemoveKeys:
			// 删除密钥详情
			for _, keyID := range op.KeyIDs {
				detailsKey := p.getKeyDetailsKey(keyID)
				if err := p.store.Delete(detailsKey); err != nil {
					logrus.WithFields(logrus.Fields{"keyID": keyID, "error": err}).Warn("Failed to delete key details")
				}
			}
			
		case BatchOpSyncKeys:
			// 同步密钥详情
			if err := p.syncKeysFromDatabase(op.GroupID, op.KeyIDs); err != nil {
				logrus.WithFields(logrus.Fields{
					"groupID": op.GroupID,
					"keyIDs":  op.KeyIDs,
					"error":   err,
				}).Warn("Failed to sync keys from database")
			}
		}
	}
	
	return nil
}

// syncKeysFromDatabase 从数据库同步密钥详情
func (p *RedisLayeredPool) syncKeysFromDatabase(groupID uint, keyIDs []uint) error {
	if len(keyIDs) == 0 {
		return nil
	}
	
	var keys []models.APIKey
	if err := p.db.Where("id IN ? AND group_id = ?", keyIDs, groupID).Find(&keys).Error; err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "DB_QUERY_FAILED", "Failed to query keys from database", err)
	}
	
	for _, key := range keys {
		if err := p.syncKeyDetailsToRedis(key.ID, key.GroupID); err != nil {
			logrus.WithFields(logrus.Fields{"keyID": key.ID, "error": err}).Warn("Failed to sync key details to Redis")
		}
	}
	
	return nil
}

// BatchAddKeys 批量添加密钥
func (p *RedisLayeredPool) BatchAddKeys(groupID uint, keyIDs []uint, poolType PoolType) error {
	if len(keyIDs) == 0 {
		return nil
	}
	
	operations := []BatchOperation{
		{
			Type:    BatchOpAddKeys,
			GroupID: groupID,
			KeyIDs:  keyIDs,
			ToPool:  poolType,
		},
		{
			Type:    BatchOpSyncKeys,
			GroupID: groupID,
			KeyIDs:  keyIDs,
		},
	}
	
	return p.ExecuteBatchOperations(operations)
}

// BatchMoveKeys 批量移动密钥
func (p *RedisLayeredPool) BatchMoveKeys(groupID uint, keyIDs []uint, fromPool, toPool PoolType) error {
	if len(keyIDs) == 0 || fromPool == toPool {
		return nil
	}
	
	operations := []BatchOperation{
		{
			Type:     BatchOpMoveKeys,
			GroupID:  groupID,
			KeyIDs:   keyIDs,
			FromPool: fromPool,
			ToPool:   toPool,
		},
	}
	
	return p.ExecuteBatchOperations(operations)
}
