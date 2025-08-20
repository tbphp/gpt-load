<template>
  <div class="stats-card">
    <div class="card-header">
      <h3 class="card-title">
        <i class="fas fa-chart-bar"></i>
        统计监控
      </h3>
      <div class="header-actions">
        <el-select 
          v-model="selectedDays" 
          size="small" 
          style="width: 120px"
          @change="handleDaysChange"
        >
          <el-option label="1天" :value="1" />
          <el-option label="3天" :value="3" />
          <el-option label="7天" :value="7" />
          <el-option label="15天" :value="15" />
          <el-option label="30天" :value="30" />
        </el-select>
        <el-button 
          type="primary" 
          size="small" 
          :loading="loading"
          @click="$emit('refresh')"
        >
          <i class="fas fa-sync-alt"></i>
          刷新
        </el-button>
      </div>
    </div>

    <div class="card-content" v-if="stats">
      <!-- 核心指标 -->
      <div class="metrics-grid">
        <div class="metric-card success">
          <div class="metric-icon">
            <i class="fas fa-check-circle"></i>
          </div>
          <div class="metric-content">
            <div class="metric-value">{{ stats.successful_logs }}</div>
            <div class="metric-label">成功处理</div>
            <div class="metric-rate">{{ formatSuccessRate(stats.success_rate) }}</div>
          </div>
        </div>

        <div class="metric-card danger">
          <div class="metric-icon">
            <i class="fas fa-times-circle"></i>
          </div>
          <div class="metric-content">
            <div class="metric-value">{{ stats.failed_logs }}</div>
            <div class="metric-label">处理失败</div>
            <div class="metric-rate">{{ formatSuccessRate(1 - stats.success_rate) }}</div>
          </div>
        </div>

        <div class="metric-card warning">
          <div class="metric-icon">
            <i class="fas fa-redo"></i>
          </div>
          <div class="metric-content">
            <div class="metric-value">{{ stats.total_retries }}</div>
            <div class="metric-label">总重试次数</div>
            <div class="metric-rate">平均 {{ stats.average_retries.toFixed(1) }}</div>
          </div>
        </div>

        <div class="metric-card info">
          <div class="metric-icon">
            <i class="fas fa-filter"></i>
          </div>
          <div class="metric-content">
            <div class="metric-value">{{ stats.thoughts_filtered }}</div>
            <div class="metric-label">思考过滤</div>
            <div class="metric-rate">{{ formatSuccessRate(stats.filter_rate) }}</div>
          </div>
        </div>
      </div>

      <!-- 性能指标 -->
      <div class="performance-section">
        <h4 class="section-title">性能指标</h4>
        <div class="performance-grid">
          <div class="performance-item">
            <span class="performance-label">平均响应时间</span>
            <span class="performance-value">{{ formatDuration(stats.average_duration_ms) }}</span>
          </div>
          <div class="performance-item">
            <span class="performance-label">最大响应时间</span>
            <span class="performance-value">{{ formatDuration(stats.max_duration_ms) }}</span>
          </div>
          <div class="performance-item">
            <span class="performance-label">最小响应时间</span>
            <span class="performance-value">{{ formatDuration(stats.min_duration_ms) }}</span>
          </div>
          <div class="performance-item">
            <span class="performance-label">最大重试次数</span>
            <span class="performance-value">{{ stats.max_retries }}</span>
          </div>
        </div>
      </div>

      <!-- 中断原因分析 -->
      <div class="interruption-section" v-if="hasInterruptions">
        <h4 class="section-title">中断原因分析</h4>
        <div class="interruption-list">
          <div 
            v-for="[reason, count] in sortedInterruptions" 
            :key="reason"
            class="interruption-item"
          >
            <div class="interruption-info">
              <span class="interruption-reason">{{ formatInterruptReason(reason) }}</span>
              <span class="interruption-count">{{ count }} 次</span>
            </div>
            <div class="interruption-bar">
              <div 
                class="interruption-fill"
                :style="{ width: getInterruptionPercentage(count) + '%' }"
              ></div>
            </div>
          </div>
        </div>
      </div>

      <!-- 时间范围 -->
      <div class="time-range">
        <i class="fas fa-calendar-alt"></i>
        <span>统计时间：{{ formatDateRange(stats.start_time, stats.end_time) }}</span>
      </div>

      <!-- 操作按钮 -->
      <div class="card-actions">
        <el-button 
          type="danger" 
          size="small"
          @click="$emit('reset')"
        >
          <i class="fas fa-trash"></i>
          重置统计
        </el-button>
      </div>
    </div>

    <div class="card-content" v-else-if="loading">
      <div class="loading-state">
        <i class="fas fa-spinner fa-spin"></i>
        <p>加载统计数据中...</p>
      </div>
    </div>

    <div class="card-content" v-else>
      <div class="error-state">
        <i class="fas fa-exclamation-triangle"></i>
        <p>统计数据加载失败</p>
        <el-button type="primary" @click="$emit('refresh')">重试</el-button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { 
  formatSuccessRate, 
  formatDuration, 
  formatInterruptReason,
  type GeminiStats 
} from '@/api/gemini'

