<script setup lang="ts">
import { getDashboardStats } from "@/api/dashboard";
import type { DashboardStatsResponse } from "@/types/models";
import { NCard, NGrid, NGridItem, NSpace, NTag, NTooltip } from "naive-ui";
import { onMounted, ref } from "vue";

// 统计数据
const stats = ref<DashboardStatsResponse | null>(null);
const loading = ref(true);
const animatedValues = ref<Record<string, number>>({});

// 格式化数值显示
const formatValue = (value: number, type: "count" | "rate" = "count"): string => {
  if (type === "rate") {
    return `${value.toFixed(1)}%`;
  }
  if (value >= 1000) {
    return `${(value / 1000).toFixed(1)}K`;
  }
  return value.toString();
};

// 格式化趋势显示
const formatTrend = (trend: number): string => {
  const sign = trend >= 0 ? "+" : "";
  return `${sign}${trend.toFixed(1)}%`;
};

// 获取统计数据
const fetchStats = async () => {
  try {
    loading.value = true;
    const response = await getDashboardStats();
    stats.value = response.data;

    // 添加动画效果
    setTimeout(() => {
      animatedValues.value = {
        key_count:
          (stats.value?.key_count?.value ?? 0) /
          ((stats.value?.key_count?.value ?? 1) + (stats.value?.key_count?.sub_value ?? 1)),
        rpm: Math.min(100 + (stats.value?.rpm?.trend ?? 0), 100) / 100,
        request_count: Math.min(100 + (stats.value?.request_count?.trend ?? 0), 100) / 100,
        error_rate: (100 - (stats.value?.error_rate?.value ?? 0)) / 100,
      };
    }, 0);
  } catch (error) {
    console.error("获取统计数据失败:", error);
  } finally {
    loading.value = false;
  }
};

onMounted(() => {
  fetchStats();
});
</script>

<template>
  <div class="stats-container">
    <n-space vertical size="medium">
      <n-grid cols="2 s:4" :x-gap="20" :y-gap="20" responsive="screen">
        <!-- 密钥数量 -->
        <n-grid-item span="1">
          <n-card :bordered="false" class="stat-card" style="animation-delay: 0s">
            <div class="stat-header">
              <div class="stat-icon key-icon">🔑</div>
              <n-tooltip v-if="stats?.key_count.sub_value" trigger="hover">
                <template #trigger>
                  <n-tag type="error" size="small" class="stat-trend">
                    {{ stats.key_count.sub_value }}
                  </n-tag>
                </template>
                {{ stats.key_count.sub_value_tip }}
              </n-tooltip>
            </div>

            <div class="stat-content">
              <div class="stat-value">
                {{ stats?.key_count?.value ?? 0 }}
              </div>
              <div class="stat-title">密钥数量</div>
            </div>

            <div class="stat-bar">
              <div
                class="stat-bar-fill key-bar"
                :style="{
                  width: `${(animatedValues.key_count ?? 0) * 100}%`,
                }"
              />
            </div>
          </n-card>
        </n-grid-item>

        <!-- RPM (10分钟) -->
        <n-grid-item span="1">
          <n-card :bordered="false" class="stat-card" style="animation-delay: 0.05s">
            <div class="stat-header">
              <div class="stat-icon rpm-icon">⏱️</div>
              <n-tag
                v-if="stats?.rpm && stats.rpm.trend !== undefined"
                :type="stats?.rpm.trend_is_growth ? 'success' : 'error'"
                size="small"
                class="stat-trend"
              >
                {{ stats ? formatTrend(stats.rpm.trend) : "--" }}
              </n-tag>
            </div>

            <div class="stat-content">
              <div class="stat-value">
                {{ stats?.rpm?.value.toFixed(1) ?? 0 }}
              </div>
              <div class="stat-title">10分钟RPM</div>
            </div>

            <div class="stat-bar">
              <div
                class="stat-bar-fill rpm-bar"
                :style="{
                  width: `${(animatedValues.rpm ?? 0) * 100}%`,
                }"
              />
            </div>
          </n-card>
        </n-grid-item>

        <!-- 24小时请求 -->
        <n-grid-item span="1">
          <n-card :bordered="false" class="stat-card" style="animation-delay: 0.1s">
            <div class="stat-header">
              <div class="stat-icon request-icon">📈</div>
              <n-tag
                v-if="stats?.request_count && stats.request_count.trend !== undefined"
                :type="stats?.request_count.trend_is_growth ? 'success' : 'error'"
                size="small"
                class="stat-trend"
              >
                {{ stats ? formatTrend(stats.request_count.trend) : "--" }}
              </n-tag>
            </div>

            <div class="stat-content">
              <div class="stat-value">
                {{ stats ? formatValue(stats.request_count.value) : "--" }}
              </div>
              <div class="stat-title">24小时请求</div>
            </div>

            <div class="stat-bar">
              <div
                class="stat-bar-fill request-bar"
                :style="{
                  width: `${(animatedValues.request_count ?? 0) * 100}%`,
                }"
              />
            </div>
          </n-card>
        </n-grid-item>

        <!-- 24小时错误率 -->
        <n-grid-item span="1">
          <n-card :bordered="false" class="stat-card" style="animation-delay: 0.15s">
            <div class="stat-header">
              <div class="stat-icon error-icon">🛡️</div>
              <n-tag
                v-if="stats?.error_rate.trend !== 0"
                :type="stats?.error_rate.trend_is_growth ? 'success' : 'error'"
                size="small"
                class="stat-trend"
              >
                {{ stats ? formatTrend(stats.error_rate.trend) : "--" }}
              </n-tag>
            </div>

            <div class="stat-content">
              <div class="stat-value">
                {{ stats ? formatValue(stats.error_rate.value ?? 0, "rate") : "--" }}
              </div>
              <div class="stat-title">24小时错误率</div>
            </div>

            <div class="stat-bar">
              <div
                class="stat-bar-fill error-bar"
                :style="{
                  width: `${(animatedValues.error_rate ?? 0) * 100}%`,
                }"
              />
            </div>
          </n-card>
        </n-grid-item>
      </n-grid>
    </n-space>
  </div>
