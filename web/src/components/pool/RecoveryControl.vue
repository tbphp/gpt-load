<script setup lang="ts">
import type { Group, PoolStatsResponse, BatchRecoveryRequest } from "@/types/models";
import { 
  PlayOutline,
  StopOutline,
  RefreshOutline,
  SettingsOutline,
  FlashOutline,
  TimeOutline
} from "@vicons/ionicons5";
import {
  NCard,
  NButton,
  NSpace,
  NIcon,
  NInputNumber,
  NSelect,
  NForm,
  NFormItem,
  NSwitch,
  NTooltip,
  NTag,
  NAlert,
  useMessage,
  useDialog,
} from "naive-ui";
import { ref, computed } from "vue";

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
const dialog = useDialog();

// 批量恢复配置
const batchConfig = ref<BatchRecoveryRequest>({
  priority: "normal",
  max_concurrent: 5,
  delay_between_batches: 1000,
  filter: {
    min_failure_count: 1,
    max_failure_count: 10,
  },
});

// 优先级选项
const priorityOptions = [
  { label: "低优先级", value: "low" },
  { label: "普通优先级", value: "normal" },
  { label: "高优先级", value: "high" },
  { label: "关键优先级", value: "critical" },
];

// 计算冷却池中的密钥数量
const coolingKeysCount = computed(() => {
  return props.poolStats?.pool_stats?.cooling_pool || 0;
});

// 是否有密钥需要恢复
const hasKeysToRecover = computed(() => {
  return coolingKeysCount.value > 0;
});

// 手动恢复所有冷却密钥
function handleManualRecoveryAll() {
  if (!hasKeysToRecover.value) {
    message.warning("当前没有需要恢复的密钥");
    return;
  }

  dialog.warning({
    title: "确认恢复",
    content: `确定要手动恢复冷却池中的所有 ${coolingKeysCount.value} 个密钥吗？`,
    positiveText: "确认恢复",
    negativeText: "取消",
    onPositiveClick: () => {
      // 这里需要先获取冷却池中的密钥ID列表
      // 简化处理，直接传递空数组，后端会处理所有冷却密钥
      emit("action", "manual_recovery", { keyIds: [] });
    },
  });
}

// 批量恢复
function handleBatchRecovery() {
  if (!hasKeysToRecover.value) {
    message.warning("当前没有需要恢复的密钥");
    return;
  }

  dialog.info({
    title: "确认批量恢复",
    content: `将使用以下配置进行批量恢复：
    • 优先级: ${priorityOptions.find(p => p.value === batchConfig.value.priority)?.label}
    • 最大并发数: ${batchConfig.value.max_concurrent}
    • 批次间延迟: ${batchConfig.value.delay_between_batches}ms
    • 失败次数范围: ${batchConfig.value.filter?.min_failure_count} - ${batchConfig.value.filter?.max_failure_count}`,
    positiveText: "开始恢复",
    negativeText: "取消",
    onPositiveClick: () => {
      emit("action", "batch_recovery", batchConfig.value);
    },
  });
}

// 启动恢复服务
function handleStartRecoveryService() {
  dialog.info({
    title: "启动恢复服务",
    content: "确定要启动自动恢复服务吗？服务启动后将自动监控和恢复429密钥。",
    positiveText: "启动服务",
    negativeText: "取消",
    onPositiveClick: () => {
      emit("action", "start_recovery_service");
    },
  });
}

// 停止恢复服务
function handleStopRecoveryService() {
  dialog.warning({
    title: "停止恢复服务",
    content: "确定要停止自动恢复服务吗？停止后将不会自动恢复429密钥。",
    positiveText: "停止服务",
    negativeText: "取消",
    onPositiveClick: () => {
      emit("action", "stop_recovery_service");
    },
  });
}

// 重置配置
function resetConfig() {
  batchConfig.value = {
    priority: "normal",
    max_concurrent: 5,
    delay_between_batches: 1000,
    filter: {
      min_failure_count: 1,
      max_failure_count: 10,
    },
  };
  message.success("配置已重置");
}
</script>

