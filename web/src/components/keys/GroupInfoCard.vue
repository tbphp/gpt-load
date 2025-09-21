<script setup lang="ts">
import { keysApi } from "@/api/keys";
import type {
  Group,
  GroupConfigOption,
  GroupStatsResponse,
  ParentAggregateGroup,
  SubGroupInfo,
} from "@/types/models";
import { appState } from "@/utils/app-state";
import { copy } from "@/utils/clipboard";
import { getGroupDisplayName, maskProxyKeys } from "@/utils/display";
import {
  CopyOutline,
  EyeOffOutline,
  EyeOutline,
  InformationCircleOutline,
  Pencil,
  Trash,
} from "@vicons/ionicons5";
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
import { useI18n } from "vue-i18n";
import AggregateGroupModal from "./AggregateGroupModal.vue";
import GroupCopyModal from "./GroupCopyModal.vue";
import GroupFormModal from "./GroupFormModal.vue";

const { t } = useI18n();

interface Props {
  group: Group | null;
  groups?: Group[];
  subGroups?: SubGroupInfo[];
}

interface Emits {
  (e: "refresh", value: Group): void;
  (e: "delete", value: Group): void;
  (e: "copy-success", group: Group): void;
  (e: "navigate-to-group", groupId: number): void;
}

const props = defineProps<Props>();

const emit = defineEmits<Emits>();

const stats = ref<GroupStatsResponse | null>(null);
const loading = ref(false);
const dialog = useDialog();
const showEditModal = ref(false);
const showCopyModal = ref(false);
const showAggregateEditModal = ref(false);
const delLoading = ref(false);
const confirmInput = ref("");
const expandedName = ref<string[]>([]);
const configOptions = ref<GroupConfigOption[]>([]);
const showProxyKeys = ref(false);
const parentAggregateGroups = ref<ParentAggregateGroup[]>([]);

const proxyKeysDisplay = computed(() => {
  if (!props.group?.proxy_keys) {
    return "-";
  }
  if (showProxyKeys.value) {
    return props.group.proxy_keys.replace(/,/g, "\n");
  }
  return maskProxyKeys(props.group.proxy_keys);
});

const hasAdvancedConfig = computed(() => {
  return (
    (props.group?.config && Object.keys(props.group.config).length > 0) ||
    props.group?.param_overrides ||
    (props.group?.header_rules && props.group.header_rules.length > 0)
  );
});

// 判断是否为聚合分组
const isAggregateGroup = computed(() => {
  return props.group?.group_type === "aggregate";
});

async function copyProxyKeys() {
  if (!props.group?.proxy_keys) {
    return;
  }
  const keysToCopy = props.group.proxy_keys.replace(/,/g, "\n");
  const success = await copy(keysToCopy);
  if (success) {
    window.$message.success(t("keys.proxyKeysCopied"));
  } else {
    window.$message.error(t("keys.copyFailed"));
  }
}

onMounted(() => {
  loadStats();
  loadConfigOptions();
  loadParentAggregateGroups();
});

watch(
  () => props.group,
  () => {
    resetPage();
    loadStats();
    loadParentAggregateGroups();
  }
);

