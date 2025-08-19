package keypool

import (
	"fmt"
	"gpt-load/internal/store"
	"hash/fnv"
	"sync"
	"time"
)

// ShardedMemoryStore 分片内存存储，减少锁竞争
type ShardedMemoryStore struct {
	shards    []*memoryShard
	shardMask uint32
	config    *ShardedStoreConfig
}

// ShardedStoreConfig 分片存储配置
type ShardedStoreConfig struct {
	ShardCount      int           `json:"shard_count"`      // 分片数量，必须是2的幂
	LockTimeout     time.Duration `json:"lock_timeout"`     // 锁超时时间
	GCInterval      time.Duration `json:"gc_interval"`      // 垃圾回收间隔
	MaxMemoryUsage  int64         `json:"max_memory_usage"` // 最大内存使用量
	EnableMetrics   bool          `json:"enable_metrics"`   // 启用指标收集
	CacheSize       int           `json:"cache_size"`       // 每个分片的缓存大小
}

// memoryShard 内存分片
type memoryShard struct {
	mu       sync.RWMutex
	data     map[string]interface{}
	metrics  *shardMetrics
	config   *ShardedStoreConfig
	lastGC   time.Time
}

// shardMetrics 分片指标
type shardMetrics struct {
	readCount    int64
	writeCount   int64
	lockWaitTime time.Duration
	memoryUsage  int64
}

// DefaultShardedStoreConfig 返回默认分片存储配置
func DefaultShardedStoreConfig() *ShardedStoreConfig {
	return &ShardedStoreConfig{
		ShardCount:     16, // 16个分片，适合大多数场景
		LockTimeout:    1 * time.Second,
		GCInterval:     10 * time.Minute,
		MaxMemoryUsage: 100 * 1024 * 1024, // 100MB
		EnableMetrics:  true,
		CacheSize:      1000,
	}
}

// NewShardedMemoryStore 创建分片内存存储
func NewShardedMemoryStore(config *ShardedStoreConfig) (*ShardedMemoryStore, error) {
	if config == nil {
		config = DefaultShardedStoreConfig()
	}

	// 验证分片数量是2的幂
	if config.ShardCount <= 0 || (config.ShardCount&(config.ShardCount-1)) != 0 {
		return nil, fmt.Errorf("shard count must be a power of 2, got %d", config.ShardCount)
	}

	shards := make([]*memoryShard, config.ShardCount)
	for i := 0; i < config.ShardCount; i++ {
		shards[i] = &memoryShard{
			data:    make(map[string]interface{}),
			metrics: &shardMetrics{},
			config:  config,
			lastGC:  time.Now(),
		}
	}

	store := &ShardedMemoryStore{
		shards:    shards,
		shardMask: uint32(config.ShardCount - 1),
		config:    config,
	}

	// 启动后台垃圾回收
	if config.GCInterval > 0 {
		go store.backgroundGC()
	}

	return store, nil
}

// getShard 根据键获取对应的分片
func (s *ShardedMemoryStore) getShard(key string) *memoryShard {
	hash := fnv.New32a()
	hash.Write([]byte(key))
	shardIndex := hash.Sum32() & s.shardMask
	return s.shards[shardIndex]
}

// Set 存储键值对
func (s *ShardedMemoryStore) Set(key string, value []byte, ttl time.Duration) error {
	shard := s.getShard(key)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	var expiresAt int64
	if ttl > 0 {
		expiresAt = time.Now().UnixNano() + ttl.Nanoseconds()
	}

	shard.data[key] = &memoryStoreItem{
		value:     value,
		expiresAt: expiresAt,
	}

	if s.config.EnableMetrics {
		shard.metrics.writeCount++
	}

	return nil
}

