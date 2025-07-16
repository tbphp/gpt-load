<script setup lang="ts">
import { keysApi } from "@/api/keys";
import type { Group, GroupStatsResponse } from "@/types/models";
import { getGroupDisplayName } from "@/utils/display";
import { Pencil, Trash } from "@vicons/ionicons5";
import {
  NButton,
  NCard,
  NCollapse,
  NCollapseItem,
  NForm,
  NFormItem,
  NGrid,
  NGridItem,
  NSpin,
  NTag,
  useDialog,
} from "naive-ui";
import { onMounted, ref, watch } from "vue";
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
const expandedName = ref<string[]>([]);

onMounted(() => {
  loadStats();
});

watch(
  () => props.group,
  () => {
    resetPage();
    loadStats();
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

  const d = dialog.warning({
    title: "删除分组",
    content: `确定要删除分组 "${getGroupDisplayName(props.group)}" 吗？此操作不可恢复。`,
    positiveText: "确定",
    negativeText: "取消",
    onPositiveClick: async () => {
      d.loading = true;
      delLoading.value = true;

      try {
        if (props.group?.id) {
          await keysApi.deleteGroup(props.group.id);
          emit("delete", props.group);
        }
      } catch (error) {
        console.error("删除分组失败:", error);
      } finally {
        d.loading = false;
        delLoading.value = false;
      }
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

function copyUrl(url: string) {
  navigator.clipboard
    .writeText(url)
    .then(() => {
      window.$message.success("地址已复制到剪贴板");
    })
    .catch(() => {
      window.$message.error("复制失败");
    });
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
              <code v-if="group" class="group-url" @click="copyUrl(group?.endpoint || '')">
                {{ group.endpoint }}
              </code>
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
          <n-grid :cols="4" :x-gap="12" :y-gap="12" responsive="screen">
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
                  <n-grid :cols="2">
                    <n-grid-item>
                      <n-form-item label="分组名称：">
                        {{ group?.name || "-" }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item>
                      <n-form-item label="显示名称：">
                        {{ group?.display_name || "-" }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item>
                      <n-form-item label="渠道类型：">
                        {{ group?.channel_type || "-" }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item>
                      <n-form-item label="测试模型：">
                        {{ group?.test_model || "-" }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item>
                      <n-form-item label="排序：">
                        {{ group?.sort || 0 }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item>
                      <n-form-item label="描述：">
                        {{ group?.description || "-" }}
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
                    :label="`上游`"
                  >
                    <div class="upstream-content">
                      <div class="upstream-weight tag-row">
                        <n-tag size="tiny" type="info">ID: {{ upstream.id }}</n-tag>
                        <n-tag size="tiny" type="info">权重: {{ upstream.weight }}</n-tag>
                      </div>
                      <n-input class="upstream-url" :value="upstream.url" readonly size="small" />
                    </div>
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
                  <n-form-item
                    v-for="(value, key) in group?.config || {}"
                    :key="key"
                    :label="`${key}:`"
                  >
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

.upstream-content {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
}

.upstream-weight {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 6px;
}

.upstream-url {
  flex: 1;
  font-family: monospace;
  font-size: 0.9rem;
  color: #374151;
  min-width: 0;
}

.tag-row {
  display: flex;
  gap: 6px;
  flex-wrap: nowrap;
  align-items: center;
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
</style>