// 监听任务完成事件，自动刷新当前分组数据
watch(
  () => [appState.groupDataRefreshTrigger, appState.syncOperationTrigger],
  () => {
    if (!props.group) {
      return;
    }

    // 检查是否需要刷新当前分组的数据
    const isCurrentGroupTask =
      appState.lastCompletedTask && appState.lastCompletedTask.groupName === props.group.name;
    const isCurrentGroupSync =
      appState.lastSyncOperation && appState.lastSyncOperation.groupName === props.group.name;

    const shouldRefresh =
      (isCurrentGroupTask &&
        ["KEY_VALIDATION", "KEY_IMPORT", "KEY_DELETE"].includes(
          appState.lastCompletedTask?.taskType || ""
        )) ||
      isCurrentGroupSync;

    if (shouldRefresh) {
      loadStats();
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
    console.error("Failed to load config options:", error);
  }
}

async function loadParentAggregateGroups() {
  if (!props.group?.id || props.group.group_type === "aggregate") {
    parentAggregateGroups.value = [];
    return;
  }

  try {
    const parentGroups = await keysApi.getParentAggregateGroups(props.group.id);
    parentAggregateGroups.value = parentGroups || [];
  } catch (error) {
    console.error("Failed to load parent aggregate groups:", error);
    parentAggregateGroups.value = [];
  }
}

function getConfigDisplayName(key: string): string {
  const option = configOptions.value.find(opt => opt.key === key);
  return option?.name || key;
}

function getConfigDescription(key: string): string {
  const option = configOptions.value.find(opt => opt.key === key);
  return option?.description || t("keys.noDescription");
}

function handleEdit() {
  if (!props.group) {
    return;
  }
  if (props.group.group_type === "aggregate") {
    showAggregateEditModal.value = true;
    return;
  }
  showEditModal.value = true;
}

function handleCopy() {
  showCopyModal.value = true;
}

function handleNavigateToGroup(groupId: number) {
  emit("navigate-to-group", groupId);
}

function handleGroupEdited(newGroup: Group) {
  showEditModal.value = false;
  if (newGroup) {
    emit("refresh", newGroup);
  }
}

function handleAggregateGroupEdited(newGroup: Group) {
  showAggregateEditModal.value = false;
  if (newGroup) {
    emit("refresh", newGroup);
  }
}

function handleGroupCopied(newGroup: Group) {
  showCopyModal.value = false;
  if (newGroup) {
    emit("copy-success", newGroup);
  }
}

async function handleDelete() {
  if (!props.group || delLoading.value) {
    return;
  }

  dialog.warning({
    title: t("keys.deleteGroup"),
    content: t("keys.confirmDeleteGroup", { name: getGroupDisplayName(props.group) }),
    positiveText: t("common.confirm"),
    negativeText: t("common.cancel"),
    onPositiveClick: () => {
      confirmInput.value = ""; // Reset before opening second dialog
      dialog.create({
        title: t("keys.enterGroupNameToConfirm"),
        content: () =>
          h("div", null, [
            h("p", null, [
              t("keys.dangerousOperation"),
              h("strong", { style: { color: "#d03050" } }, props.group?.name),
              t("keys.toConfirmDeletion"),
            ]),
            h(NInput, {
              value: confirmInput.value,
              "onUpdate:value": v => {
                confirmInput.value = v;
              },
              placeholder: t("keys.enterGroupName"),
            }),
          ]),
        positiveText: t("keys.confirmDelete"),
        negativeText: t("common.cancel"),
        onPositiveClick: async () => {
          if (confirmInput.value !== props.group?.name) {
            window.$message.error(t("keys.incorrectGroupName"));
            return false; // Prevent dialog from closing
          }

          delLoading.value = true;
          try {
            if (props.group?.id) {
              await keysApi.deleteGroup(props.group.id);
              emit("delete", props.group);
              window.$message.success(t("keys.groupDeletedSuccess"));
            }
          } catch (error) {
            console.error(error);
            window.$message.error(t("keys.deleteGroupFailed"));
          } finally {
            delLoading.value = false;
          }
        },
      });
    },
  });
}

function formatNumber(num: number): string {
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
    window.$message.success(t("keys.urlCopied"));
  } else {
    window.$message.error(t("keys.copyFailed"));
  }
}

