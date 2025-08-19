<script setup lang="ts">
import type { Group, PoolStatsResponse } from "@/types/models";
import { 
  CheckmarkCircleOutline, 
  TimeOutline, 
  FlashOutline, 
  SnowOutline,
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
  NTooltip,
  NButton,
  NSpace,
} from "naive-ui";
import { computed } from "vue";

interface Props {
  group: Group | null;
  poolStats: PoolStatsResponse | null;
}

interface Emits {
  (e: "refresh"): void;
}

const props = defineProps<Props>();
const emit = defineEmits<Emits>();

// 计算池分布百分比
const poolDistribution = computed(() => {
  if (!props.poolStats?.pool_stats) {
    return {
      validation: 0,
      ready: 0,
      active: 0,
      cooling: 0,
    };
  }

  const stats = props.poolStats.pool_stats;
  const total = stats.total_keys || 1;

  return {
    validation: Math.round((stats.validation_pool / total) * 100),
    ready: Math.round((stats.ready_pool / total) * 100),
    active: Math.round((stats.active_pool / total) * 100),
    cooling: Math.round((stats.cooling_pool / total) * 100),
  };
});

// 健康状态颜色
const healthStatusColor = computed(() => {
  const status = props.poolStats?.pool_health?.status;
  switch (status) {
    case "healthy":
      return "success";
    case "warning":
      return "warning";
    case "critical":
      return "error";
    default:
      return "default";
  }
});

// 格式化时间
function formatTime(timeStr?: string) {
  if (!timeStr) return "未知";
  return new Date(timeStr).toLocaleString("zh-CN");
}
</script>

<template>
  <n-card class="modern-card pool-overview-card" title="池概览">
    <template #header-extra>
      <n-button size="small" @click="emit('refresh')">
        <template #icon>
          <n-icon :component="RefreshOutline" />
        </template>
        刷新
      </n-button>
    </template>

    <div v-if="!poolStats" class="loading-state">
      <p>暂无数据</p>
    </div>

    <div v-else class="pool-overview-content">
      <!-- 池统计网格 -->
      <n-grid :cols="2" :x-gap="16" :y-gap="16" class="pool-stats-grid">
        <!-- 验证池 -->
        <n-grid-item>
          <div class="pool-stat-item validation-pool">
            <div class="stat-header">
              <n-icon size="20" color="#10b981">
                <CheckmarkCircleOutline />
              </n-icon>
              <span class="stat-title">验证池</span>
            </div>
            <div class="stat-content">
              <div class="stat-number">{{ poolStats.pool_stats.validation_pool }}</div>
              <div class="stat-percentage">{{ poolDistribution.validation }}%</div>
            </div>
            <n-progress
              type="line"
              :percentage="poolDistribution.validation"
              color="#10b981"
              :show-indicator="false"
              :height="4"
            />
          </div>
        </n-grid-item>

        <!-- 就绪池 -->
        <n-grid-item>
          <div class="pool-stat-item ready-pool">
            <div class="stat-header">
              <n-icon size="20" color="#3b82f6">
                <FlashOutline />
              </n-icon>
              <span class="stat-title">就绪池</span>
            </div>
            <div class="stat-content">
              <div class="stat-number">{{ poolStats.pool_stats.ready_pool }}</div>
              <div class="stat-percentage">{{ poolDistribution.ready }}%</div>
            </div>
            <n-progress
              type="line"
              :percentage="poolDistribution.ready"
              color="#3b82f6"
              :show-indicator="false"
              :height="4"
            />
          </div>
        </n-grid-item>

        <!-- 活跃池 -->
        <n-grid-item>
          <div class="pool-stat-item active-pool">
            <div class="stat-header">
              <n-icon size="20" color="#f59e0b">
                <TimeOutline />
              </n-icon>
              <span class="stat-title">活跃池</span>
            </div>
            <div class="stat-content">
              <div class="stat-number">{{ poolStats.pool_stats.active_pool }}</div>
              <div class="stat-percentage">{{ poolDistribution.active }}%</div>
            </div>
            <n-progress
              type="line"
              :percentage="poolDistribution.active"
              color="#f59e0b"
              :show-indicator="false"
              :height="4"
            />
          </div>
        </n-grid-item>

        <!-- 冷却池 -->
        <n-grid-item>
          <div class="pool-stat-item cooling-pool">
            <div class="stat-header">
              <n-icon size="20" color="#ef4444">
                <SnowOutline />
              </n-icon>
              <span class="stat-title">冷却池</span>
            </div>
            <div class="stat-content">
              <div class="stat-number">{{ poolStats.pool_stats.cooling_pool }}</div>
              <div class="stat-percentage">{{ poolDistribution.cooling }}%</div>
            </div>
            <n-progress
              type="line"
              :percentage="poolDistribution.cooling"
              color="#ef4444"
              :show-indicator="false"
              :height="4"
            />
          </div>
        </n-grid-item>
      </n-grid>

      <!-- 总体统计 -->
      <div class="overall-stats">
        <n-grid :cols="3" :x-gap="16">
          <n-grid-item>
            <n-statistic label="总密钥数" :value="poolStats.pool_stats.total_keys" />
          </n-grid-item>
          <n-grid-item>
            <n-statistic 
              label="吞吐量" 
              :value="poolStats.performance_metrics.throughput" 
              suffix="req/s"
            />
          </n-grid-item>
          <n-grid-item>
            <n-statistic 
              label="平均延迟" 
              :value="poolStats.performance_metrics.avg_latency" 
              suffix="ms"
            />
          </n-grid-item>
        </n-grid>
      </div>

      <!-- 健康状态 -->
      <div class="health-status">
        <div class="health-header">
          <span class="health-title">池健康状态</span>
          <n-tag :type="healthStatusColor" round>
            {{ poolStats.pool_health.status === 'healthy' ? '健康' : 
               poolStats.pool_health.status === 'warning' ? '警告' : '严重' }}
          </n-tag>
        </div>
        
        <div v-if="poolStats.pool_health.issues.length > 0" class="health-issues">
          <div class="issues-title">发现的问题：</div>
          <ul class="issues-list">
            <li v-for="issue in poolStats.pool_health.issues" :key="issue">
              {{ issue }}
            </li>
          </ul>
        </div>
      </div>

      <!-- 更新时间 -->
      <div class="update-time">
        <span class="update-label">最后更新：</span>
        <span class="update-value">{{ formatTime(poolStats.last_updated) }}</span>
      </div>
    </div>
  </n-card>