</template>

<style scoped>
.stats-container {
  width: 100%;
  animation: fadeInUp 0.2s ease-out;
}

.stat-card {
  background: rgba(255, 255, 255, 0.98);
  border-radius: var(--border-radius-lg);
  border: 1px solid rgba(255, 255, 255, 0.3);
  position: relative;
  overflow: hidden;
  animation: slideInUp 0.2s ease-out both;
  transition: all 0.2s ease;
}

.stat-card:hover {
  transform: translateY(-2px);
  box-shadow: var(--shadow-lg);
}

.stat-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
}

.stat-icon {
  width: 40px;
  height: 40px;
  border-radius: var(--border-radius-md);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 1.4rem;
  color: white;
  box-shadow: var(--shadow-md);
}

.key-icon {
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
}

.rpm-icon {
  background: linear-gradient(135deg, #f093fb 0%, #f5576c 100%);
}

.request-icon {
  background: linear-gradient(135deg, #4facfe 0%, #00f2fe 100%);
}

.error-icon {
  background: linear-gradient(135deg, #43e97b 0%, #38f9d7 100%);
}

.stat-trend {
  font-weight: 600;
}

.stat-trend:before {
  content: "";
  display: inline-block;
  width: 0;
  height: 0;
  margin-right: 4px;
  vertical-align: middle;
}

.stat-content {
  margin-bottom: 16px;
}

.stat-value {
  font-size: 2rem;
  font-weight: 700;
  line-height: 1.2;
  color: #1e293b;
  margin-bottom: 4px;
}

.stat-title {
  font-size: 0.95rem;
  color: #64748b;
  font-weight: 500;
}

.stat-bar {
  width: 100%;
  height: 4px;
  background: rgba(0, 0, 0, 0.05);
  border-radius: 2px;
  overflow: hidden;
  position: relative;
}

.stat-bar-fill {
  height: 100%;
  border-radius: 2px;
  transition: width 0.5s ease-out;
  transition-delay: 0.2s;
}

.key-bar {
  background: linear-gradient(90deg, #667eea 0%, #764ba2 100%);
}

.rpm-bar {
  background: linear-gradient(90deg, #f093fb 0%, #f5576c 100%);
}

.request-bar {
  background: linear-gradient(90deg, #4facfe 0%, #00f2fe 100%);
}

.error-bar {
  background: linear-gradient(90deg, #43e97b 0%, #38f9d7 100%);
}

@keyframes slideInUp {
  from {
    opacity: 0;
    transform: translateY(30px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

@keyframes fadeInUp {
  from {
    opacity: 0;
    transform: translateY(20px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

/* 响应式网格 */
:deep(.n-grid-item) {
  min-width: 0;
}
</style>
