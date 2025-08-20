<template>
  <div class="logs-card">
    <div class="card-header">
      <h3 class="card-title">
        <i class="fas fa-list-alt"></i>
        最近日志
      </h3>
      <div class="header-actions">
        <el-button 
          type="primary" 
          size="small" 
          :loading="loading"
          @click="$emit('refresh')"
        >
          <i class="fas fa-sync-alt"></i>
          刷新
        </el-button>
        <el-button 
          type="default" 
          size="small"
          @click="$emit('view-all')"
        >
          <i class="fas fa-external-link-alt"></i>
          查看全部
        </el-button>
      </div>
    </div>

    <div class="card-content" v-if="logs && logs.length > 0">
      <div class="logs-list">
        <div 
          v-for="log in logs" 
          :key="log.id"
          class="log-item"
          :class="{ 'log-failed': !log.final_success }"
        >
          <div class="log-header">
            <div class="log-status">
              <i 
                :class="getStatusIcon(log.final_success)"
                :style="{ color: getStatusColor(log.final_success) }"
              ></i>
              <span class="log-group">{{ log.group_name }}</span>
            </div>
            <div class="log-time">
              {{ formatTime(log.created_at) }}
            </div>
          </div>

          <div class="log-details">
            <div class="log-info">
              <span class="log-label">请求ID:</span>
              <span class="log-value">{{ log.request_id.substring(0, 8) }}...</span>
            </div>
            
            <div class="log-info" v-if="log.retry_count > 0">
              <span class="log-label">重试次数:</span>
              <span class="log-value retry-count">{{ log.retry_count }}</span>
            </div>
            
            <div class="log-info" v-if="log.interrupt_reason">
              <span class="log-label">中断原因:</span>
              <span class="log-value interrupt-reason">
                {{ formatInterruptReason(log.interrupt_reason) }}
              </span>
            </div>
            
            <div class="log-info">
              <span class="log-label">响应时间:</span>
              <span class="log-value">{{ formatDuration(log.total_duration_ms) }}</span>
            </div>
            
            <div class="log-info">
              <span class="log-label">输出字符:</span>
              <span class="log-value">{{ log.output_chars.toLocaleString() }}</span>
            </div>
            
            <div class="log-info" v-if="log.thought_filtered">
              <span class="log-label">思考过滤:</span>
              <span class="log-value thought-filtered">
                <i class="fas fa-filter"></i>
                已过滤
              </span>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div class="card-content" v-else-if="logs && logs.length === 0">
      <div class="empty-state">
        <i class="fas fa-inbox"></i>
        <p>暂无日志记录</p>
        <el-button type="primary" @click="$emit('refresh')">刷新</el-button>
      </div>
    </div>

    <div class="card-content" v-else-if="loading">
      <div class="loading-state">
        <i class="fas fa-spinner fa-spin"></i>
        <p>加载日志中...</p>
      </div>
    </div>

    <div class="card-content" v-else>
      <div class="error-state">
        <i class="fas fa-exclamation-triangle"></i>
        <p>日志加载失败</p>
        <el-button type="primary" @click="$emit('refresh')">重试</el-button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { 
  formatDuration, 
  formatInterruptReason,
  type GeminiLogSummary 
} from '@/api/gemini'

// Props
interface Props {
  logs: GeminiLogSummary[] | null
  loading: boolean
}

const props = defineProps<Props>()

// Emits
const emit = defineEmits<{
  refresh: []
  'view-all': []
}>()

// 方法
const getStatusIcon = (success: boolean): string => {
  return success ? 'fas fa-check-circle' : 'fas fa-times-circle'
}

const getStatusColor = (success: boolean): string => {
  return success ? '#10b981' : '#ef4444'
}

const formatTime = (timestamp: string): string => {
  const date = new Date(timestamp)
  const now = new Date()
  const diff = now.getTime() - date.getTime()
  
  // 小于1分钟
  if (diff < 60000) {
    return '刚刚'
  }
  
  // 小于1小时
  if (diff < 3600000) {
    const minutes = Math.floor(diff / 60000)
    return `${minutes}分钟前`
  }
  
  // 小于24小时
  if (diff < 86400000) {
    const hours = Math.floor(diff / 3600000)
    return `${hours}小时前`
  }
  
  // 超过24小时，显示具体日期
  return date.toLocaleDateString('zh-CN', {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit'
  })
}
</script>

<style scoped>
.logs-card {
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
  gap: 8px;
}

.card-content {
  padding: 0;
}

.logs-list {
  max-height: 500px;
  overflow-y: auto;
}

.log-item {
  padding: 16px 20px;
  border-bottom: 1px solid #f3f4f6;
  transition: background-color 0.2s ease;
}

.log-item:hover {
  background: #f9fafb;
}

.log-item:last-child {
  border-bottom: none;
}

.log-failed {
  background: #fef2f2;
}

.log-failed:hover {
  background: #fee2e2;
}

.log-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}

.log-status {
  display: flex;
  align-items: center;
  gap: 8px;
}

.log-status i {
  font-size: 16px;
}

.log-group {
  font-weight: 600;
  color: #1f2937;
}

.log-time {
  font-size: 12px;
  color: #6b7280;
}

.log-details {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 8px;
}

.log-info {
  display: flex;
  align-items: center;
  gap: 4px;
}

.log-label {
  font-size: 12px;
  color: #6b7280;
  min-width: 60px;
}

.log-value {
  font-size: 12px;
  color: #374151;
  font-weight: 500;
}

.retry-count {
  color: #f59e0b;
  font-weight: 600;
}

.interrupt-reason {
  color: #ef4444;
}

.thought-filtered {
  color: #3b82f6;
  display: flex;
  align-items: center;
  gap: 4px;
}

.thought-filtered i {
  font-size: 10px;
}

.empty-state,
.loading-state,
.error-state {
  text-align: center;
  padding: 40px 20px;
}

.empty-state i {
  font-size: 48px;
  color: #d1d5db;
  margin-bottom: 16px;
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

.empty-state p,
.loading-state p,
.error-state p {
  font-size: 16px;
  color: #6b7280;
  margin: 0 0 16px 0;
}

/* 滚动条样式 */
.logs-list::-webkit-scrollbar {
  width: 6px;
}

.logs-list::-webkit-scrollbar-track {
  background: #f1f5f9;
}

.logs-list::-webkit-scrollbar-thumb {
  background: #cbd5e1;
  border-radius: 3px;
}

.logs-list::-webkit-scrollbar-thumb:hover {
  background: #94a3b8;
}

@media (max-width: 768px) {
  .log-details {
    grid-template-columns: 1fr;
    gap: 4px;
  }
  
  .log-header {
    flex-direction: column;
    align-items: flex-start;
    gap: 8px;
  }
  
  .header-actions {
    flex-direction: column;
    gap: 4px;
  }
}
</style>
