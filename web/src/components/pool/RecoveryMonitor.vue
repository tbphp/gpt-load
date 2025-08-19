<script setup lang="ts">
import type { Group, RecoveryMetrics } from "@/types/models";
import { 
  TrendingUpOutline,
  TimeOutline,
  CheckmarkCircleOutline,
  CloseCircleOutline,
  BarChartOutline,
  RefreshOutline
} from "@vicons/ionicons5";
import {
  NCard,
  NGrid,
  NGridItem,
  NIcon,
  NProgress,
  NStatistic,
  NTag,
  NButton,
  NSpace,
  NTooltip,
  NTable,
  NEmpty,
} from "naive-ui";
import { computed } from "vue";

interface Props {
  group: Group | null;
  recoveryMetrics: RecoveryMetrics | null;
}

interface Emits {
  (e: "refresh"): void;
}

const props = defineProps<Props>();
const emit = defineEmits<Emits>();

// 计算成功率颜色
const successRateColor = computed(() => {
  if (!props.recoveryMetrics) return "default";
  
  const rate = props.recoveryMetrics.overall_success_rate;
  if (rate >= 0.9) return "success";
  if (rate >= 0.7) return "warning";
  return "error";
});

// 格式化百分比
function formatPercentage(value: number): string {
  return `${(value * 100).toFixed(1)}%`;
}

// 格式化时间
function formatTime(timeStr?: string): string {
  if (!timeStr) return "无";
  return new Date(timeStr).toLocaleString("zh-CN");
}

