<script setup lang="ts">
import { keysApi } from "@/api/keys";
import { poolApi } from "@/api/pool";
import PoolOverview from "@/components/pool/PoolOverview.vue";
import PoolStats from "@/components/pool/PoolStats.vue";
import RecoveryControl from "@/components/pool/RecoveryControl.vue";
import RecoveryMonitor from "@/components/pool/RecoveryMonitor.vue";
import type { Group, PoolStatsResponse, RecoveryMetrics } from "@/types/models";
import { RefreshOutline, SettingsOutline } from "@vicons/ionicons5";
import {
    NButton,
    NCard,
    NGrid,
    NGridItem,
    NIcon,
    NSelect,
    NSpace,
    NSpin,
    useMessage
} from "naive-ui";
import { computed, onMounted, onUnmounted, ref, watch } from "vue";

const message = useMessage();
const loading = ref(false);
const groups = ref<Group[]>([]);
const selectedGroup = ref<Group | null>(null);
const poolStats = ref<PoolStatsResponse | null>(null);
const recoveryMetrics = ref<RecoveryMetrics | null>(null);
const autoRefresh = ref(true);
const refreshInterval = ref<NodeJS.Timeout | null>(null);

// 分组选项
const groupOptions = computed(() => [
  { label: "选择分组", value: null, disabled: true },
  ...groups.value.map(group => ({
    label: `${group.name} (${group.id})`,
    value: group.id,
  })),
]);

onMounted(async () => {
  await loadGroups();
  startAutoRefresh();
});

onUnmounted(() => {
  stopAutoRefresh();
});

// 加载分组列表
async function loadGroups() {
  try {
    loading.value = true;
    groups.value = await keysApi.getGroups();

    // 自动选择第一个分组
    if (groups.value.length > 0 && !selectedGroup.value) {
      selectedGroup.value = groups.value[0];
    }
  } catch (error) {
    message.error("加载分组列表失败");
  } finally {
    loading.value = false;
  }
}

// 监听分组变化
watch(selectedGroup, async (newGroup) => {
  if (newGroup) {
    await loadPoolData();
  }
});

// 加载池数据
async function loadPoolData() {
  if (!selectedGroup.value) return;

  try {
    loading.value = true;

    // 并行加载池统计和恢复指标
    const [statsRes, metricsRes] = await Promise.all([
      poolApi.getPoolStats(selectedGroup.value.id),
      poolApi.getRecoveryMetrics(selectedGroup.value.id),
    ]);

    poolStats.value = statsRes;
    recoveryMetrics.value = metricsRes;
  } catch (error) {
    message.error("加载池数据失败");
  } finally {
    loading.value = false;
  }
}

// 手动刷新
async function handleRefresh() {
  await loadPoolData();
  message.success("数据已刷新");
}

// 自动刷新控制
function startAutoRefresh() {
  if (refreshInterval.value) return;

  refreshInterval.value = setInterval(async () => {
    if (autoRefresh.value && selectedGroup.value) {
      await loadPoolData();
    }
  }, 30000); // 30秒刷新一次
}

function stopAutoRefresh() {
  if (refreshInterval.value) {
    clearInterval(refreshInterval.value);
    refreshInterval.value = null;
  }
}

function toggleAutoRefresh() {
  autoRefresh.value = !autoRefresh.value;
  if (autoRefresh.value) {
    startAutoRefresh();
  } else {
    stopAutoRefresh();
  }
}

// 处理恢复操作
async function handleRecoveryAction(action: string, data?: any) {
  if (!selectedGroup.value) return;

  try {
    switch (action) {
      case 'manual_recovery':
        await poolApi.triggerManualRecovery(selectedGroup.value.id, data.keyIds);
        message.success(`已触发 ${data.keyIds.length} 个密钥的手动恢复`);
        break;
      case 'batch_recovery':
        await poolApi.triggerBatchRecovery(selectedGroup.value.id, data);
        message.success("已触发批量恢复");
        break;
      case 'pool_refill':
        await poolApi.refillPools(selectedGroup.value.id);
        message.success("池重填完成");
        break;
    }

    // 刷新数据
    await loadPoolData();
  } catch (error) {
    message.error(`操作失败: ${error.message}`);
  }
}
</script>

