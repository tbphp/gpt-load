package keypool

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

// PerformanceTuner 性能调优器
type PerformanceTuner struct {
	pool           *MemoryLayeredPool
	config         *TunerConfig
	
	// 测试状态
	running        int32
	testResults    []*TestResult
	mu             sync.RWMutex
	
	// 最优配置
	optimalConfig  *OptimalConfig
}

// TunerConfig 调优器配置
type TunerConfig struct {
	TestDuration      time.Duration `json:"test_duration"`
	ConcurrencyLevels []int         `json:"concurrency_levels"`
	ShardCounts       []int         `json:"shard_counts"`
	CacheSizes        []int         `json:"cache_sizes"`
	BatchSizes        []int         `json:"batch_sizes"`
	TestKeyCount      int           `json:"test_key_count"`
	WarmupDuration    time.Duration `json:"warmup_duration"`
}

// TestResult 测试结果
type TestResult struct {
	Config          *TestConfig       `json:"config"`
	Metrics         *PerformanceMetrics `json:"metrics"`
	Duration        time.Duration     `json:"duration"`
	Timestamp       time.Time         `json:"timestamp"`
	MemoryUsage     *MemoryStats      `json:"memory_usage"`
	CPUUsage        float64           `json:"cpu_usage"`
}

// TestConfig 测试配置
type TestConfig struct {
	Concurrency int `json:"concurrency"`
	ShardCount  int `json:"shard_count"`
	CacheSize   int `json:"cache_size"`
	BatchSize   int `json:"batch_size"`
}

// PerformanceMetrics 性能指标
type PerformanceMetrics struct {
	TotalOperations    int64         `json:"total_operations"`
	SuccessfulOps      int64         `json:"successful_ops"`
	FailedOps          int64         `json:"failed_ops"`
	AvgLatency         time.Duration `json:"avg_latency"`
	P95Latency         time.Duration `json:"p95_latency"`
	P99Latency         time.Duration `json:"p99_latency"`
	Throughput         float64       `json:"throughput"` // ops/sec
	ErrorRate          float64       `json:"error_rate"`
	CacheHitRate       float64       `json:"cache_hit_rate"`
	LockContentionRate float64       `json:"lock_contention_rate"`
}

// MemoryStats 内存统计
type MemoryStats struct {
	HeapAlloc    uint64 `json:"heap_alloc"`
	HeapSys      uint64 `json:"heap_sys"`
	HeapInuse    uint64 `json:"heap_inuse"`
	StackInuse   uint64 `json:"stack_inuse"`
	NumGC        uint32 `json:"num_gc"`
	GCPauseTotal uint64 `json:"gc_pause_total"`
}

// OptimalConfig 最优配置
type OptimalConfig struct {
	ShardCount       int     `json:"shard_count"`
	CacheSize        int     `json:"cache_size"`
	BatchSize        int     `json:"batch_size"`
	Score            float64 `json:"score"`
	Throughput       float64 `json:"throughput"`
	AvgLatency       time.Duration `json:"avg_latency"`
	MemoryEfficiency float64 `json:"memory_efficiency"`
}

// NewPerformanceTuner 创建性能调优器
func NewPerformanceTuner(pool *MemoryLayeredPool, config *TunerConfig) *PerformanceTuner {
	if config == nil {
		config = DefaultTunerConfig()
	}
	
	return &PerformanceTuner{
		pool:        pool,
		config:      config,
		testResults: make([]*TestResult, 0),
	}
}

// DefaultTunerConfig 默认调优器配置
func DefaultTunerConfig() *TunerConfig {
	return &TunerConfig{
		TestDuration:      30 * time.Second,
		ConcurrencyLevels: []int{1, 2, 4, 8, 16, 32, 64},
		ShardCounts:       []int{4, 8, 16, 32, 64},
		CacheSizes:        []int{100, 500, 1000, 2000, 5000},
		BatchSizes:        []int{10, 50, 100, 200, 500},
		TestKeyCount:      10000,
		WarmupDuration:    5 * time.Second,
	}
}