// Get 获取值
func (s *ShardedMemoryStore) Get(key string) ([]byte, error) {
	shard := s.getShard(key)

	shard.mu.RLock()
	rawItem, exists := shard.data[key]
	shard.mu.RUnlock()

	if !exists {
		return nil, store.ErrNotFound
	}

	item, ok := rawItem.(*memoryStoreItem)
	if !ok {
		return nil, fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	// 检查过期
	if item.expiresAt > 0 && time.Now().UnixNano() > item.expiresAt {
		shard.mu.Lock()
		delete(shard.data, key)
		shard.mu.Unlock()
		return nil, store.ErrNotFound
	}

	if s.config.EnableMetrics {
		shard.metrics.readCount++
	}

	return item.value, nil
}

// Delete 删除键
func (s *ShardedMemoryStore) Delete(key string) error {
	shard := s.getShard(key)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	delete(shard.data, key)
	return nil
}

// Del 批量删除键
func (s *ShardedMemoryStore) Del(keys ...string) error {
	// 按分片分组键
	shardKeys := make(map[*memoryShard][]string)
	for _, key := range keys {
		shard := s.getShard(key)
		shardKeys[shard] = append(shardKeys[shard], key)
	}

	// 并发删除每个分片的键
	var wg sync.WaitGroup
	for shard, keys := range shardKeys {
		wg.Add(1)
		go func(s *memoryShard, k []string) {
			defer wg.Done()
			s.mu.Lock()
			defer s.mu.Unlock()
			for _, key := range k {
				delete(s.data, key)
			}
		}(shard, keys)
	}

	wg.Wait()
	return nil
}

// Exists 检查键是否存在
func (s *ShardedMemoryStore) Exists(key string) (bool, error) {
	shard := s.getShard(key)

	shard.mu.RLock()
	rawItem, exists := shard.data[key]
	shard.mu.RUnlock()

	if !exists {
		return false, nil
	}

	// 检查过期
	if item, ok := rawItem.(*memoryStoreItem); ok {
		if item.expiresAt > 0 && time.Now().UnixNano() > item.expiresAt {
			shard.mu.Lock()
			delete(shard.data, key)
			shard.mu.Unlock()
			return false, nil
		}
	}

	return true, nil
}

// SetNX 如果键不存在则设置
func (s *ShardedMemoryStore) SetNX(key string, value []byte, ttl time.Duration) (bool, error) {
	shard := s.getShard(key)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	rawItem, exists := shard.data[key]
	if exists {
		if item, ok := rawItem.(*memoryStoreItem); ok {
			if item.expiresAt == 0 || time.Now().UnixNano() < item.expiresAt {
				return false, nil
			}
		} else {
			return false, nil
		}
	}

	var expiresAt int64
	if ttl > 0 {
		expiresAt = time.Now().UnixNano() + ttl.Nanoseconds()
	}

	shard.data[key] = &memoryStoreItem{
		value:     value,
		expiresAt: expiresAt,
	}

	return true, nil
}

// backgroundGC 后台垃圾回收
func (s *ShardedMemoryStore) backgroundGC() {
	ticker := time.NewTicker(s.config.GCInterval)
	defer ticker.Stop()

	for range ticker.C {
		s.performGC()
	}
}

// performGC 执行垃圾回收
func (s *ShardedMemoryStore) performGC() {
	now := time.Now()

	for _, shard := range s.shards {
		if now.Sub(shard.lastGC) < s.config.GCInterval {
			continue
		}

		go func(s *memoryShard) {
			s.mu.Lock()
			defer s.mu.Unlock()

			nowNano := now.UnixNano()
			for key, rawItem := range s.data {
				if item, ok := rawItem.(*memoryStoreItem); ok {
					if item.expiresAt > 0 && nowNano > item.expiresAt {
						delete(s.data, key)
					}
				}
			}

			s.lastGC = now
		}(shard)
	}
}

// GetMetrics 获取分片指标
func (s *ShardedMemoryStore) GetMetrics() map[string]interface{} {
	if !s.config.EnableMetrics {
		return nil
	}

	totalReads := int64(0)
	totalWrites := int64(0)
	totalMemory := int64(0)

	for i, shard := range s.shards {
		shard.mu.RLock()
		reads := shard.metrics.readCount
		writes := shard.metrics.writeCount
		memory := shard.metrics.memoryUsage
		shard.mu.RUnlock()

		totalReads += reads
		totalWrites += writes
		totalMemory += memory

		// 可以添加每个分片的详细指标
		_ = i // 分片索引
	}

	return map[string]interface{}{
		"total_reads":    totalReads,
		"total_writes":   totalWrites,
		"total_memory":   totalMemory,
		"shard_count":    len(s.shards),
		"avg_reads_per_shard":  totalReads / int64(len(s.shards)),
		"avg_writes_per_shard": totalWrites / int64(len(s.shards)),
	}
}

// Close 关闭存储
func (s *ShardedMemoryStore) Close() error {
	// 清理所有分片
	for _, shard := range s.shards {
		shard.mu.Lock()
		shard.data = nil
		shard.mu.Unlock()
	}
	return nil
}

// memoryStoreItem 内存存储项（复用现有定义）
type memoryStoreItem struct {
	value     []byte
	expiresAt int64
}

// --- HASH operations ---

// HSet 设置哈希字段
func (s *ShardedMemoryStore) HSet(key string, values map[string]any) error {
	shard := s.getShard(key)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	var hash map[string]string
	rawHash, exists := shard.data[key]
	if !exists {
		hash = make(map[string]string)
		shard.data[key] = hash
	} else {
		var ok bool
		hash, ok = rawHash.(map[string]string)
		if !ok {
			return fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
		}
	}

	for field, value := range values {
		hash[field] = fmt.Sprint(value)
	}

	if s.config.EnableMetrics {
		shard.metrics.writeCount++
	}

	return nil
}

// HGetAll 获取所有哈希字段
func (s *ShardedMemoryStore) HGetAll(key string) (map[string]string, error) {
	shard := s.getShard(key)

	shard.mu.RLock()
	defer shard.mu.RUnlock()

	rawHash, exists := shard.data[key]
	if !exists {
		return make(map[string]string), nil
	}

	hash, ok := rawHash.(map[string]string)
	if !ok {
		return nil, fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	// 复制map以避免并发问题
	result := make(map[string]string, len(hash))
	for k, v := range hash {
		result[k] = v
	}

	if s.config.EnableMetrics {
		shard.metrics.readCount++
	}

	return result, nil
}

// HIncrBy 增加哈希字段的整数值
func (s *ShardedMemoryStore) HIncrBy(key, field string, incr int64) (int64, error) {
	shard := s.getShard(key)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	var hash map[string]string
	rawHash, exists := shard.data[key]
	if !exists {
		hash = make(map[string]string)
		shard.data[key] = hash
	} else {
		var ok bool
		hash, ok = rawHash.(map[string]string)
		if !ok {
			return 0, fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
		}
	}

	currentStr, exists := hash[field]
	var current int64 = 0
	if exists {
		var err error
		current, err = parseInt64(currentStr)
		if err != nil {
			return 0, fmt.Errorf("hash field value is not an integer")
		}
	}

	newValue := current + incr
	hash[field] = fmt.Sprintf("%d", newValue)

	if s.config.EnableMetrics {
		shard.metrics.writeCount++
	}

	return newValue, nil
}

// --- LIST operations ---

// LPush 向列表头部添加元素
func (s *ShardedMemoryStore) LPush(key string, values ...any) error {
	shard := s.getShard(key)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	var list []string
	rawList, exists := shard.data[key]
	if !exists {
		list = make([]string, 0)
	} else {
		var ok bool
		list, ok = rawList.([]string)
		if !ok {
			return fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
		}
	}

	strValues := make([]string, len(values))
	for i, v := range values {
		strValues[i] = fmt.Sprint(v)
	}

	shard.data[key] = append(strValues, list...)

	if s.config.EnableMetrics {
		shard.metrics.writeCount++
	}

	return nil
}

// LRem 从列表中移除元素
func (s *ShardedMemoryStore) LRem(key string, count int64, value any) error {
	shard := s.getShard(key)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	rawList, exists := shard.data[key]
	if !exists {
		return nil
	}

	list, ok := rawList.([]string)
	if !ok {
		return fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	strValue := fmt.Sprint(value)
	newList := make([]string, 0, len(list))

	if count != 0 {
		return fmt.Errorf("LRem with non-zero count is not implemented in ShardedMemoryStore")
	}

	for _, item := range list {
		if item != strValue {
			newList = append(newList, item)
		}
	}

	shard.data[key] = newList

	if s.config.EnableMetrics {
		shard.metrics.writeCount++
	}

	return nil
}

// Rotate 轮转列表元素
func (s *ShardedMemoryStore) Rotate(key string) (string, error) {
	shard := s.getShard(key)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	rawList, exists := shard.data[key]
	if !exists {
		return "", store.ErrNotFound
	}

	list, ok := rawList.([]string)
	if !ok {
		return "", fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	if len(list) == 0 {
		return "", store.ErrNotFound
	}

	lastIndex := len(list) - 1
	item := list[lastIndex]

	// 将最后一个元素移到前面
	newList := append([]string{item}, list[:lastIndex]...)
	shard.data[key] = newList

	if s.config.EnableMetrics {
		shard.metrics.writeCount++
	}

	return item, nil
}

// --- SET operations ---

// SAdd 向集合添加成员
func (s *ShardedMemoryStore) SAdd(key string, members ...any) error {
	shard := s.getShard(key)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	var set map[string]struct{}
	rawSet, exists := shard.data[key]
	if !exists {
		set = make(map[string]struct{})
		shard.data[key] = set
	} else {
		var ok bool
		set, ok = rawSet.(map[string]struct{})
		if !ok {
			return fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
		}
	}

	for _, member := range members {
		set[fmt.Sprint(member)] = struct{}{}
	}

	if s.config.EnableMetrics {
		shard.metrics.writeCount++
	}

	return nil
}

// SRem 从集合移除成员
func (s *ShardedMemoryStore) SRem(key string, members ...any) error {
	shard := s.getShard(key)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	rawSet, exists := shard.data[key]
	if !exists {
		return nil
	}

	set, ok := rawSet.(map[string]struct{})
	if !ok {
		return fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	for _, member := range members {
		delete(set, fmt.Sprint(member))
	}

	if s.config.EnableMetrics {
		shard.metrics.writeCount++
	}

	return nil
}

// SMembers 获取集合所有成员
func (s *ShardedMemoryStore) SMembers(key string) ([]string, error) {
	shard := s.getShard(key)

	shard.mu.RLock()
	defer shard.mu.RUnlock()

	rawSet, exists := shard.data[key]
	if !exists {
		return []string{}, nil
	}

	set, ok := rawSet.(map[string]struct{})
	if !ok {
		return nil, fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	members := make([]string, 0, len(set))
	for member := range set {
		members = append(members, member)
	}

	if s.config.EnableMetrics {
		shard.metrics.readCount++
	}

	return members, nil
}

// SPopN 随机移除并返回集合成员
func (s *ShardedMemoryStore) SPopN(key string, count int64) ([]string, error) {
	shard := s.getShard(key)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	rawSet, exists := shard.data[key]
	if !exists {
		return []string{}, nil
	}

	set, ok := rawSet.(map[string]struct{})
	if !ok {
		return nil, fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	if count > int64(len(set)) {
		count = int64(len(set))
	}

	popped := make([]string, 0, count)
	for member := range set {
		if int64(len(popped)) >= count {
			break
		}
		popped = append(popped, member)
		delete(set, member)
	}

	if s.config.EnableMetrics {
		shard.metrics.writeCount++
	}

	return popped, nil
}

// --- ZSET operations ---

// ZAdd 向有序集合添加成员
func (s *ShardedMemoryStore) ZAdd(key string, members ...store.ZMember) error {
	shard := s.getShard(key)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	var zs *shardedZSet
	rawZset, exists := shard.data[key]
	if !exists {
		zs = &shardedZSet{
			members: make(map[string]float64),
			sorted:  make([]zsetMember, 0),
			dirty:   false,
		}
		shard.data[key] = zs
	} else {
		var ok bool
		zs, ok = rawZset.(*shardedZSet)
		if !ok {
			return fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
		}
	}

	for _, member := range members {
		memberStr := fmt.Sprint(member.Member)
		oldScore, existed := zs.members[memberStr]
		zs.members[memberStr] = member.Score

		if !existed || oldScore != member.Score {
			zs.dirty = true
		}
	}

	if s.config.EnableMetrics {
		shard.metrics.writeCount++
	}

	return nil
}

// ZRem 从有序集合移除成员
func (s *ShardedMemoryStore) ZRem(key string, members ...any) error {
	shard := s.getShard(key)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	rawZset, exists := shard.data[key]
	if !exists {
		return nil
	}

	zs, ok := rawZset.(*shardedZSet)
	if !ok {
		return fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	for _, member := range members {
		memberStr := fmt.Sprint(member)
		if _, existed := zs.members[memberStr]; existed {
			delete(zs.members, memberStr)
			zs.dirty = true
		}
	}

	if s.config.EnableMetrics {
		shard.metrics.writeCount++
	}

	return nil
}

// ZRangeByScore 按分数范围获取成员
func (s *ShardedMemoryStore) ZRangeByScore(key string, min, max float64) ([]string, error) {
	shard := s.getShard(key)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	rawZset, exists := shard.data[key]
	if !exists {
		return []string{}, nil
	}

	zs, ok := rawZset.(*shardedZSet)
	if !ok {
		return nil, fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	s.ensureSorted(zs)

	var result []string
	for _, member := range zs.sorted {
		if member.Score >= min && member.Score <= max {
			result = append(result, member.Member)
		}
	}

	if s.config.EnableMetrics {
		shard.metrics.readCount++
	}

	return result, nil
}

// ZRemRangeByScore 按分数范围移除成员
func (s *ShardedMemoryStore) ZRemRangeByScore(key string, min, max float64) (int64, error) {
	shard := s.getShard(key)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	rawZset, exists := shard.data[key]
	if !exists {
		return 0, nil
	}

	zs, ok := rawZset.(*shardedZSet)
	if !ok {
		return 0, fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	var removed int64
	for member, score := range zs.members {
		if score >= min && score <= max {
			delete(zs.members, member)
			removed++
		}
	}

	if removed > 0 {
		zs.dirty = true
	}

	if s.config.EnableMetrics {
		shard.metrics.writeCount++
	}

	return removed, nil
}

// ZCard 获取有序集合成员数量
func (s *ShardedMemoryStore) ZCard(key string) (int64, error) {
	shard := s.getShard(key)

	shard.mu.RLock()
	defer shard.mu.RUnlock()

	rawZset, exists := shard.data[key]
	if !exists {
		return 0, nil
	}

	zs, ok := rawZset.(*shardedZSet)
	if !ok {
		return 0, fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	if s.config.EnableMetrics {
		shard.metrics.readCount++
	}

	return int64(len(zs.members)), nil
}

// --- Pub/Sub operations (简化实现) ---

// Publish 发布消息
func (s *ShardedMemoryStore) Publish(channel string, message []byte) error {
	// 简化实现，实际应用中可能需要更复杂的发布订阅机制
	return nil
}

// Subscribe 订阅频道
func (s *ShardedMemoryStore) Subscribe(channel string) (store.Subscription, error) {
	// 简化实现，返回一个空的订阅
	return &emptySubscription{}, nil
}

// --- 辅助函数和类型定义 ---

// shardedZSet 分片有序集合
type shardedZSet struct {
	members map[string]float64
	sorted  []zsetMember
	dirty   bool
}

// zsetMember 有序集合成员
type zsetMember struct {
	Member string
	Score  float64
}

// ensureSorted 确保有序集合已排序
func (s *ShardedMemoryStore) ensureSorted(zs *shardedZSet) {
	if !zs.dirty {
		return
	}

	zs.sorted = zs.sorted[:0] // 清空但保留容量
	for member, score := range zs.members {
		zs.sorted = append(zs.sorted, zsetMember{
			Member: member,
			Score:  score,
		})
	}

	// 按分数排序
	for i := 0; i < len(zs.sorted)-1; i++ {
		for j := i + 1; j < len(zs.sorted); j++ {
			if zs.sorted[i].Score > zs.sorted[j].Score {
				zs.sorted[i], zs.sorted[j] = zs.sorted[j], zs.sorted[i]
			}
		}
	}

	zs.dirty = false
}

// parseInt64 解析整数
func parseInt64(s string) (int64, error) {
	var result int64 = 0
	var sign int64 = 1

	if len(s) == 0 {
		return 0, fmt.Errorf("empty string")
	}

	i := 0
	if s[0] == '-' {
		sign = -1
		i = 1
	} else if s[0] == '+' {
		i = 1
	}

	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("invalid character: %c", s[i])
		}
		result = result*10 + int64(s[i]-'0')
	}

	return result * sign, nil
}

// emptySubscription 空订阅实现
type emptySubscription struct{}

func (e *emptySubscription) Channel() <-chan *store.Message {
	ch := make(chan *store.Message)
	close(ch)
	return ch
}

func (e *emptySubscription) Close() error {
	return nil
}

// Reconfigure 重新配置分片存储
func (s *ShardedMemoryStore) Reconfigure(newConfig *ShardedStoreConfig) error {
	if newConfig == nil {
		return fmt.Errorf("config cannot be nil")
	}

	// 验证配置
	if err := s.validateConfig(newConfig); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// 如果分片数量改变，需要重新创建分片
	if newConfig.ShardCount != len(s.shards) {
		if err := s.reshardData(newConfig.ShardCount); err != nil {
			return fmt.Errorf("failed to reshard data: %w", err)
		}
	}

	// 更新配置
	s.config = newConfig
	s.shardMask = uint32(newConfig.ShardCount - 1)

	return nil
}

// reshardData 重新分片数据
func (s *ShardedMemoryStore) reshardData(newShardCount int) error {
	// 收集所有现有数据
	allData := make(map[string]interface{})
	for _, shard := range s.shards {
		shard.mu.Lock()
		for key, item := range shard.data {
			allData[key] = item
		}
		shard.mu.Unlock()
	}

	// 创建新的分片
	newShards := make([]*memoryShard, newShardCount)
	for i := 0; i < newShardCount; i++ {
		newShards[i] = &memoryShard{
			data:    make(map[string]interface{}),
			metrics: &shardMetrics{},
			config:  s.config,
			lastGC:  time.Now(),
		}
	}

	// 重新分布数据
	newShardMask := uint32(newShardCount - 1)
	for key, item := range allData {
		hash := fnv.New32a()
		hash.Write([]byte(key))
		shardIndex := hash.Sum32() & newShardMask
		newShards[shardIndex].data[key] = item
	}

	// 替换分片
	s.shards = newShards

	return nil
}

// validateConfig 验证配置
func (s *ShardedMemoryStore) validateConfig(config *ShardedStoreConfig) error {
	if config.ShardCount <= 0 || config.ShardCount > 1024 {
		return fmt.Errorf("invalid shard count: %d", config.ShardCount)
	}

	if config.CacheSize <= 0 {
		return fmt.Errorf("invalid cache size: %d", config.CacheSize)
	}

	if config.MaxMemoryUsage <= 0 {
		return fmt.Errorf("invalid max memory usage: %d", config.MaxMemoryUsage)
	}

	// 检查分片数量是否为2的幂
	if config.ShardCount&(config.ShardCount-1) != 0 {
		return fmt.Errorf("shard count must be a power of 2: %d", config.ShardCount)
	}

	return nil
}