</template>

<style scoped>
.pool-overview-card {
  height: 100%;
}

.loading-state {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 200px;
  color: #6b7280;
}

.pool-overview-content {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.pool-stats-grid {
  margin-bottom: 8px;
}

.pool-stat-item {
  padding: 16px;
  border-radius: var(--border-radius-md);
  background: rgba(255, 255, 255, 0.5);
  border: 1px solid rgba(0, 0, 0, 0.05);
  transition: all 0.2s ease;
}

.pool-stat-item:hover {
  background: rgba(255, 255, 255, 0.8);
  transform: translateY(-1px);
  box-shadow: var(--shadow-md);
}

.stat-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}

.stat-title {
  font-size: 0.875rem;
  font-weight: 600;
  color: #374151;
}

.stat-content {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  margin-bottom: 8px;
}

.stat-number {
  font-size: 1.5rem;
  font-weight: 700;
  color: #1f2937;
}

.stat-percentage {
  font-size: 0.875rem;
  font-weight: 600;
  color: #6b7280;
}

.overall-stats {
  padding: 16px;
  background: rgba(102, 126, 234, 0.05);
  border-radius: var(--border-radius-md);
  border: 1px solid rgba(102, 126, 234, 0.1);
}

.health-status {
  padding: 16px;
  background: rgba(255, 255, 255, 0.5);
  border-radius: var(--border-radius-md);
  border: 1px solid rgba(0, 0, 0, 0.05);
}

.health-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}

.health-title {
  font-size: 0.875rem;
  font-weight: 600;
  color: #374151;
}

.health-issues {
  margin-top: 12px;
}

.issues-title {
  font-size: 0.8rem;
  font-weight: 600;
  color: #ef4444;
  margin-bottom: 8px;
}

.issues-list {
  margin: 0;
  padding-left: 16px;
  font-size: 0.8rem;
  color: #6b7280;
}

.issues-list li {
  margin-bottom: 4px;
}

.update-time {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 8px;
  font-size: 0.8rem;
  color: #6b7280;
  border-top: 1px solid rgba(0, 0, 0, 0.05);
}

.update-label {
  font-weight: 500;
}

.update-value {
  font-weight: 600;
}

/* 响应式设计 */
@media (max-width: 640px) {
  .pool-stats-grid {
    grid-template-columns: 1fr;
  }
  
  .stat-content {
    flex-direction: column;
    align-items: flex-start;
    gap: 4px;
  }
  
  .overall-stats :deep(.n-grid) {
    grid-template-columns: 1fr;
    gap: 16px;
  }
}
</style>