// RunFullTuning 运行完整的性能调优
func (t *PerformanceTuner) RunFullTuning() (*OptimalConfig, error) {
	if !atomic.CompareAndSwapInt32(&t.running, 0, 1) {
		return nil, fmt.Errorf("tuning is already running")
	}
	defer atomic.StoreInt32(&t.running, 0)
	
	logrus.Info("Starting full performance tuning")
	
	bestConfig := &OptimalConfig{Score: -1}
	
	// 测试不同的配置组合
	for _, shardCount := range t.config.ShardCounts {
		for _, cacheSize := range t.config.CacheSizes {
			for _, batchSize := range t.config.BatchSizes {
				testConfig := &TestConfig{
					Concurrency: runtime.NumCPU() * 2, // 使用CPU核心数的2倍
					ShardCount:  shardCount,
					CacheSize:   cacheSize,
					BatchSize:   batchSize,
				}
				
				result, err := t.runSingleTest(testConfig)
				if err != nil {
					logrus.WithError(err).Warn("Test failed")
					continue
				}
				
				// 计算配置得分
				score := t.calculateScore(result)
				if score > bestConfig.Score {
					bestConfig = &OptimalConfig{
						ShardCount:       testConfig.ShardCount,
						CacheSize:        testConfig.CacheSize,
						BatchSize:        testConfig.BatchSize,
						Score:            score,
						Throughput:       result.Metrics.Throughput,
						AvgLatency:       result.Metrics.AvgLatency,
						MemoryEfficiency: t.calculateMemoryEfficiency(result),
					}
				}
				
				t.mu.Lock()
				t.testResults = append(t.testResults, result)
				t.mu.Unlock()
				
				logrus.WithFields(logrus.Fields{
					"shardCount": testConfig.ShardCount,
					"cacheSize":  testConfig.CacheSize,
					"batchSize":  testConfig.BatchSize,
					"score":      score,
					"throughput": result.Metrics.Throughput,
				}).Info("Test completed")
			}
		}
	}
	
	t.optimalConfig = bestConfig
	
	logrus.WithFields(logrus.Fields{
		"optimalShardCount": bestConfig.ShardCount,
		"optimalCacheSize":  bestConfig.CacheSize,
		"optimalBatchSize":  bestConfig.BatchSize,
		"bestScore":         bestConfig.Score,
		"bestThroughput":    bestConfig.Throughput,
	}).Info("Performance tuning completed")
	
	return bestConfig, nil
}

// runSingleTest 运行单个测试
func (t *PerformanceTuner) runSingleTest(config *TestConfig) (*TestResult, error) {
	// 创建测试用的分片存储
	shardedConfig := &ShardedStoreConfig{
		ShardCount:     config.ShardCount,
		LockTimeout:    1 * time.Second,
		GCInterval:     10 * time.Minute,
		MaxMemoryUsage: 100 * 1024 * 1024,
		EnableMetrics:  true,
		CacheSize:      config.CacheSize,
	}
	
	testStore, err := NewShardedMemoryStore(shardedConfig)
	if err != nil {
		return nil, err
	}
	defer testStore.Close()
	
	// 预热阶段
	if err := t.warmup(testStore, config); err != nil {
		return nil, fmt.Errorf("warmup failed: %w", err)
	}
	
	// 开始性能测试
	startTime := time.Now()
	var memStatsBefore runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memStatsBefore)
	
	metrics := t.runLoadTest(testStore, config)
	
	var memStatsAfter runtime.MemStats
	runtime.ReadMemStats(&memStatsAfter)
	duration := time.Since(startTime)
	
	result := &TestResult{
		Config:    config,
		Metrics:   metrics,
		Duration:  duration,
		Timestamp: time.Now(),
		MemoryUsage: &MemoryStats{
			HeapAlloc:    memStatsAfter.HeapAlloc - memStatsBefore.HeapAlloc,
			HeapSys:      memStatsAfter.HeapSys,
			HeapInuse:    memStatsAfter.HeapInuse,
			StackInuse:   memStatsAfter.StackInuse,
			NumGC:        memStatsAfter.NumGC - memStatsBefore.NumGC,
			GCPauseTotal: memStatsAfter.PauseTotalNs - memStatsBefore.PauseTotalNs,
		},
	}
	
	return result, nil
}