// 格式化延迟
function formatLatency(ms: number): string {
  if (ms < 1000) return `${ms.toFixed(0)}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

// 小时统计表格列
const hourlyColumns = [
  {
    title: "时间",
    key: "hour",
    render: (row: any) => new Date(row.hour).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" }),
  },
  {
    title: "尝试次数",
    key: "attempts",
  },
  {
    title: "成功次数",
    key: "successes",
  },
  {
    title: "成功率",
    key: "success_rate",
    render: (row: any) => formatPercentage(row.success_rate),
  },
];
</script>

<template>
  <n-card class="modern-card recovery-monitor-card" title="恢复监控">
    <template #header-extra>
      <n-button size="small" @click="emit('refresh')">
        <template #icon>
          <n-icon :component="RefreshOutline" />
        </template>
        刷新
      </n-button>
    </template>

    <div v-if="!recoveryMetrics" class="loading-state">
      <n-empty description="暂无恢复数据" />
    </div>

    <div v-else class="recovery-monitor-content">
      <!-- 核心指标 -->
      <div class="core-metrics">
        <n-grid :cols="4" :x-gap="16" :y-gap="16">
          <!-- 总恢复尝试 -->
          <n-grid-item>
            <div class="metric-card">
              <div class="metric-header">
                <n-icon size="18" color="#3b82f6">
                  <BarChartOutline />
                </n-icon>
                <span class="metric-title">总尝试</span>
              </div>
              <div class="metric-value">{{ recoveryMetrics.total_recovery_attempts }}</div>
            </div>
          </n-grid-item>

          <!-- 成功恢复 -->
          <n-grid-item>
            <div class="metric-card">
              <div class="metric-header">
                <n-icon size="18" color="#10b981">
                  <CheckmarkCircleOutline />
                </n-icon>
                <span class="metric-title">成功恢复</span>
              </div>
              <div class="metric-value">{{ recoveryMetrics.successful_recoveries }}</div>
            </div>
          </n-grid-item>

          <!-- 失败恢复 -->
          <n-grid-item>
            <div class="metric-card">
              <div class="metric-header">
                <n-icon size="18" color="#ef4444">
                  <CloseCircleOutline />
                </n-icon>
                <span class="metric-title">失败恢复</span>
              </div>
              <div class="metric-value">{{ recoveryMetrics.failed_recoveries }}</div>
            </div>
          </n-grid-item>

          <!-- 平均延迟 -->
          <n-grid-item>
            <div class="metric-card">
              <div class="metric-header">
                <n-icon size="18" color="#f59e0b">
                  <TimeOutline />
                </n-icon>
                <span class="metric-title">平均延迟</span>
              </div>
              <div class="metric-value">{{ formatLatency(recoveryMetrics.avg_recovery_latency) }}</div>
            </div>
          </n-grid-item>
        </n-grid>
      </div>

      <!-- 成功率统计 -->
      <div class="success-rate-section">
        <n-grid :cols="2" :x-gap="16">
          <!-- 总体成功率 -->
          <n-grid-item>
            <div class="rate-card overall-rate">
              <div class="rate-header">
                <span class="rate-title">总体成功率</span>
                <n-tag :type="successRateColor" round>
                  {{ formatPercentage(recoveryMetrics.overall_success_rate) }}
                </n-tag>
              </div>
              <n-progress
                type="circle"
                :percentage="recoveryMetrics.overall_success_rate * 100"
                :color="successRateColor === 'success' ? '#10b981' : 
                       successRateColor === 'warning' ? '#f59e0b' : '#ef4444'"
                :stroke-width="8"
                style="margin-top: 12px"
              />
            </div>
          </n-grid-item>

          <!-- 最近成功率 -->
          <n-grid-item>
            <div class="rate-card recent-rate">
              <div class="rate-header">
                <span class="rate-title">最近成功率</span>
                <span class="rate-subtitle">(最近1小时)</span>
              </div>
              <div class="rate-value">
                {{ formatPercentage(recoveryMetrics.recent_success_rate) }}
              </div>
              <n-progress
                type="line"
                :percentage="recoveryMetrics.recent_success_rate * 100"
                :color="recoveryMetrics.recent_success_rate >= 0.8 ? '#10b981' : 
                       recoveryMetrics.recent_success_rate >= 0.6 ? '#f59e0b' : '#ef4444'"
                :show-indicator="false"
                :height="6"
                style="margin-top: 8px"
              />
            </div>
          </n-grid-item>
        </n-grid>
      </div>

      <!-- 频率统计 -->
      <div class="frequency-stats">
        <div class="stats-header">
          <h4 class="stats-title">恢复频率</h4>
        </div>
        <n-grid :cols="2" :x-gap="16">
          <n-grid-item>
            <n-statistic 
              label="每小时恢复次数" 
              :value="recoveryMetrics.recoveries_per_hour.toFixed(1)" 
            />
          </n-grid-item>
          <n-grid-item>
            <n-statistic 
              label="最后恢复时间" 
              :value="formatTime(recoveryMetrics.last_recovery_at)" 
            />
          </n-grid-item>
        </n-grid>
      </div>

      <!-- 错误统计 -->
      <div v-if="Object.keys(recoveryMetrics.error_stats).length > 0" class="error-stats">
        <div class="stats-header">
          <h4 class="stats-title">错误统计</h4>
        </div>
        <div class="error-list">
          <div 
            v-for="(count, errorType) in recoveryMetrics.error_stats" 
            :key="errorType"
            class="error-item"
          >
            <span class="error-type">{{ errorType }}</span>
            <n-tag type="error" size="small">{{ count }} 次</n-tag>
          </div>
        </div>
      </div>

      <!-- 小时统计 -->
      <div v-if="recoveryMetrics.hourly_stats.length > 0" class="hourly-stats">
        <div class="stats-header">
          <h4 class="stats-title">小时统计</h4>
          <span class="stats-subtitle">最近24小时恢复情况</span>
        </div>
        <n-table 
          :columns="hourlyColumns" 
          :data="recoveryMetrics.hourly_stats.slice(-12)" 
          size="small"
          :bordered="false"
          :single-line="false"
        />
      </div>
    </div>
  </n-card>
</template>

<style scoped>
.recovery-monitor-card {
  height: 100%;
}

.loading-state {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 200px;
}

.recovery-monitor-content {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.core-metrics {
  margin-bottom: 8px;
}

.metric-card {
  padding: 16px;
  border-radius: var(--border-radius-md);
  background: rgba(255, 255, 255, 0.5);
  border: 1px solid rgba(0, 0, 0, 0.05);
  transition: all 0.2s ease;
  text-align: center;
}

.metric-card:hover {
  background: rgba(255, 255, 255, 0.8);
  transform: translateY(-1px);
  box-shadow: var(--shadow-md);
}

.metric-header {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  margin-bottom: 8px;
}

.metric-title {
  font-size: 0.875rem;
  font-weight: 600;
  color: #374151;
}

.metric-value {
  font-size: 1.5rem;
  font-weight: 700;
  color: #1f2937;
}

.success-rate-section {
  padding: 16px;
  background: rgba(102, 126, 234, 0.05);
  border-radius: var(--border-radius-md);
  border: 1px solid rgba(102, 126, 234, 0.1);
}

.rate-card {
  padding: 16px;
  background: rgba(255, 255, 255, 0.8);
  border-radius: var(--border-radius-md);
  text-align: center;
}

.rate-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}

.rate-title {
  font-size: 0.875rem;
  font-weight: 600;
  color: #374151;
}

.rate-subtitle {
  font-size: 0.75rem;
  color: #6b7280;
}

.rate-value {
  font-size: 2rem;
  font-weight: 700;
  color: #1f2937;
  margin: 12px 0;
}

.frequency-stats,
.error-stats,
.hourly-stats {
  padding: 16px;
  background: rgba(255, 255, 255, 0.5);
  border-radius: var(--border-radius-md);
  border: 1px solid rgba(0, 0, 0, 0.05);
}

.stats-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}

.stats-title {
  font-size: 1rem;
  font-weight: 600;
  color: #374151;
  margin: 0;
}

.stats-subtitle {
  font-size: 0.8rem;
  color: #6b7280;
}

.error-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.error-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  background: rgba(239, 68, 68, 0.05);
  border-radius: var(--border-radius-sm);
  border: 1px solid rgba(239, 68, 68, 0.1);
}

.error-type {
  font-size: 0.875rem;
  color: #374151;
  font-weight: 500;
}

/* 响应式设计 */
@media (max-width: 768px) {
  .core-metrics :deep(.n-grid) {
    grid-template-columns: repeat(2, 1fr);
  }
  
  .success-rate-section :deep(.n-grid) {
    grid-template-columns: 1fr;
  }
  
  .frequency-stats :deep(.n-grid) {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 480px) {
  .core-metrics :deep(.n-grid) {
    grid-template-columns: 1fr;
  }
  
  .rate-header {
    flex-direction: column;
    gap: 4px;
  }
}
</style>
