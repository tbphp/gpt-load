<script setup lang="ts">
import { keysApi } from "@/api/keys";
import type { Group, GroupConfigOption, GroupStatsResponse } from "@/types/models";
import { appState } from "@/utils/app-state";
import { copy } from "@/utils/clipboard";
import { getGroupDisplayName, maskProxyKeys } from "@/utils/display";
import { CopyOutline, EyeOffOutline, EyeOutline, Pencil, Trash } from "@vicons/ionicons5";
import {
  NButton,
  NButtonGroup,
  NCard,
  NCollapse,
  NCollapseItem,
  NForm,
  NFormItem,
  NGrid,
  NGridItem,
  NIcon,
  NInput,
  NSpin,
  NTag,
  NTooltip,
  useDialog,
} from "naive-ui";
import { computed, h, onMounted, ref, watch } from "vue";
import GroupFormModal from "./GroupFormModal.vue";

interface Props {
  group: Group | null;
}

interface Emits {
  (e: "refresh", value: Group): void;
  (e: "delete", value: Group): void;
}

const props = defineProps<Props>();

const emit = defineEmits<Emits>();

const stats = ref<GroupStatsResponse | null>(null);
const loading = ref(false);
const dialog = useDialog();
const showEditModal = ref(false);
const delLoading = ref(false);
const confirmInput = ref("");
const expandedName = ref<string[]>([]);
const configOptions = ref<GroupConfigOption[]>([]);
const showProxyKeys = ref(false);

const proxyKeysDisplay = computed(() => {
  if (!props.group?.proxy_keys) {
    return "-";
  }
  if (showProxyKeys.value) {
    return props.group.proxy_keys.replace(/,/g, "\n");
  }
  return maskProxyKeys(props.group.proxy_keys);
});

async function copyProxyKeys() {
  if (!props.group?.proxy_keys) {
    return;
  }
  const keysToCopy = props.group.proxy_keys.replace(/,/g, "\n");
  const success = await copy(keysToCopy);
  if (success) {
    window.$message.success("代理密钥已复制到剪贴板");
  } else {
    window.$message.error("复制失败");
  }
}

onMounted(() => {
  loadStats();
  loadConfigOptions();
});

watch(
  () => props.group,
  () => {
    resetPage();
    loadStats();
  }
);

// 监听任务完成事件，自动刷新当前分组数据
watch(
  () => appState.groupDataRefreshTrigger,
  () => {
    // 检查是否需要刷新当前分组的数据
    if (appState.lastCompletedTask && props.group) {
      // 通过分组名称匹配
      const isCurrentGroup = appState.lastCompletedTask.groupName === props.group.name;

      const shouldRefresh =
        appState.lastCompletedTask.taskType === "KEY_VALIDATION" ||
        appState.lastCompletedTask.taskType === "KEY_IMPORT";

      if (isCurrentGroup && shouldRefresh) {
        // 刷新当前分组的统计数据
        loadStats();
      }
    }
  }
);

// 监听同步操作完成事件，自动刷新当前分组数据
watch(
  () => appState.syncOperationTrigger,
  () => {
    // 检查是否需要刷新当前分组的数据
    if (appState.lastSyncOperation && props.group) {
      // 通过分组名称匹配
      const isCurrentGroup = appState.lastSyncOperation.groupName === props.group.name;

      if (isCurrentGroup) {
        // 刷新当前分组的统计数据
        loadStats();
      }
    }
  }
);

async function loadStats() {
  if (!props.group?.id) {
    stats.value = null;
    return;
  }

  try {
    loading.value = true;
    if (props.group?.id) {
      stats.value = await keysApi.getGroupStats(props.group.id);
    }
  } finally {
    loading.value = false;
  }
}

async function loadConfigOptions() {
  try {
    const options = await keysApi.getGroupConfigOptions();
    configOptions.value = options || [];
  } catch (error) {
    console.error("获取配置选项失败:", error);
  }
}

function getConfigDisplayName(key: string): string {
  const option = configOptions.value.find(opt => opt.key === key);
  return option?.name || key;
}

function getConfigDescription(key: string): string {
  const option = configOptions.value.find(opt => opt.key === key);
  return option?.description || "暂无说明";
}

function handleEdit() {
  showEditModal.value = true;
}

function handleGroupEdited(newGroup: Group) {
  showEditModal.value = false;
  if (newGroup) {
    emit("refresh", newGroup);
  }
}