function resetPage() {
  showEditModal.value = false;
  showCopyModal.value = false;
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
              {{ group ? getGroupDisplayName(group) : t("keys.selectGroup") }}
              <n-tooltip trigger="hover" v-if="group && group.endpoint">
                <template #trigger>
                  <code class="group-url" @click="copyUrl(group.endpoint)">
                    {{ group.endpoint }}
                  </code>
                </template>
                {{ t("keys.clickToCopy") }}
              </n-tooltip>
            </h3>
          </div>
          <div class="header-actions">
            <n-button
              v-if="group?.group_type !== 'aggregate'"
              quaternary
              circle
              size="small"
              @click="handleCopy"
              :title="t('keys.copyGroup')"
              :disabled="!group"
            >
              <template #icon>
                <n-icon :component="CopyOutline" />
              </template>
            </n-button>
            <n-button
              quaternary
              circle
              size="small"
              @click="handleEdit"
              :title="t('keys.editGroup')"
            >
              <template #icon>
                <n-icon :component="Pencil" />
              </template>
            </n-button>
            <n-button
              quaternary
              circle
              size="small"
              @click="handleDelete"
              :title="t('keys.deleteGroup')"
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
              <!-- 聚合分组：子分组统计 -->
              <n-statistic
                v-if="isAggregateGroup"
                :label="`${t('keys.subGroups')}：${props.subGroups?.length || 0}`"
              >
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="success" size="20">
                      {{ props.subGroups?.filter(sg => sg.weight > 0).length || 0 }}
                    </n-gradient-text>
                  </template>
                  {{ t("keys.activeSubGroups") }}
                </n-tooltip>
                <n-divider vertical />
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="warning" size="20">
                      {{ props.subGroups?.filter(sg => sg.weight === 0).length || 0 }}
                    </n-gradient-text>
                  </template>
                  {{ t("keys.inactiveSubGroups") }}
                </n-tooltip>
              </n-statistic>

              <!-- 标准分组：密钥统计 -->
              <n-statistic
                v-else
                :label="`${t('keys.keyCount')}：${stats?.key_stats?.total_keys ?? 0}`"
              >
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="success" size="20">
                      {{ stats?.key_stats?.active_keys ?? 0 }}
                    </n-gradient-text>
                  </template>
                  {{ t("keys.activeKeyCount") }}
                </n-tooltip>
                <n-divider vertical />
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ stats?.key_stats?.invalid_keys ?? 0 }}
                    </n-gradient-text>
                  </template>
                  {{ t("keys.invalidKeyCount") }}
                </n-tooltip>
              </n-statistic>
            </n-grid-item>
            <n-grid-item span="1">
              <n-statistic
                :label="`${t('keys.hourlyRequests')}：${formatNumber(stats?.hourly_stats?.total_requests ?? 0)}`"
              >
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ formatNumber(stats?.hourly_stats?.failed_requests ?? 0) }}
                    </n-gradient-text>
                  </template>
                  {{ t("keys.hourlyFailedRequests") }}
                </n-tooltip>
                <n-divider vertical />
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ formatPercentage(stats?.hourly_stats?.failure_rate ?? 0) }}
                    </n-gradient-text>
                  </template>
                  {{ t("keys.hourlyFailureRate") }}
                </n-tooltip>
              </n-statistic>
            </n-grid-item>
            <n-grid-item span="1">
              <n-statistic
                :label="`${t('keys.dailyRequests')}：${formatNumber(stats?.daily_stats?.total_requests ?? 0)}`"
              >
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ formatNumber(stats?.daily_stats?.failed_requests ?? 0) }}
                    </n-gradient-text>
                  </template>
                  {{ t("keys.dailyFailedRequests") }}
                </n-tooltip>
                <n-divider vertical />
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ formatPercentage(stats?.daily_stats?.failure_rate ?? 0) }}
                    </n-gradient-text>
                  </template>
                  {{ t("keys.dailyFailureRate") }}
                </n-tooltip>
              </n-statistic>
            </n-grid-item>
            <n-grid-item span="1">
              <n-statistic
                :label="`${t('keys.weeklyRequests')}：${formatNumber(stats?.weekly_stats?.total_requests ?? 0)}`"
              >
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ formatNumber(stats?.weekly_stats?.failed_requests ?? 0) }}
                    </n-gradient-text>
                  </template>
                  {{ t("keys.weeklyFailedRequests") }}
                </n-tooltip>
                <n-divider vertical />
                <n-tooltip trigger="hover">
                  <template #trigger>
                    <n-gradient-text type="error" size="20">
                      {{ formatPercentage(stats?.weekly_stats?.failure_rate ?? 0) }}
                    </n-gradient-text>
                  </template>
                  {{ t("keys.weeklyFailureRate") }}
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
          <n-collapse-item :title="t('keys.detailInfo')" name="details">
            <div class="details-content">
              <div class="detail-section">
                <h4 class="section-title">{{ t("keys.basicInfo") }}</h4>
                <n-form label-placement="left" label-width="140px" label-align="right">
                  <n-grid cols="1 m:2">
                    <n-grid-item>
                      <n-form-item :label="`${t('keys.groupName')}：`">
                        {{ group?.name }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item>
                      <n-form-item :label="`${t('keys.displayName')}：`">
                        {{ group?.display_name }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item>
                      <n-form-item :label="`${t('keys.channelType')}：`">
                        {{ group?.channel_type }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item>
                      <n-form-item :label="`${t('keys.sortOrder')}：`">
                        {{ group?.sort }}
                      </n-form-item>
                    </n-grid-item>
                    <!-- 标准分组才显示测试模型和测试路径 -->
                    <n-grid-item v-if="!isAggregateGroup">
                      <n-form-item :label="`${t('keys.testModel')}：`">
                        {{ group?.test_model }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item v-if="!isAggregateGroup && group?.channel_type !== 'gemini'">
                      <n-form-item :label="`${t('keys.testPath')}：`">
                        {{ group?.validation_endpoint }}
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item :span="2">
                      <n-form-item :label="`${t('keys.proxyKeys')}：`">
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
                              {{ showProxyKeys ? t("keys.hideKeys") : t("keys.showKeys") }}
                            </n-tooltip>
                            <n-tooltip trigger="hover">
                              <template #trigger>
                                <n-button quaternary circle @click="copyProxyKeys">
                                  <template #icon>
                                    <n-icon :component="CopyOutline" />
                                  </template>
                                </n-button>
                              </template>
                              {{ t("keys.copyKeys") }}
                            </n-tooltip>
                          </n-button-group>
                        </div>
                      </n-form-item>
                    </n-grid-item>
                    <n-grid-item :span="2">
                      <n-form-item :label="`${t('common.description')}：`">
                        <div class="description-content">
                          {{ group?.description || "-" }}
                        </div>
                      </n-form-item>
                    </n-grid-item>
                  </n-grid>
                </n-form>
              </div>

              <!-- 聚合引用区（仅普通分组且存在被引用关系时显示） -->
              <div
                class="detail-section"
                v-if="!isAggregateGroup && parentAggregateGroups.length > 0"
              >
                <h4 class="section-title">{{ t("keys.aggregateReferences") }}</h4>
                <n-form label-placement="left" label-width="140px">
                  <n-form-item
                    v-for="(parent, index) in parentAggregateGroups"
                    :key="parent.group_id"
                    class="aggregate-ref-item"
                    :label="`${t('keys.aggregateGroup')} ${index + 1}:`"
                  >
                    <span class="aggregate-weight">
                      <n-tag size="small" type="info">
                        {{ t("keys.weight") }}: {{ parent.weight }}
                      </n-tag>
                    </span>
                    <n-input
                      class="aggregate-name"
                      :value="parent.display_name || parent.name"
                      readonly
                      size="small"
                      style="margin-left: 5px; margin-right: 8px"
                    />
                    <n-button
                      round
                      tertiary
                      type="default"
                      size="tiny"
                      @click="handleNavigateToGroup(parent.group_id)"
                      :title="t('keys.viewGroupInfo')"
                    >
                      <template #icon>
                        <n-icon :component="InformationCircleOutline" />
                      </template>
                      {{ t("keys.groupInfo") }}
                    </n-button>
                  </n-form-item>
                </n-form>
              </div>

              <!-- 标准分组才显示上游地址 -->
              <div class="detail-section" v-if="!isAggregateGroup">
                <h4 class="section-title">{{ t("keys.upstreamAddresses") }}</h4>
                <n-form label-placement="left" label-width="140px">
                  <n-form-item
                    v-for="(upstream, index) in group?.upstreams ?? []"
                    :key="index"
                    class="upstream-item"
                    :label="`${t('keys.upstream')} ${index + 1}:`"
                  >
                    <span class="upstream-weight">
                      <n-tag size="small" type="info">
                        {{ t("keys.weight") }}: {{ upstream.weight }}
                      </n-tag>
                    </span>
                    <n-input class="upstream-url" :value="upstream.url" readonly size="small" />
                  </n-form-item>
                </n-form>
              </div>

              <!-- 标准分组才显示高级配置 -->
              <div class="detail-section" v-if="!isAggregateGroup && hasAdvancedConfig">
                <h4 class="section-title">{{ t("keys.advancedConfig") }}</h4>
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
                          <div class="tooltip-key">{{ t("keys.configKey") }}: {{ key }}</div>
                        </div>
                      </n-tooltip>
                    </template>
                    {{ value || "-" }}
                  </n-form-item>
                  <n-form-item
                    v-if="group?.header_rules && group.header_rules.length > 0"
                    :label="`${t('keys.customHeaders')}：`"
                    :span="2"
                  >
                    <div class="header-rules-display">
                      <div
                        v-for="(rule, index) in group.header_rules"
                        :key="index"
                        class="header-rule-item"
                      >
                        <n-tag :type="rule.action === 'remove' ? 'error' : 'default'" size="small">
                          {{ rule.key }}
                        </n-tag>
                        <span class="header-separator">:</span>
                        <span class="header-value" v-if="rule.action === 'set'">
                          {{ rule.value || t("keys.emptyValue") }}
                        </span>
                        <span class="header-removed" v-else>{{ t("common.delete") }}</span>
                      </div>
                    </div>
                  </n-form-item>
                  <n-form-item
                    v-if="group?.param_overrides"
                    :label="`${t('keys.paramOverrides')}：`"
                    :span="2"
                  >
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
    <aggregate-group-modal
      v-model:show="showAggregateEditModal"
      :group="group"
      :groups="props.groups"
      @success="handleAggregateGroupEdited"
    />
    <group-copy-modal
      v-model:show="showCopyModal"
      :source-group="group"
      @success="handleGroupCopied"
    />
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
  background: var(--card-bg-solid);
  border-radius: var(--border-radius-md);
  border: 1px solid var(--border-color);
  animation: fadeInUp 0.2s ease-out;
  box-shadow: var(--shadow-sm);
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
  color: var(--text-primary);
  margin: 0 0 8px 0;
  display: flex;
  align-items: center;
  gap: 8px;
}

.group-url {
  font-size: 0.8rem;
  color: var(--primary-color);
  margin-left: 8px;
  font-family: monospace;
  background: var(--bg-secondary);
  border-radius: 4px;
  padding: 2px 6px;
  margin-right: 4px;
  border: 1px solid var(--border-color);
  cursor: pointer;
  transition: all 0.2s ease;
}

.group-id {
  font-size: 0.75rem;
  color: var(--text-secondary);
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
  color: var(--error-color, #d03050);
}

.status-title {
  color: var(--text-secondary);
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
  color: var(--text-primary);
  margin: 0 0 12px 0;
  padding-bottom: 8px;
  border-bottom: 2px solid var(--border-color);
}

.upstream-url {
  font-family: monospace;
  font-size: 0.9rem;
  color: var(--text-primary);
  margin-left: 5px;
}

.upstream-weight {
  min-width: 70px;
}

.config-json {
  background: var(--bg-secondary);
  border-radius: var(--border-radius-sm);
  padding: 12px;
  font-size: 0.8rem;
  color: var(--text-primary);
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
  color: var(--text-primary);
}

.aggregate-weight {
  min-width: 70px;
}

.aggregate-name {
  font-family: monospace;
  font-size: 0.9rem;
  color: var(--text-primary);
  width: 200px;
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
  color: var(--text-primary);
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
  color: var(--text-tertiary);
  transition: color 0.2s ease;
}

.config-label:hover .config-help-icon {
  color: var(--primary-color);
}

.config-tooltip {
  max-width: 300px;
  padding: 8px 0;
}

.tooltip-title {
  font-weight: 600;
  color: white;
  margin-bottom: 4px;
  font-size: 0.9rem;
}

.tooltip-description {
  color: rgba(255, 255, 255, 0.9);
  margin-bottom: 6px;
  line-height: 1.4;
  font-size: 0.85rem;
}

.tooltip-key {
  color: rgba(255, 255, 255, 0.8);
  font-size: 0.75rem;
  font-family: monospace;
  background: rgba(255, 255, 255, 0.15);
  padding: 2px 6px;
  border-radius: 4px;
  display: inline-block;
}

/* Header rules display styles */
.header-rules-display {
  display: flex;
  flex-direction: column;
  gap: 6px;
  background: var(--bg-secondary);
  border-radius: var(--border-radius-sm);
  padding: 8px;
}

.header-rule-item {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.875rem;
}

.header-separator {
  color: var(--text-secondary);
  font-weight: 500;
}

.header-value {
  color: var(--text-primary);
  font-family: monospace;
  background: var(--bg-secondary);
  padding: 2px 6px;
  border-radius: 3px;
  font-size: 0.8rem;
}

.header-removed {
  color: var(--error-color, #dc2626);
  font-style: italic;
  font-size: 0.8rem;
}
</style>
