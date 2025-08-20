<template>
  <div class="gemini-page">
    <!-- 页面标题 -->
    <div class="page-header">
      <h1 class="page-title">
        <i class="fas fa-brain"></i>
        Gemini 智能防断流
      </h1>
      <p class="page-description">
        管理 Gemini 模型的智能流式重试和思考内容过滤功能
      </p>
    </div>

    <!-- 健康状态卡片 -->
    <div class="health-status-card" v-if="healthStatus">
      <div class="status-indicator" :class="getStatusClass(healthStatus.status)">
        <i :class="getStatusIcon(healthStatus.status)"></i>
        <span class="status-text">{{ getStatusText(healthStatus.status) }}</span>
      </div>
      <div class="health-metrics">
        <div class="metric">
          <span class="metric-label">成功率</span>
          <span class="metric-value">{{ formatSuccessRate(healthStatus.success_rate) }}</span>
        </div>
        <div class="metric">
          <span class="metric-label">24h处理量</span>
          <span class="metric-value">{{ healthStatus.total_logs }}</span>
        </div>
        <div class="metric">
          <span class="metric-label">平均重试</span>
          <span class="metric-value">{{ healthStatus.average_retries.toFixed(1) }}</span>
        </div>
        <div class="metric">
          <span class="metric-label">思考过滤</span>
          <span class="metric-value">{{ healthStatus.thoughts_filtered }}</span>
        </div>
      </div>
    </div>

    <!-- 主要内容区域 -->
    <div class="content-grid">
      <!-- 左侧配置区域 -->
      <div class="config-section">
        <GeminiConfigCard 
          :config="config"
          :loading="configLoading"
          @update="handleConfigUpdate"
          @refresh="loadConfig"
        />
      </div>

      <!-- 右侧监控区域 -->
      <div class="monitoring-section">
        <GeminiStatsCard 
          :stats="stats"
          :loading="statsLoading"
          @refresh="loadStats"
          @reset="handleStatsReset"
        />
        
        <GeminiLogsCard 
          :logs="recentLogs"
          :loading="logsLoading"
          @refresh="loadRecentLogs"
          @view-all="showLogsModal = true"
        />
      </div>
    </div>

    <!-- 详细日志模态框 -->
    <GeminiLogsModal 
      v-if="showLogsModal"
      @close="showLogsModal = false"
    />

    <!-- 加载状态 -->
    <div v-if="initialLoading" class="loading-overlay">
      <div class="loading-spinner">
        <i class="fas fa-spinner fa-spin"></i>
        <p>加载 Gemini 数据中...</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  getGeminiSettings,
  getGeminiStats,
  getRecentGeminiLogs,
  getGeminiHealth,
  resetGeminiStats,
  updateGeminiSettings,
  formatSuccessRate,
  getStatusColor,
  type GeminiConfig,
  type GeminiStats,
  type GeminiLogSummary,
  type GeminiHealth,
  type GeminiConfigUpdate
} from '@/api/gemini'
import GeminiConfigCard from '@/components/gemini/GeminiConfigCard.vue'
import GeminiStatsCard from '@/components/gemini/GeminiStatsCard.vue'
import GeminiLogsCard from '@/components/gemini/GeminiLogsCard.vue'
import GeminiLogsModal from '@/components/gemini/GeminiLogsModal.vue'

// 响应式数据
const initialLoading = ref(true)
const configLoading = ref(false)
const statsLoading = ref(false)
const logsLoading = ref(false)

const config = ref<GeminiConfig | null>(null)
const stats = ref<GeminiStats | null>(null)
const recentLogs = ref<GeminiLogSummary[]>([])
const healthStatus = ref<GeminiHealth | null>(null)

const showLogsModal = ref(false)

// 定时刷新
let refreshInterval: NodeJS.Timeout | null = null

// 页面挂载
onMounted(async () => {
  await loadAllData()
  initialLoading.value = false
  
  // 设置定时刷新（每30秒）
  refreshInterval = setInterval(() => {
    loadHealthStatus()
    loadRecentLogs()
  }, 30000)
})

// 页面卸载
onUnmounted(() => {
  if (refreshInterval) {
    clearInterval(refreshInterval)
  }
})

// 加载所有数据
const loadAllData = async () => {
  await Promise.all([
    loadConfig(),
    loadStats(),
    loadRecentLogs(),
    loadHealthStatus()
  ])
}

// 加载配置
const loadConfig = async () => {
  try {
    configLoading.value = true
    config.value = await getGeminiSettings()
  } catch (error) {
    console.error('Failed to load Gemini config:', error)
    ElMessage.error('加载 Gemini 配置失败')
  } finally {
    configLoading.value = false
  }
}