async function handleDelete() {
  if (!props.group || delLoading.value) {
    return;
  }

  dialog.warning({
    title: "删除分组",
    content: `确定要删除分组 "${getGroupDisplayName(
      props.group
    )}" 吗？此操作将删除分组及其下所有密钥，且不可恢复。`,
    positiveText: "确定",
    negativeText: "取消",
    onPositiveClick: () => {
      confirmInput.value = ""; // Reset before opening second dialog
      dialog.create({
        title: "请输入分组名称以确认删除",
        content: () =>
          h("div", null, [
            h("p", null, [
              "这是一个非常危险的操作。为防止误操作，请输入分组名称 ",
              h("strong", { style: { color: "#d03050" } }, props.group!.name),
              " 以确认删除。",
            ]),
            h(NInput, {
              value: confirmInput.value,
              "onUpdate:value": (v) => {
                confirmInput.value = v;
              },
              placeholder: "请输入分组名称",
            }),
          ]),
        positiveText: "确认删除",
        negativeText: "取消",
        onPositiveClick: async () => {
          if (confirmInput.value !== props.group!.name) {
            window.$message.error("分组名称输入不正确");
            return false; // Prevent dialog from closing
          }

          delLoading.value = true;
          try {
            if (props.group?.id) {
              await keysApi.deleteGroup(props.group.id);
              emit("delete", props.group);
              window.$message.success("分组已成功删除");
            }
          } catch (error) {
            console.error("删除分组失败:", error);
            window.$message.error("删除分组失败，请稍后重试");
          } finally {
            delLoading.value = false;
          }
        },
      });
    },
  });
}

function formatNumber(num: number): string {
  // if (num >= 1000000) {
  //   return `${(num / 1000000).toFixed(1)}M`;
  // }
  if (num >= 1000) {
    return `${(num / 1000).toFixed(1)}K`;
  }
  return num.toString();
}

function formatPercentage(num: number): string {
  if (num <= 0) {
    return "0";
  }
  return `${(num * 100).toFixed(1)}%`;
}

async function copyUrl(url: string) {
  if (!url) {
    return;
  }
  const success = await copy(url);
  if (success) {
    window.$message.success("地址已复制到剪贴板");
  } else {
    window.$message.error("复制失败");
  }
}

function resetPage() {
  showEditModal.value = false;
  expandedName.value = [];
}
</script>

