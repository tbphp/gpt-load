<script setup lang="ts">
import { keysApi } from "@/api/keys";
import type { Group, GroupStatsResponse } from "@/types/models";
import { getGroupDisplayName } from "@/utils/display";
import { copy } from "@/utils/clipboard";
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
    title: "Delete Group",
    content: `Are you sure you want to delete the group "${getGroupDisplayName(props.group)}"? This action cannot be undone.`,
    positiveText: "Confirm",
    negativeText: "Cancel",
    onPositiveClick: async () => {
      d.loading = true;
      delLoading.value = true;

      try {
        if (props.group?.id) {
          await keysApi.deleteGroup(props.group.id);
          emit("delete", props.group);
        }
      } catch (error) {
        console.error("Failed to delete group:", error);
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

async function copyUrl(url: string) {
  if (!url) {
    return;
  }
  const success = await copy(url);
  if (success) {
    window.$message.success("Address copied to clipboard");
  } else {
    window.$message.error("Copy failed");
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
              {{ group ? getGroupDisplayName(group) : "Please select a group" }}
              <code v-if="group" class="group-url" @click="copyUrl(group?.endpoint || '')">
                {{ group.endpoint }}
              </code>
            </h3>
          </div>
          <div class="header-actions">
            <n-button quaternary circle size="small" @click="handleEdit" title="Edit group">
              <template #icon>
                <n-icon :component="Pencil" />
              </template>
            </n-button>
            <n-button
              quaternary
              circle
              size="small"
              @click="handleDelete"
              title="Delete group"
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
      <!-- Stats summary section -->
      <div class="stats-summary">
        <n-spin :show="loading" size="small">
          <n-grid :cols="4" :x-gap="12" :y-gap="12" responsive="screen">
            <n-grid-item span="1">
              <n-statistic :label="`Key count: ${stats?.key_stats?.total_keys ?? 0}`">
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="success" size="20">
                      {{ stats?.key_stats?.active_keys ?? 0 }}
                    </n-gradient-text>
                  </template>
                  Valid keys
                </n-tooltip>
                <n-divider vertical />
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ stats?.key_stats?.invalid_keys ?? 0 }}
                    </n-gradient-text>
                  </template>
                  Invalid keys
                </n-tooltip>
              </n-statistic>
            </n-grid-item>
            <n-grid-item span="1">
              <n-statistic
                :label="`1h requests: ${formatNumber(stats?.hourly_stats?.total_requests ?? 0)}`"
              >
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ formatNumber(stats?.hourly_stats?.failed_requests ?? 0) }}
                    </n-gradient-text>
                  </template>
                  Failed requests in last hour
                </n-tooltip>
                <n-divider vertical />
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ formatPercentage(stats?.hourly_stats?.failure_rate ?? 0) }}
                    </n-gradient-text>
                  </template>
                  Failure rate in last hour
                </n-tooltip>
              </n-statistic>
            </n-grid-item>
            <n-grid-item span="1">
              <n-statistic
                :label="`24h requests: ${formatNumber(stats?.daily_stats?.total_requests ?? 0)}`"
              >
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ formatNumber(stats?.daily_stats?.failed_requests ?? 0) }}
                    </n-gradient-text>
                  </template>
                  Failed requests in last 24h
                </n-tooltip>
                <n-divider vertical />
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ formatPercentage(stats?.daily_stats?.failure_rate ?? 0) }}
                    </n-gradient-text>
                  </template>
                  Failure rate in last 24h
                </n-tooltip>
              </n-statistic>
            </n-grid-item>
            <n-grid-item span="1">
              <n-statistic
                :label="`7d requests: ${formatNumber(stats?.weekly_stats?.total_requests ?? 0)}`"
              >
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ formatNumber(stats?.weekly_stats?.failed_requests ?? 0) }}
                    </n-gradient-text>
                  </template>
                  Failed requests in last 7 days
                </n-tooltip>
                <n-divider vertical />
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ formatPercentage(stats?.weekly_stats?.failure_rate ?? 0) }}
                    </n-gradient-text>
                  </template>
                  Failure rate in last 7 days
                </n-tooltip>
              </n-statistic>
            </n-grid-item>
          </n-grid>
        </n-spin>
      </div>
      <n-divider style="margin: 0" />

      <!-- Detail information section (collapsible) -->
      <div class="details-section">
        <n-collapse accordion v-model:expanded-names="expandedName">
          <n-collapse-item title="Detailed Information" name="details">
            <div class="details-content">
              <div class="detail-section">
                <h4 class="section-title">Basic Information</h4>
                <n-form label-placement="left" label-width="85px" label-align="right">
                  <n-grid :cols="2">
                    <n-grid-item>
                      <n-form-item label="Group Name:">
                        {{ group?.name || "-" }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item>
                      <n-form-item label="Display Name:">
                        {{ group?.display_name || "-" }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item>
                      <n-form-item label="Channel Type:">
                        {{ group?.channel_type || "-" }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item>
                      <n-form-item label="Test Model:">
                        {{ group?.test_model || "-" }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item>
                      <n-form-item label="Sort Order:">
                        {{ group?.sort || 0 }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item>
                      <n-form-item label="Description:">
                        {{ group?.description || "-" }}
                      </n-form-item>
                    </n-grid-item>
                  </n-grid>
                </n-form>
              </div>

              <div class="detail-section">
                <h4 class="section-title">Upstream Addresses</h4>
                <n-form label-placement="left" label-width="100px">
                  <n-form-item
                    v-for="(upstream, index) in group?.upstreams ?? []"
                    :key="index"
                    class="upstream-item"
                    :label="`Upstream ${index + 1}:`"
                  >
                    <span class="upstream-weight">
                      <n-tag size="small" type="info">Weight: {{ upstream.weight }}</n-tag>
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
                <h4 class="section-title">Advanced Configuration</h4>
                <n-form label-placement="left">
                  <n-form-item
                    v-for="(value, key) in group?.config || {}"
                    :key="key"
                    :label="`${key}:`"
                  >
                    {{ value || "-" }}
                  </n-form-item>
                  <n-form-item v-if="group?.param_overrides" label="Parameter Overrides:" :span="2">
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
</style>