// Props
interface Props {
  stats: GeminiStats | null
  loading: boolean
}

const props = defineProps<Props>()

// Emits
const emit = defineEmits<{
  refresh: []
  reset: []
}>()

// 响应式数据
const selectedDays = ref(7)

// 计算属性
const hasInterruptions = computed(() => {
  return props.stats && Object.keys(props.stats.interruption_stats).length > 0
})

const sortedInterruptions = computed(() => {
  if (!props.stats) return []
  
  return Object.entries(props.stats.interruption_stats)
    .sort(([, a], [, b]) => b - a)
})

const maxInterruptions = computed(() => {
  if (!props.stats) return 0
  
  return Math.max(...Object.values(props.stats.interruption_stats))
})

// 方法
const handleDaysChange = (days: number) => {
  selectedDays.value = days
  emit('refresh')
}

const getInterruptionPercentage = (count: number): number => {
  if (maxInterruptions.value === 0) return 0
  return (count / maxInterruptions.value) * 100
}

const formatDateRange = (startTime: string, endTime: string): string => {
  const start = new Date(startTime).toLocaleDateString('zh-CN')
  const end = new Date(endTime).toLocaleDateString('zh-CN')
  return `${start} - ${end}`
}
</script>

<style scoped>
.stats-card {
  background: white;
  border-radius: 8px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
  overflow: hidden;
}

.card-header {
  padding: 20px;
  border-bottom: 1px solid #e5e7eb;
  display: flex;
  justify-content: space-between;
  align-items: center;
  background: #f9fafb;
}

.card-title {
  font-size: 18px;
  font-weight: 600;
  color: #1f2937;
  margin: 0;
  display: flex;
  align-items: center;
  gap: 8px;
}

.card-title i {
  color: #6366f1;
}

.header-actions {
  display: flex;
  gap: 12px;
  align-items: center;
}

.card-content {
  padding: 24px;
}

.metrics-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 16px;
  margin-bottom: 32px;
}

.metric-card {
  padding: 20px;
  border-radius: 8px;
  display: flex;
  align-items: center;
  gap: 16px;
}

.metric-card.success {
  background: linear-gradient(135deg, #10b981, #059669);
  color: white;
}

.metric-card.danger {
  background: linear-gradient(135deg, #ef4444, #dc2626);
  color: white;
}

.metric-card.warning {
  background: linear-gradient(135deg, #f59e0b, #d97706);
  color: white;
}

.metric-card.info {
  background: linear-gradient(135deg, #3b82f6, #2563eb);
  color: white;
}

.metric-icon {
  font-size: 32px;
  opacity: 0.8;
}

.metric-content {
  flex: 1;
}

.metric-value {
  font-size: 28px;
  font-weight: 700;
  line-height: 1;
  margin-bottom: 4px;
}

.metric-label {
  font-size: 14px;
  opacity: 0.9;
  margin-bottom: 2px;
}

.metric-rate {
  font-size: 12px;
  opacity: 0.8;
}

.performance-section,
.interruption-section {
  margin-bottom: 24px;
}

.section-title {
  font-size: 16px;
  font-weight: 600;
  color: #374151;
  margin: 0 0 16px 0;
  padding-bottom: 8px;
  border-bottom: 2px solid #e5e7eb;
}

.performance-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 16px;
}

.performance-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  background: #f9fafb;
  border-radius: 6px;
}

.performance-label {
  font-size: 14px;
  color: #6b7280;
}

.performance-value {
  font-size: 16px;
  font-weight: 600;
  color: #1f2937;
}

.interruption-list {
  space-y: 12px;
}

.interruption-item {
  margin-bottom: 12px;
}

.interruption-info {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 6px;
}

.interruption-reason {
  font-size: 14px;
  color: #374151;
  font-weight: 500;
}

.interruption-count {
  font-size: 14px;
  color: #6b7280;
}

.interruption-bar {
  height: 6px;
  background: #e5e7eb;
  border-radius: 3px;
  overflow: hidden;
}

.interruption-fill {
  height: 100%;
  background: linear-gradient(90deg, #ef4444, #dc2626);
  transition: width 0.3s ease;
}

.time-range {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  color: #6b7280;
  margin-bottom: 24px;
  padding: 12px 16px;
  background: #f9fafb;
  border-radius: 6px;
}

.time-range i {
  color: #6366f1;
}

.card-actions {
  display: flex;
  justify-content: flex-end;
  padding-top: 16px;
  border-top: 1px solid #e5e7eb;
}

.loading-state,
.error-state {
  text-align: center;
  padding: 40px 20px;
}

.loading-state i {
  font-size: 32px;
  color: #6366f1;
  margin-bottom: 16px;
}

.error-state i {
  font-size: 32px;
  color: #ef4444;
  margin-bottom: 16px;
}

.loading-state p,
.error-state p {
  font-size: 16px;
  color: #6b7280;
  margin: 0 0 16px 0;
}

@media (max-width: 768px) {
  .metrics-grid {
    grid-template-columns: 1fr;
  }
  
  .performance-grid {
    grid-template-columns: 1fr;
  }
  
  .header-actions {
    flex-direction: column;
    gap: 8px;
  }
}
</style>
