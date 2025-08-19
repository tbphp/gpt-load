<script setup lang="ts">
import type { Group, PoolStatsResponse } from "@/types/models";
import { 
  BarChartOutline,
  TrendingUpOutline,
  SpeedometerOutline,
  AlertCircleOutline,
  RefreshOutline,
  SettingsOutline
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
  useMessage,
} from "naive-ui";
import { computed } from "vue";

interface Props {
  group: Group | null;
  poolStats: PoolStatsResponse | null;
}

interface Emits {
  (e: "action", action: string, data?: any): void;
}

const props = defineProps<Props>();
const emit = defineEmits<Emits>();
const message = useMessage();

// 计算性能指标颜色
const performanceColors = computed(() => {
  if (!props.poolStats?.performance_metrics) {
    return {
      errorRate: "default",
      cacheHitRate: "default",
    };
  }

  const metrics = props.poolStats.performance_metrics;
  
  return {
    errorRate: metrics.error_rate < 0.05 ? "success" : metrics.error_rate < 0.1 ? "warning" : "error",
    cacheHitRate: metrics.cache_hit_rate > 0.8 ? "success" : metrics.cache_hit_rate > 0.6 ? "warning" : "error",
  };
});

// 格式化百分比
function formatPercentage(value: number): string {
  return `${(value * 100).toFixed(1)}%`;
}

// 格式化数字
function formatNumber(value: number): string {
  if (value >= 1000000) {
    return `${(value / 1000000).toFixed(1)}M`;
  } else if (value >= 1000) {
    return `${(value / 1000).toFixed(1)}K`;
  }
  return value.toString();
}

// 处理池重填
function handleRefillPools() {
  emit("action", "pool_refill");
}

// 处理配置优化
function handleOptimizeConfig() {
  emit("action", "optimize_config");
}
</script>