// warmup 预热阶段
func (t *PerformanceTuner) warmup(store *ShardedMemoryStore, config *TestConfig) error {
	// 预填充一些数据
	for i := 0; i < config.CacheSize; i++ {
		key := fmt.Sprintf("warmup_key_%d", i)
		value := []byte(fmt.Sprintf("warmup_value_%d", i))
		if err := store.Set(key, value, time.Hour); err != nil {
			return err
		}
	}
	
	// 预热访问
	ctx, cancel := context.WithTimeout(context.Background(), t.config.WarmupDuration)
	defer cancel()
	
	var wg sync.WaitGroup
	for i := 0; i < config.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					key := fmt.Sprintf("warmup_key_%d", i%config.CacheSize)
					store.Get(key)
				}
			}
		}()
	}
	
	wg.Wait()
	return nil
}

// runLoadTest 运行负载测试
func (t *PerformanceTuner) runLoadTest(store *ShardedMemoryStore, config *TestConfig) *PerformanceMetrics {
	ctx, cancel := context.WithTimeout(context.Background(), t.config.TestDuration)
	defer cancel()
	
	var (
		totalOps     int64
		successfulOps int64
		failedOps    int64
		latencies    []time.Duration
		latencyMu    sync.Mutex
	)
	
	var wg sync.WaitGroup
	
	// 启动并发工作协程
	for i := 0; i < config.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			
			for {
				select {
				case <-ctx.Done():
					return
				default:
					start := time.Now()
					
					// 执行操作（读写混合）
					if workerID%3 == 0 {
						// 写操作
						key := fmt.Sprintf("test_key_%d_%d", workerID, atomic.LoadInt64(&totalOps))
						value := []byte(fmt.Sprintf("test_value_%d", atomic.LoadInt64(&totalOps)))
						err := store.Set(key, value, time.Hour)
						if err != nil {
							atomic.AddInt64(&failedOps, 1)
						} else {
							atomic.AddInt64(&successfulOps, 1)
						}
					} else {
						// 读操作
						key := fmt.Sprintf("test_key_%d_%d", workerID, atomic.LoadInt64(&totalOps)%1000)
						_, err := store.Get(key)
						if err != nil && err != store.ErrNotFound {
							atomic.AddInt64(&failedOps, 1)
						} else {
							atomic.AddInt64(&successfulOps, 1)
						}
					}
					
					latency := time.Since(start)
					
					latencyMu.Lock()
					latencies = append(latencies, latency)
					latencyMu.Unlock()
					
					atomic.AddInt64(&totalOps, 1)
				}
			}
		}(i)
	}
	
	wg.Wait()
	
	// 计算统计指标
	metrics := &PerformanceMetrics{
		TotalOperations: totalOps,
		SuccessfulOps:   successfulOps,
		FailedOps:       failedOps,
		Throughput:      float64(totalOps) / t.config.TestDuration.Seconds(),
	}
	
	if totalOps > 0 {
		metrics.ErrorRate = float64(failedOps) / float64(totalOps)
	}
	
	// 计算延迟统计
	if len(latencies) > 0 {
		metrics.AvgLatency = t.calculateAvgLatency(latencies)
		metrics.P95Latency = t.calculatePercentile(latencies, 0.95)
		metrics.P99Latency = t.calculatePercentile(latencies, 0.99)
	}
	
	// 获取缓存命中率
	if storeMetrics := store.GetMetrics(); storeMetrics != nil {
		if totalReads, ok := storeMetrics["total_reads"].(int64); ok && totalReads > 0 {
			if hits, ok := storeMetrics["cache_hits"].(int64); ok {
				metrics.CacheHitRate = float64(hits) / float64(totalReads)
			}
		}
	}
	
	return metrics
}