// 加载统计数据
const loadStats = async () => {
  try {
    statsLoading.value = true
    stats.value = await getGeminiStats(7) // 默认7天
  } catch (error) {
    console.error('Failed to load Gemini stats:', error)
    ElMessage.error('加载 Gemini 统计失败')
  } finally {
    statsLoading.value = false
  }
}

// 加载最近日志
const loadRecentLogs = async () => {
  try {
    logsLoading.value = true
    recentLogs.value = await getRecentGeminiLogs(10)
  } catch (error) {
    console.error('Failed to load recent logs:', error)
    ElMessage.error('加载最近日志失败')
  } finally {
    logsLoading.value = false
  }
}

// 加载健康状态
const loadHealthStatus = async () => {
  try {
    healthStatus.value = await getGeminiHealth()
  } catch (error) {
    console.error('Failed to load health status:', error)
  }
}

// 处理配置更新
const handleConfigUpdate = async (update: GeminiConfigUpdate) => {
  try {
    await updateGeminiSettings(update)
    ElMessage.success('Gemini 配置更新成功')
    await loadConfig()
    await loadHealthStatus()
  } catch (error) {
    console.error('Failed to update config:', error)
    ElMessage.error('更新 Gemini 配置失败')
  }
}

// 处理统计重置
const handleStatsReset = async () => {
  try {
    await ElMessageBox.confirm(
      '确定要重置 Gemini 统计数据吗？这将删除30天前的日志记录。',
      '确认重置',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )
    
    await resetGeminiStats(30)
    ElMessage.success('Gemini 统计数据重置成功')
    await loadStats()
    await loadRecentLogs()
    await loadHealthStatus()
  } catch (error) {
    if (error !== 'cancel') {
      console.error('Failed to reset stats:', error)
      ElMessage.error('重置统计数据失败')
    }
  }
}

// 获取状态样式类
const getStatusClass = (status: string) => {
  return `status-${getStatusColor(status)}`
}

// 获取状态图标
const getStatusIcon = (status: string) => {
  switch (status) {
    case 'healthy':
      return 'fas fa-check-circle'
    case 'degraded':
      return 'fas fa-exclamation-triangle'
    case 'unhealthy':
      return 'fas fa-times-circle'
    default:
      return 'fas fa-question-circle'
  }
}

// 获取状态文本
const getStatusText = (status: string) => {
  switch (status) {
    case 'healthy':
      return '健康'
    case 'degraded':
      return '降级'
    case 'unhealthy':
      return '不健康'
    default:
      return '未知'
  }
}
</script>

<style scoped>
.gemini-page {
  padding: 20px;
  max-width: 1400px;
  margin: 0 auto;
}

.page-header {
  margin-bottom: 24px;
}

.page-title {
  font-size: 28px;
  font-weight: 600;
  color: #1f2937;
  margin: 0 0 8px 0;
  display: flex;
  align-items: center;
  gap: 12px;
}

.page-title i {
  color: #6366f1;
}

.page-description {
  font-size: 16px;
  color: #6b7280;
  margin: 0;
}

.health-status-card {
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  border-radius: 12px;
  padding: 20px;
  margin-bottom: 24px;
  color: white;
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.status-indicator {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 18px;
  font-weight: 600;
}

.status-indicator i {
  font-size: 24px;
}

.status-success i {
  color: #10b981;
}

.status-warning i {
  color: #f59e0b;
}

.status-danger i {
  color: #ef4444;
}

.health-metrics {
  display: flex;
  gap: 32px;
}

.metric {
  text-align: center;
}

.metric-label {
  display: block;
  font-size: 14px;
  opacity: 0.8;
  margin-bottom: 4px;
}

.metric-value {
  display: block;
  font-size: 20px;
  font-weight: 600;
}

.content-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 24px;
}

.config-section,
.monitoring-section {
  display: flex;
  flex-direction: column;
  gap: 24px;
}

.loading-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(255, 255, 255, 0.8);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}

.loading-spinner {
  text-align: center;
}

.loading-spinner i {
  font-size: 48px;
  color: #6366f1;
  margin-bottom: 16px;
}

.loading-spinner p {
  font-size: 16px;
  color: #6b7280;
  margin: 0;
}

@media (max-width: 1024px) {
  .content-grid {
    grid-template-columns: 1fr;
  }
  
  .health-metrics {
    gap: 16px;
  }
  
  .metric-value {
    font-size: 18px;
  }
}

@media (max-width: 768px) {
  .gemini-page {
    padding: 16px;
  }
  
  .page-title {
    font-size: 24px;
  }
  
  .health-status-card {
    flex-direction: column;
    gap: 16px;
    text-align: center;
  }
  
  .health-metrics {
    gap: 12px;
  }
}
</style>