<template>
  <n-card class="modern-card pool-stats-card" title="性能统计">
    <template #header-extra>
      <n-space :size="8">
        <n-tooltip trigger="hover">
          <template #trigger>
            <n-button size="small" @click="handleOptimizeConfig">
              <template #icon>
                <n-icon :component="SettingsOutline" />
              </template>
            </n-button>
          </template>
          优化配置
        </n-tooltip>
        
        <n-tooltip trigger="hover">
          <template #trigger>
            <n-button size="small" type="primary" @click="handleRefillPools">
              <template #icon>
                <n-icon :component="RefreshOutline" />
              </template>
            </n-button>
          </template>
          重填池
        </n-tooltip>
      </n-space>
    </template>

    <div v-if="!poolStats" class="loading-state">
      <p>暂无数据</p>
    </div>

    <div v-else class="pool-stats-content">
      <!-- 核心性能指标 -->
      <div class="performance-metrics">
        <n-grid :cols="2" :x-gap="16" :y-gap="16">
          <!-- 吞吐量 -->
          <n-grid-item>
            <div class="metric-card throughput-metric">
              <div class="metric-header">
                <n-icon size="18" color="#3b82f6">
                  <SpeedometerOutline />
                </n-icon>
                <span class="metric-title">吞吐量</span>
              </div>
              <div class="metric-value">
                {{ formatNumber(poolStats.performance_metrics.throughput) }}
                <span class="metric-unit">req/s</span>
              </div>
              <div class="metric-trend">
                <n-icon size="14" color="#10b981">
                  <TrendingUpOutline />
                </n-icon>
                <span class="trend-text">实时</span>
              </div>
            </div>
          </n-grid-item>

          <!-- 平均延迟 -->
          <n-grid-item>
            <div class="metric-card latency-metric">
              <div class="metric-header">
                <n-icon size="18" color="#f59e0b">
                  <BarChartOutline />
                </n-icon>
                <span class="metric-title">平均延迟</span>
              </div>
              <div class="metric-value">
                {{ poolStats.performance_metrics.avg_latency.toFixed(1) }}
                <span class="metric-unit">ms</span>
              </div>
              <div class="metric-trend">
                <span class="trend-text">响应时间</span>
              </div>
            </div>
          </n-grid-item>

          <!-- 错误率 -->
          <n-grid-item>
            <div class="metric-card error-metric">
              <div class="metric-header">
                <n-icon size="18" color="#ef4444">
                  <AlertCircleOutline />
                </n-icon>
                <span class="metric-title">错误率</span>
              </div>
              <div class="metric-value">
                {{ formatPercentage(poolStats.performance_metrics.error_rate) }}
              </div>
              <n-progress
                type="line"
                :percentage="poolStats.performance_metrics.error_rate * 100"
                :color="performanceColors.errorRate === 'success' ? '#10b981' : 
                       performanceColors.errorRate === 'warning' ? '#f59e0b' : '#ef4444'"
                :show-indicator="false"
                :height="4"
                style="margin-top: 8px"
              />
            </div>
          </n-grid-item>

          <!-- 缓存命中率 -->
          <n-grid-item>
            <div class="metric-card cache-metric">
              <div class="metric-header">
                <n-icon size="18" color="#8b5cf6">
                  <SpeedometerOutline />
                </n-icon>
                <span class="metric-title">缓存命中率</span>
              </div>
              <div class="metric-value">
                {{ formatPercentage(poolStats.performance_metrics.cache_hit_rate) }}
              </div>
              <n-progress
                type="line"
                :percentage="poolStats.performance_metrics.cache_hit_rate * 100"
                :color="performanceColors.cacheHitRate === 'success' ? '#10b981' : 
                       performanceColors.cacheHitRate === 'warning' ? '#f59e0b' : '#ef4444'"
                :show-indicator="false"
                :height="4"
                style="margin-top: 8px"
              />
            </div>
          </n-grid-item>
        </n-grid>
      </div>

      <!-- 池分布图表 -->
      <div class="pool-distribution">
        <div class="distribution-header">
          <h4 class="distribution-title">池分布</h4>
          <div class="distribution-total">
            总计: {{ poolStats.pool_stats.total_keys }} 个密钥
          </div>
        </div>
        
        <div class="distribution-chart">
          <div class="chart-bar">
            <!-- 验证池 -->
            <div 
              class="bar-segment validation-segment"
              :style="{ 
                width: `${(poolStats.pool_stats.validation_pool / poolStats.pool_stats.total_keys) * 100}%` 
              }"
            >
              <n-tooltip trigger="hover">
                <template #trigger>
                  <div class="segment-content"></div>
                </template>
                验证池: {{ poolStats.pool_stats.validation_pool }} 个
              </n-tooltip>
            </div>
            
            <!-- 就绪池 -->
            <div 
              class="bar-segment ready-segment"
              :style="{ 
                width: `${(poolStats.pool_stats.ready_pool / poolStats.pool_stats.total_keys) * 100}%` 
              }"
            >
              <n-tooltip trigger="hover">
                <template #trigger>
                  <div class="segment-content"></div>
                </template>
                就绪池: {{ poolStats.pool_stats.ready_pool }} 个
              </n-tooltip>
            </div>
            
            <!-- 活跃池 -->
            <div 
              class="bar-segment active-segment"
              :style="{ 
                width: `${(poolStats.pool_stats.active_pool / poolStats.pool_stats.total_keys) * 100}%` 
              }"
            >
              <n-tooltip trigger="hover">
                <template #trigger>
                  <div class="segment-content"></div>
                </template>
                活跃池: {{ poolStats.pool_stats.active_pool }} 个
              </n-tooltip>
            </div>
            
            <!-- 冷却池 -->
            <div 
              class="bar-segment cooling-segment"
              :style="{ 
                width: `${(poolStats.pool_stats.cooling_pool / poolStats.pool_stats.total_keys) * 100}%` 
              }"
            >
              <n-tooltip trigger="hover">
                <template #trigger>
                  <div class="segment-content"></div>
                </template>
                冷却池: {{ poolStats.pool_stats.cooling_pool }} 个
              </n-tooltip>
            </div>
          </div>
          
          <!-- 图例 -->
          <div class="chart-legend">
            <div class="legend-item">
              <div class="legend-color validation-color"></div>
              <span class="legend-text">验证池</span>
            </div>
            <div class="legend-item">
              <div class="legend-color ready-color"></div>
              <span class="legend-text">就绪池</span>
            </div>
            <div class="legend-item">
              <div class="legend-color active-color"></div>
              <span class="legend-text">活跃池</span>
            </div>
            <div class="legend-item">
              <div class="legend-color cooling-color"></div>
              <span class="legend-text">冷却池</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </n-card>