<template>
  <div class="group-info-container">
    <n-card :bordered="false" class="group-info-card">
      <template #header>
        <div class="card-header">
          <div class="header-left">
            <h3 class="group-title">
              {{ group ? getGroupDisplayName(group) : "请选择分组" }}
              <n-tooltip trigger="hover" v-if="group">
                <template #trigger>
                  <code class="group-url" @click="copyUrl(group?.endpoint || '')">
                    {{ group.endpoint }}
                  </code>
                </template>
                点击复制
              </n-tooltip>
            </h3>
          </div>
          <div class="header-actions">
            <n-button quaternary circle size="small" @click="handleEdit" title="编辑分组">
              <template #icon>
                <n-icon :component="Pencil" />
              </template>
            </n-button>
            <n-button
              quaternary
              circle
              size="small"
              @click="handleDelete"
              title="删除分组"
              type="error"
              :disabled="!group"
            >
              <template #icon>
                <n-icon :component="Trash" />
              </template>
            </n-button>
          </div>
        </div>
      </template>

      <n-divider style="margin: 0; margin-bottom: 12px" />
      <!-- 统计摘要区 -->
      <div class="stats-summary">
        <n-spin :show="loading" size="small">
          <n-grid cols="2 s:4" :x-gap="12" :y-gap="12" responsive="screen">
            <n-grid-item span="1">
              <n-statistic :label="`密钥数量：${stats?.key_stats?.total_keys ?? 0}`">
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="success" size="20">
                      {{ stats?.key_stats?.active_keys ?? 0 }}
                    </n-gradient-text>
                  </template>
                  有效密钥数
                </n-tooltip>
                <n-divider vertical />
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ stats?.key_stats?.invalid_keys ?? 0 }}
                    </n-gradient-text>
                  </template>
                  无效密钥数
                </n-tooltip>
              </n-statistic>
            </n-grid-item>
            <n-grid-item span="1">
              <n-statistic
                :label="`1小时请求：${formatNumber(stats?.hourly_stats?.total_requests ?? 0)}`"
              >
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ formatNumber(stats?.hourly_stats?.failed_requests ?? 0) }}
                    </n-gradient-text>
                  </template>
                  近1小时失败请求
                </n-tooltip>
                <n-divider vertical />
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ formatPercentage(stats?.hourly_stats?.failure_rate ?? 0) }}
                    </n-gradient-text>
                  </template>
                  近1小时失败率
                </n-tooltip>
              </n-statistic>
            </n-grid-item>
            <n-grid-item span="1">
              <n-statistic
                :label="`24小时请求：${formatNumber(stats?.daily_stats?.total_requests ?? 0)}`"
              >
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ formatNumber(stats?.daily_stats?.failed_requests ?? 0) }}
                    </n-gradient-text>
                  </template>
                  近24小时失败请求
                </n-tooltip>
                <n-divider vertical />
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ formatPercentage(stats?.daily_stats?.failure_rate ?? 0) }}
                    </n-gradient-text>
                  </template>
                  近24小时失败率
                </n-tooltip>
              </n-statistic>
            </n-grid-item>
            <n-grid-item span="1">
              <n-statistic
                :label="`近7天请求：${formatNumber(stats?.weekly_stats?.total_requests ?? 0)}`"
              >
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ formatNumber(stats?.weekly_stats?.failed_requests ?? 0) }}
                    </n-gradient-text>
                  </template>
                  近7天失败请求
                </n-tooltip>
                <n-divider vertical />
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ formatPercentage(stats?.weekly_stats?.failure_rate ?? 0) }}
                    </n-gradient-text>
                  </template>
                  近7天失败率
                </n-tooltip>
              </n-statistic>
            </n-grid-item>
          </n-grid>
        </n-spin>
      </div>
      <n-divider style="margin: 0" />

      <!-- 详细信息区（可折叠） -->
      <div class="details-section">
        <n-collapse accordion v-model:expanded-names="expandedName">
          <n-collapse-item title="详细信息" name="details">
            <div class="details-content">
              <div class="detail-section">
                <h4 class="section-title">基础信息</h4>
                <n-form label-placement="left" label-width="85px" label-align="right">
                  <n-grid cols="1 m:2">
                    <n-grid-item>
                      <n-form-item label="分组名称：">
                        {{ group?.name }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item>
                      <n-form-item label="显示名称：">
                        {{ group?.display_name }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item>
                      <n-form-item label="渠道类型：">
                        {{ group?.channel_type }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item>
                      <n-form-item label="排序：">
                        {{ group?.sort }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item>
                      <n-form-item label="测试模型：">
                        {{ group?.test_model }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item v-if="group?.channel_type !== 'gemini'">
                      <n-form-item label="测试路径：">
                        {{ group?.validation_endpoint }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item :span="2">
                      <n-form-item label="代理密钥：">
                        <div class="proxy-keys-content">
                          <span class="key-text">{{ proxyKeysDisplay }}</span>
                          <n-button-group size="small" class="key-actions" v-if="group?.proxy_keys">
                            <n-tooltip trigger="hover">
                              <template #trigger>
                                <n-button quaternary circle @click="showProxyKeys = !showProxyKeys">
                                  <template #icon>
                                    <n-icon
                                      :component="showProxyKeys ? EyeOffOutline : EyeOutline"
                                    />
                                  </template>
                                </n-button>
                              </template>
                              {{ showProxyKeys ? "隐藏密钥" : "显示密钥" }}
                            </n-tooltip>
                            <n-tooltip trigger="hover">
                              <template #trigger>
                                <n-button quaternary circle @click="copyProxyKeys">
                                  <template #icon>
                                    <n-icon :component="CopyOutline" />
                                  </template>
                                </n-button>
                              </template>
                              复制密钥
                            </n-tooltip>
                          </n-button-group>
                        </div>
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item :span="2">
                      <n-form-item label="描述：">
                        <div class="description-content">
                          {{ group?.description || "-" }}
                        </div>
                      </n-form-item>
                    </n-grid-item>
                  </n-grid>
                </n-form>
              </div>

              <div class="detail-section">
                <h4 class="section-title">上游地址</h4>
                <n-form label-placement="left" label-width="100px">
                  <n-form-item
                    v-for="(upstream, index) in group?.upstreams ?? []"
                    :key="index"
                    class="upstream-item"
                    :label="`上游 ${index + 1}:`"
                  >
                    <span class="upstream-weight">
                      <n-tag size="small" type="info">权重: {{ upstream.weight }}</n-tag>
                    </span>
                    <n-input class="upstream-url" :value="upstream.url" readonly size="small" />
                  </n-form-item>
                </n-form>
              </div>

              <div
                class="detail-section"
                v-if="
                  (group?.config && Object.keys(group.config).length > 0) || group?.param_overrides
                "
              >
                <h4 class="section-title">高级配置</h4>
                <n-form label-placement="left">
                  <n-form-item v-for="(value, key) in group?.config || {}" :key="key">
                    <template #label>
                      <n-tooltip trigger="hover" :delay="300" placement="top">
                        <template #trigger>
                          <span class="config-label">
                            {{ getConfigDisplayName(key) }}:
                            <n-icon size="14" class="config-help-icon">
                              <svg viewBox="0 0 24 24">
                                <path
                                  fill="currentColor"
                                  d="M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2M12,17A1.5,1.5 0 0,1 10.5,15.5A1.5,1.5 0 0,1 12,14A1.5,1.5 0 0,1 13.5,15.5A1.5,1.5 0 0,1 12,17M12,10.5C10.07,10.5 8.5,8.93 8.5,7A3.5,3.5 0 0,1 12,3.5A3.5,3.5 0 0,1 15.5,7C15.5,8.93 13.93,10.5 12,10.5Z"
                                />
                              </svg>
                            </n-icon>
                          </span>
                        </template>
                        <div class="config-tooltip">
                          <div class="tooltip-title">{{ getConfigDisplayName(key) }}</div>
                          <div class="tooltip-description">{{ getConfigDescription(key) }}</div>
                          <div class="tooltip-key">配置键: {{ key }}</div>
                        </div>
                      </n-tooltip>
                    </template>
                    {{ value || "-" }}
                  </n-form-item>
                  <n-form-item v-if="group?.param_overrides" label="参数覆盖:" :span="2">
                    <pre class="config-json">{{
                      JSON.stringify(group?.param_overrides || "", null, 2)
                    }}</pre>
                  </n-form-item>
                </n-form>
              </div>
            </div>
          </n-collapse-item>
        </n-collapse>
      </div>
    </n-card>

    <group-form-modal v-model:show="showEditModal" :group="group" @success="handleGroupEdited" />
  </div>
</template>

<style scoped>
.group-info-container {
  width: 100%;
}

:deep(.n-card-header) {
  padding: 12px 24px;
}

.group-info-card {
  background: rgba(255, 255, 255, 0.98);
  border-radius: var(--border-radius-lg);
  border: 1px solid rgba(255, 255, 255, 0.3);
  animation: fadeInUp 0.2s ease-out;
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
}

.header-left {
  flex: 1;
}

.group-title {
  font-size: 1.2rem;
  font-weight: 600;
  color: #1e293b;
  margin: 0 0 8px 0;
}

.group-url {
  font-size: 0.8rem;
  color: #2563eb;
  margin-left: 8px;
  font-family: monospace;
  background: rgba(37, 99, 235, 0.1);
  border-radius: 4px;
  padding: 2px 6px;
  margin-right: 4px;
}

/* .group-meta {
  display: flex;
  align-items: center;
  gap: 8px;
} */

.group-id {
  font-size: 0.75rem;
  color: #64748b;
  opacity: 0.7;
}

.header-actions {
  display: flex;
  gap: 8px;
}

.stats-summary {
  margin-bottom: 12px;
  text-align: center;
}

.status-cards-container:deep(.n-card) {
  max-width: 160px;
}

:deep(.status-card-failure .n-card-header__main) {
  color: #d03050;
}

.status-title {
  color: #64748b;
  font-size: 12px;
}

.details-section {
  margin-top: 12px;
}

.details-content {
  margin-top: 12px;
}

.detail-section {
  margin-bottom: 24px;
}

.detail-section:last-child {
  margin-bottom: 0;
}

.section-title {
  font-size: 1rem;
  font-weight: 600;
  color: #374151;
  margin: 0 0 12px 0;
  padding-bottom: 8px;
  border-bottom: 2px solid rgba(102, 126, 234, 0.1);
}

.upstream-url {
  font-family: monospace;
  font-size: 0.9rem;
  color: #374151;
  margin-left: 5px;
}

.upstream-weight {
  min-width: 70px;
}

.config-json {
  background: rgba(102, 126, 234, 0.05);
  border-radius: var(--border-radius-sm);
  padding: 12px;
  font-size: 0.8rem;
  color: #374151;
  margin: 8px 0;
  overflow-x: auto;
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

:deep(.n-form-item-feedback-wrapper) {
  min-height: 0;
}

/* 描述内容样式 */
.description-content {
  white-space: pre-wrap;
  word-wrap: break-word;
  line-height: 1.5;
  min-height: 20px;
  color: #374151;
}

.proxy-keys-content {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  width: 100%;
  gap: 8px;
}

.key-text {
  flex-grow: 1;
  font-family: monospace;
  white-space: pre-wrap;
  word-break: break-all;
  line-height: 1.5;
  padding-top: 4px; /* Align with buttons */
  color: #374151;
}

.key-actions {
  flex-shrink: 0;
}

/* 配置项tooltip样式 */
.config-label {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  cursor: help;
}

.config-help-icon {
  color: #9ca3af;
  transition: color 0.2s ease;
}

.config-label:hover .config-help-icon {
  color: #6366f1;
}

.config-tooltip {
  max-width: 300px;
  padding: 8px 0;
}

.tooltip-title {
  font-weight: 600;
  color: #ffffff;
  margin-bottom: 4px;
  font-size: 0.9rem;
}

.tooltip-description {
  color: #e5e7eb;
  margin-bottom: 6px;
  line-height: 1.4;
  font-size: 0.85rem;
}

.tooltip-key {
  color: #d1d5db;
  font-size: 0.75rem;
  font-family: monospace;
  background: rgba(255, 255, 255, 0.15);
  padding: 2px 6px;
  border-radius: 4px;
  display: inline-block;
}
</style>