<template>
  <n-card class="modern-card recovery-control-card" title="恢复控制">
    <template #header-extra>
      <n-tooltip trigger="hover">
        <template #trigger>
          <n-button size="small" @click="resetConfig">
            <template #icon>
              <n-icon :component="RefreshOutline" />
            </template>
          </n-button>
        </template>
        重置配置
      </n-tooltip>
    </template>

    <div class="recovery-control-content">
      <!-- 状态信息 -->
      <div class="status-section">
        <n-alert 
          v-if="!hasKeysToRecover" 
          type="success" 
          title="池状态良好"
          style="margin-bottom: 16px"
        >
          当前没有需要恢复的密钥
        </n-alert>
        
        <n-alert 
          v-else 
          type="warning" 
          :title="`发现 ${coolingKeysCount} 个密钥需要恢复`"
          style="margin-bottom: 16px"
        >
          这些密钥因为429错误被移到冷却池，可以手动或自动恢复
        </n-alert>
      </div>

      <!-- 快速操作 -->
      <div class="quick-actions">
        <div class="section-title">快速操作</div>
        <n-space :size="12" vertical>
          <n-button 
            type="primary" 
            block 
            :disabled="!hasKeysToRecover"
            @click="handleManualRecoveryAll"
          >
            <template #icon>
              <n-icon :component="FlashOutline" />
            </template>
            立即恢复所有密钥
          </n-button>
          
          <n-space :size="8">
            <n-button 
              type="success" 
              size="small"
              @click="handleStartRecoveryService"
            >
              <template #icon>
                <n-icon :component="PlayOutline" />
              </template>
              启动自动恢复
            </n-button>
            
            <n-button 
              type="error" 
              size="small"
              @click="handleStopRecoveryService"
            >
              <template #icon>
                <n-icon :component="StopOutline" />
              </template>
              停止自动恢复
            </n-button>
          </n-space>
        </n-space>
      </div>

      <!-- 批量恢复配置 -->
      <div class="batch-config">
        <div class="section-title">批量恢复配置</div>
        
        <n-form :model="batchConfig" size="small">
          <n-form-item label="优先级">
            <n-select 
              v-model:value="batchConfig.priority" 
              :options="priorityOptions"
              placeholder="选择优先级"
            />
          </n-form-item>
          
          <n-form-item label="最大并发数">
            <n-input-number 
              v-model:value="batchConfig.max_concurrent"
              :min="1"
              :max="20"
              placeholder="并发恢复数量"
            />
          </n-form-item>
          
          <n-form-item label="批次间延迟 (ms)">
            <n-input-number 
              v-model:value="batchConfig.delay_between_batches"
              :min="0"
              :max="10000"
              :step="100"
              placeholder="批次间延迟时间"
            />
          </n-form-item>
          
          <n-form-item label="失败次数范围">
            <n-space :size="8" align="center">
              <n-input-number 
                v-model:value="batchConfig.filter!.min_failure_count"
                :min="0"
                :max="100"
                placeholder="最小"
                style="width: 80px"
              />
              <span>-</span>
              <n-input-number 
                v-model:value="batchConfig.filter!.max_failure_count"
                :min="1"
                :max="100"
                placeholder="最大"
                style="width: 80px"
              />
            </n-space>
          </n-form-item>
        </n-form>
        
        <n-button 
          type="warning" 
          block 
          :disabled="!hasKeysToRecover"
          @click="handleBatchRecovery"
        >
          <template #icon>
            <n-icon :component="SettingsOutline" />
          </template>
          执行批量恢复
        </n-button>
      </div>

      <!-- 恢复统计 -->
      <div v-if="poolStats" class="recovery-stats">
        <div class="section-title">当前状态</div>
        
        <div class="stats-grid">
          <div class="stat-item">
            <div class="stat-label">验证池</div>
            <div class="stat-value">{{ poolStats.pool_stats.validation_pool }}</div>
            <n-tag type="success" size="small">可用</n-tag>
          </div>
          
          <div class="stat-item">
            <div class="stat-label">就绪池</div>
            <div class="stat-value">{{ poolStats.pool_stats.ready_pool }}</div>
            <n-tag type="info" size="small">待用</n-tag>
          </div>
          
          <div class="stat-item">
            <div class="stat-label">活跃池</div>
            <div class="stat-value">{{ poolStats.pool_stats.active_pool }}</div>
            <n-tag type="warning" size="small">使用中</n-tag>
          </div>
          
          <div class="stat-item">
            <div class="stat-label">冷却池</div>
            <div class="stat-value">{{ poolStats.pool_stats.cooling_pool }}</div>
            <n-tag type="error" size="small">需恢复</n-tag>
          </div>
        </div>
      </div>
    </div>
  </n-card>
</template>

<style scoped>
.recovery-control-card {
  height: 100%;
}

.recovery-control-content {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.section-title {
  font-size: 0.875rem;
  font-weight: 600;
  color: #374151;
  margin-bottom: 12px;
  padding-bottom: 6px;
  border-bottom: 1px solid rgba(0, 0, 0, 0.1);
}

.status-section {
  margin-bottom: 8px;
}

.quick-actions {
  padding: 16px;
  background: rgba(255, 255, 255, 0.5);
  border-radius: var(--border-radius-md);
  border: 1px solid rgba(0, 0, 0, 0.05);
}

.batch-config {
  padding: 16px;
  background: rgba(102, 126, 234, 0.05);
  border-radius: var(--border-radius-md);
  border: 1px solid rgba(102, 126, 234, 0.1);
}

.recovery-stats {
  padding: 16px;
  background: rgba(255, 255, 255, 0.5);
  border-radius: var(--border-radius-md);
  border: 1px solid rgba(0, 0, 0, 0.05);
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
}

.stat-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 12px;
  background: rgba(255, 255, 255, 0.8);
  border-radius: var(--border-radius-sm);
  border: 1px solid rgba(0, 0, 0, 0.05);
  text-align: center;
}

.stat-label {
  font-size: 0.75rem;
  color: #6b7280;
  margin-bottom: 4px;
}

.stat-value {
  font-size: 1.25rem;
  font-weight: 700;
  color: #1f2937;
  margin-bottom: 6px;
}

/* 表单样式优化 */
:deep(.n-form-item) {
  margin-bottom: 16px;
}

:deep(.n-form-item-label) {
  font-size: 0.8rem;
  font-weight: 600;
  color: #374151;
}

/* 响应式设计 */
@media (max-width: 640px) {
  .stats-grid {
    grid-template-columns: 1fr;
  }
  
  .recovery-control-content {
    gap: 16px;
  }
}
</style>