</template>

<style scoped>
.pool-stats-card {
  height: 100%;
}

.loading-state {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 200px;
  color: #6b7280;
}

.pool-stats-content {
  display: flex;
  flex-direction: column;
  gap: 24px;
}

.performance-metrics {
  margin-bottom: 8px;
}

.metric-card {
  padding: 16px;
  border-radius: var(--border-radius-md);
  background: rgba(255, 255, 255, 0.5);
  border: 1px solid rgba(0, 0, 0, 0.05);
  transition: all 0.2s ease;
}

.metric-card:hover {
  background: rgba(255, 255, 255, 0.8);
  transform: translateY(-1px);
  box-shadow: var(--shadow-md);
}

.metric-header {
  display: flex;
  align-items: center;
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
  margin-bottom: 4px;
}

.metric-unit {
  font-size: 0.875rem;
  font-weight: 500;
  color: #6b7280;
  margin-left: 4px;
}

.metric-trend {
  display: flex;
  align-items: center;
  gap: 4px;
}

.trend-text {
  font-size: 0.75rem;
  color: #6b7280;
}

.pool-distribution {
  padding: 16px;
  background: rgba(255, 255, 255, 0.5);
  border-radius: var(--border-radius-md);
  border: 1px solid rgba(0, 0, 0, 0.05);
}

.distribution-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}

.distribution-title {
  font-size: 1rem;
  font-weight: 600;
  color: #374151;
  margin: 0;
}

.distribution-total {
  font-size: 0.875rem;
  color: #6b7280;
  font-weight: 500;
}

.distribution-chart {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.chart-bar {
  display: flex;
  height: 24px;
  border-radius: 12px;
  overflow: hidden;
  background: #f3f4f6;
}

.bar-segment {
  height: 100%;
  transition: all 0.2s ease;
}

.segment-content {
  width: 100%;
  height: 100%;
  cursor: pointer;
}

.validation-segment {
  background: linear-gradient(135deg, #10b981, #059669);
}

.ready-segment {
  background: linear-gradient(135deg, #3b82f6, #2563eb);
}

.active-segment {
  background: linear-gradient(135deg, #f59e0b, #d97706);
}

.cooling-segment {
  background: linear-gradient(135deg, #ef4444, #dc2626);
}

.chart-legend {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  justify-content: center;
}

.legend-item {
  display: flex;
  align-items: center;
  gap: 6px;
}

.legend-color {
  width: 12px;
  height: 12px;
  border-radius: 2px;
}

.validation-color {
  background: #10b981;
}

.ready-color {
  background: #3b82f6;
}

.active-color {
  background: #f59e0b;
}

.cooling-color {
  background: #ef4444;
}

.legend-text {
  font-size: 0.8rem;
  color: #6b7280;
  font-weight: 500;
}

/* 响应式设计 */
@media (max-width: 640px) {
  .performance-metrics :deep(.n-grid) {
    grid-template-columns: 1fr;
  }
  
  .distribution-header {
    flex-direction: column;
    align-items: flex-start;
    gap: 8px;
  }
  
  .chart-legend {
    justify-content: flex-start;
    gap: 12px;
  }
}
</style>