// calculateScore 计算配置得分
func (t *PerformanceTuner) calculateScore(result *TestResult) float64 {
	// 综合考虑吞吐量、延迟、错误率和内存效率
	throughputScore := result.Metrics.Throughput / 10000.0 // 归一化到0-1
	latencyScore := 1.0 / (float64(result.Metrics.AvgLatency.Microseconds()) / 1000.0 + 1.0)
	errorScore := 1.0 - result.Metrics.ErrorRate
	memoryScore := t.calculateMemoryEfficiency(result)
	
	// 加权平均
	score := throughputScore*0.4 + latencyScore*0.3 + errorScore*0.2 + memoryScore*0.1
	
	return score
}

// calculateMemoryEfficiency 计算内存效率
func (t *PerformanceTuner) calculateMemoryEfficiency(result *TestResult) float64 {
	if result.MemoryUsage.HeapAlloc == 0 {
		return 1.0
	}
	
	// 操作数 / 内存使用量 (MB)
	efficiency := float64(result.Metrics.TotalOperations) / (float64(result.MemoryUsage.HeapAlloc) / 1024.0 / 1024.0)
	
	// 归一化到0-1
	return 1.0 / (efficiency/1000.0 + 1.0)
}

// calculateAvgLatency 计算平均延迟
func (t *PerformanceTuner) calculateAvgLatency(latencies []time.Duration) time.Duration {
	var total time.Duration
	for _, latency := range latencies {
		total += latency
	}
	return total / time.Duration(len(latencies))
}

// calculatePercentile 计算百分位延迟
func (t *PerformanceTuner) calculatePercentile(latencies []time.Duration, percentile float64) time.Duration {
	if len(latencies) == 0 {
		return 0
	}
	
	// 简单排序（实际应用中可以使用更高效的算法）
	sorted := make([]time.Duration, len(latencies))
	copy(sorted, latencies)
	
	// 冒泡排序
	for i := 0; i < len(sorted)-1; i++ {
		for j := 0; j < len(sorted)-i-1; j++ {
			if sorted[j] > sorted[j+1] {
				sorted[j], sorted[j+1] = sorted[j+1], sorted[j]
			}
		}
	}
	
	index := int(float64(len(sorted)) * percentile)
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	
	return sorted[index]
}

// GetOptimalConfig 获取最优配置
func (t *PerformanceTuner) GetOptimalConfig() *OptimalConfig {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.optimalConfig
}

// GetTestResults 获取所有测试结果
func (t *PerformanceTuner) GetTestResults() []*TestResult {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	results := make([]*TestResult, len(t.testResults))
	copy(results, t.testResults)
	return results
}

// ApplyOptimalConfig 应用最优配置到池
func (t *PerformanceTuner) ApplyOptimalConfig() error {
	if t.optimalConfig == nil {
		return fmt.Errorf("no optimal configuration found, run tuning first")
	}
	
	// 更新池配置
	newConfig := &MemoryPoolConfig{
		ShardCount:     t.optimalConfig.ShardCount,
		EnableSharding: true,
		LockTimeout:    1 * time.Second,
		GCInterval:     10 * time.Minute,
		MaxMemoryUsage: 100 * 1024 * 1024,
	}
	
	t.pool.memoryConfig = newConfig
	
	// 如果有本地缓存，更新缓存大小
	if t.pool.localCache != nil {
		t.pool.localCache.maxSize = t.optimalConfig.CacheSize
	}
	
	logrus.WithFields(logrus.Fields{
		"shardCount": t.optimalConfig.ShardCount,
		"cacheSize":  t.optimalConfig.CacheSize,
		"batchSize":  t.optimalConfig.BatchSize,
	}).Info("Applied optimal configuration")
	
	return nil
}