<template>
  <div class="token-pool-container">
    <!-- 页面头部 -->
    <div class="page-header">
      <div class="header-left">
        <h1 class="page-title">Token池管理</h1>
        <p class="page-subtitle">分层密钥池状态监控与429智能恢复</p>
      </div>

      <div class="header-actions">
        <n-space :size="12">
          <!-- 分组选择 -->
          <n-select
            v-model:value="selectedGroup"
            :options="groupOptions"
            placeholder="选择分组"
            style="width: 200px"
            :loading="loading"
            @update:value="loadPoolData"
          />

          <!-- 自动刷新开关 -->
          <n-button
            :type="autoRefresh ? 'primary' : 'default'"
            size="medium"
            @click="toggleAutoRefresh"
          >
            <template #icon>
              <n-icon :component="RefreshOutline" />
            </template>
            {{ autoRefresh ? '自动刷新' : '手动刷新' }}
          </n-button>

          <!-- 手动刷新 -->
          <n-button
            type="default"
            size="medium"
            :loading="loading"
            @click="handleRefresh"
          >
            <template #icon>
              <n-icon :component="RefreshOutline" />
            </template>
            刷新
          </n-button>
        </n-space>
      </div>
    </div>

    <!-- 主要内容 -->
    <div class="main-content">
      <n-spin :show="loading">
        <div v-if="!selectedGroup" class="empty-state">
          <n-card class="modern-card">
            <div class="empty-content">
              <n-icon size="48" color="#d1d5db">
                <SettingsOutline />
              </n-icon>
              <h3>请选择一个分组</h3>
              <p>选择分组后查看Token池状态和恢复信息</p>
            </div>
          </n-card>
        </div>

        <div v-else class="pool-dashboard">
          <!-- 第一行：池概览和统计 -->
          <n-grid :cols="24" :x-gap="16" :y-gap="16">
            <!-- 池概览 -->
            <n-grid-item :span="24" :md="12">
              <pool-overview
                :group="selectedGroup"
                :pool-stats="poolStats"
                @refresh="loadPoolData"
              />
            </n-grid-item>

            <!-- 池统计 -->
            <n-grid-item :span="24" :md="12">
              <pool-stats
                :group="selectedGroup"
                :pool-stats="poolStats"
                @action="handleRecoveryAction"
              />
            </n-grid-item>
          </n-grid>

          <!-- 第二行：恢复监控和控制 -->
          <n-grid :cols="24" :x-gap="16" :y-gap="16" style="margin-top: 16px">
            <!-- 恢复监控 -->
            <n-grid-item :span="24" :md="14">
              <recovery-monitor
                :group="selectedGroup"
                :recovery-metrics="recoveryMetrics"
                @refresh="loadPoolData"
              />
            </n-grid-item>

            <!-- 恢复控制 -->
            <n-grid-item :span="24" :md="10">
              <recovery-control
                :group="selectedGroup"
                :pool-stats="poolStats"
                @action="handleRecoveryAction"
              />
            </n-grid-item>
          </n-grid>
        </div>
      </n-spin>
    </div>
  </div>
</template>

<style scoped>
.token-pool-container {
  display: flex;
  flex-direction: column;
  gap: 16px;
  width: 100%;
  min-height: calc(100vh - 120px);
}

.page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 20px 24px;
  background: rgba(255, 255, 255, 0.95);
  backdrop-filter: blur(10px);
  border-radius: var(--border-radius-lg);
  box-shadow: var(--shadow-md);
  border: 1px solid rgba(255, 255, 255, 0.2);
}

.header-left {
  flex: 1;
}

.page-title {
  font-size: 1.75rem;
  font-weight: 700;
  background: var(--primary-gradient);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
  margin: 0 0 4px 0;
  letter-spacing: -0.5px;
}

.page-subtitle {
  font-size: 0.95rem;
  color: #64748b;
  margin: 0;
}

.header-actions {
  flex-shrink: 0;
}

.main-content {
  flex: 1;
  padding: 0 24px 24px;
}

.empty-state {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 400px;
}

.empty-content {
  text-align: center;
  padding: 40px;
}

.empty-content h3 {
  font-size: 1.25rem;
  font-weight: 600;
  color: #374151;
  margin: 16px 0 8px 0;
}

.empty-content p {
  color: #6b7280;
  margin: 0;
}

.pool-dashboard {
  width: 100%;
}

/* 响应式设计 */
@media (max-width: 768px) {
  .page-header {
    flex-direction: column;
    gap: 16px;
    align-items: stretch;
  }

  .header-actions {
    width: 100%;
  }

  .header-actions :deep(.n-space) {
    justify-content: center;
  }

  .main-content {
    padding: 0 16px 16px;
  }

  .page-title {
    font-size: 1.5rem;
    text-align: center;
  }

  .page-subtitle {
    text-align: center;
  }
}

/* 状态标签样式 */
:deep(.status-tag) {
  font-weight: 600;
  letter-spacing: 0.5px;
}

/* 卡片悬停效果 */
:deep(.modern-card) {
  transition: all 0.2s ease;
}

:deep(.modern-card:hover) {
  transform: translateY(-2px);
  box-shadow: var(--shadow-xl);
}

/* 加载状态优化 */
:deep(.n-spin-container) {
  min-height: 400px;
}
</style>
