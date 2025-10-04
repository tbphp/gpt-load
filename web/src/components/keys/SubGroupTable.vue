<script setup lang="ts">
import { keysApi } from "@/api/keys";
import type { Group, SubGroupInfo } from "@/types/models";
import { getGroupDisplayName } from "@/utils/display";
import { Add, CreateOutline, InformationCircleOutline, Trash } from "@vicons/ionicons5";
import { NButton, NButtonGroup, NEmpty, NIcon, NSpin, useDialog } from "naive-ui";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import AddSubGroupModal from "./AddSubGroupModal.vue";
import EditSubGroupWeightModal from "./EditSubGroupWeightModal.vue";

const { t } = useI18n();

interface SubGroupRow extends SubGroupInfo {
  percentage: number;
}

interface Props {
  selectedGroup: Group | null;
  subGroups?: SubGroupInfo[];
  groups?: Group[];
  loading?: boolean;
}

interface Emits {
  (e: "refresh"): void;
  (e: "group-select", groupId: number): void;
}

const props = defineProps<Props>();
const emit = defineEmits<Emits>();

const dialog = useDialog();

const addModalShow = ref(false);
const editModalShow = ref(false);
const editingSubGroup = ref<SubGroupInfo | null>(null);

// 计算带百分比的子分组数据并按权重排序
const sortedSubGroupsWithPercentage = computed<SubGroupRow[]>(() => {
  if (!props.subGroups) {
    return [];
  }
  const total = props.subGroups.reduce((sum, sg) => sum + sg.weight, 0);
  const withPercentage = props.subGroups.map(sg => ({
    ...sg,
    percentage: total > 0 ? Math.round((sg.weight / total) * 100) : 0,
  }));

  // 按权重降序排序
  return withPercentage.sort((a, b) => b.weight - a.weight);
});

function openEditModal(subGroup: SubGroupInfo) {
  editingSubGroup.value = subGroup;
  editModalShow.value = true;
}

async function deleteSubGroup(subGroup: SubGroupInfo) {
  if (!props.selectedGroup?.id) {
    return;
  }

  const d = dialog.warning({
    title: t("subGroups.removeSubGroup"),
    content: t("subGroups.confirmRemoveSubGroup", { name: getGroupDisplayName(subGroup) }),
    positiveText: t("common.confirm"),
    negativeText: t("common.cancel"),
    onPositiveClick: async () => {
      if (!props.selectedGroup?.id) {
        return;
      }

      d.loading = true;
      try {
        const groupId = subGroup.group.id;
        if (!groupId) {
          return;
        }
        await keysApi.deleteSubGroup(props.selectedGroup.id, groupId);
        emit("refresh");
      } finally {
        d.loading = false;
      }
    },
  });
}

// Handle success after modal operations
function handleSuccess() {
  emit("refresh");
}

// Navigate to group info
function goToGroupInfo(groupId: number) {
  emit("group-select", groupId);
}
</script>

<template>
  <div class="key-table-container">
    <!-- 工具栏 -->
    <div class="toolbar">
      <div class="toolbar-left">
        <n-button type="info" size="small" @click="addModalShow = true">
          <template #icon>
            <n-icon :component="Add" />
          </template>
          {{ t("subGroups.addSubGroup") }}
        </n-button>
      </div>
      <div class="toolbar-right">
        <!-- 可以添加其他操作按钮 -->
      </div>
    </div>

    <!-- 子分组卡片网格 -->
    <div class="keys-grid-container">
      <n-spin :show="props.loading || false">
        <div v-if="!props.subGroups || props.subGroups.length === 0" class="empty-container">
          <n-empty :description="t('subGroups.noSubGroups')" />
        </div>
        <div v-else class="keys-grid">
          <div
            v-for="subGroup in sortedSubGroupsWithPercentage"
            :key="subGroup.group.id"
            class="key-card status-sub-group"
            :class="{ disabled: subGroup.weight === 0 || subGroup.active_keys === 0 }"
          >
            <!-- Main info row: display name + group name -->
            <div class="key-main">
              <div class="key-section">
                <div class="sub-group-names">
                  <span class="display-name">{{ getGroupDisplayName(subGroup) }}</span>
                </div>
                <div class="quick-actions">
                  <span class="group-name">#{{ subGroup.group.name }}</span>
                </div>
              </div>
            </div>

            <!-- 权重显示 -->
            <div class="weight-display">
              <div class="weight-bar-container">
                <span class="weight-label">
                  {{ t("subGroups.weight") }}
                  <strong>{{ subGroup.weight }}</strong>
                </span>
                <div class="weight-bar">
                  <div class="weight-fill" :style="{ width: `${subGroup.percentage}%` }" />
                </div>
                <span class="weight-text">{{ subGroup.percentage }}%</span>
              </div>
            </div>

            <!-- 操作按钮行 -->
            <div class="key-bottom">
              <div class="key-stats">
                <n-button
                  round
                  tertiary
                  type="default"
                  size="tiny"
                  @click="subGroup.group.id && goToGroupInfo(subGroup.group.id)"
                  :title="t('subGroups.viewGroupInfo')"
                >
                  <template #icon>
                    <n-icon :component="InformationCircleOutline" />
                  </template>
                  {{ t("subGroups.groupInfo") }}
                </n-button>
              </div>
              <n-button-group class="key-actions">
                <n-button
                  round
                  tertiary
                  type="info"
                  size="tiny"
                  @click="openEditModal(subGroup)"
                  :title="t('subGroups.editWeight')"
                >
                  <template #icon>
                    <n-icon :component="CreateOutline" />
                  </template>
                  {{ t("common.edit") }}
                </n-button>
                <n-button
                  round
                  tertiary
                  size="tiny"
                  type="error"
                  @click="deleteSubGroup(subGroup)"
                  :title="t('subGroups.removeSubGroup')"
                >
                  <template #icon>
                    <n-icon :component="Trash" />
                  </template>
                  {{ t("subGroups.remove") }}
                </n-button>
              </n-button-group>
            </div>
          </div>
        </div>
      </n-spin>
    </div>

    <!-- 底部信息 -->
    <div class="pagination-container">
      <div class="pagination-info">
        <span>{{ t("subGroups.totalSubGroups", { total: props.subGroups?.length || 0 }) }}</span>
      </div>
      <div class="pagination-controls">
        <span class="page-info">
          {{ t("subGroups.sortedByWeight") }}
        </span>
      </div>
    </div>

    <!-- 添加子分组弹窗 -->
    <add-sub-group-modal
      v-if="selectedGroup?.id"
      v-model:show="addModalShow"
      :aggregate-group="selectedGroup"
      :existing-sub-groups="subGroups || []"
      :groups="groups || []"
      @success="handleSuccess"
    />

    <!-- 编辑权重弹窗 -->
    <edit-sub-group-weight-modal
      v-if="editingSubGroup && selectedGroup?.id"
      v-model:show="editModalShow"
      :aggregate-group="selectedGroup"
      :sub-group="editingSubGroup"
      :sub-groups="subGroups || []"
      @success="handleSuccess"
      @update:show="
        show => {
          if (!show) editingSubGroup = null;
        }
      "
    />
  </div>
</template>

<style scoped>
/* 直接复用KeyTable的所有样式 */
.key-table-container {
  background: var(--card-bg-solid);
  border-radius: 8px;
  box-shadow: var(--shadow-md);
  border: 1px solid var(--border-color);
  overflow: hidden;
  height: 100%;
  display: flex;
  flex-direction: column;
}

.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px;
  background: var(--card-bg-solid);
  border-bottom: 1px solid var(--border-color);
  flex-shrink: 0;
  gap: 16px;
  min-height: 64px;
}

.toolbar :deep(.n-button) {
  font-weight: 500;
}

.toolbar-left {
  display: flex;
  gap: 8px;
  flex-shrink: 0;
}

.toolbar-right {
  display: flex;
  gap: 12px;
  align-items: center;
  flex: 1;
  justify-content: flex-end;
  min-width: 0;
}

.keys-grid-container {
  flex: 1;
  overflow-y: auto;
  padding: 16px;
}

.keys-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 16px;
}

.key-card {
  background: var(--card-bg-solid);
  border: 1px solid var(--border-color);
  border-radius: 8px;
  padding: 14px;
  transition: all 0.2s;
  display: flex;
  flex-direction: column;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.05);
}

.key-card:hover {
  box-shadow: var(--shadow-md);
  transform: translateY(-1px);
}

.key-card.status-valid {
  border-color: var(--success-border);
  background: var(--success-bg);
  border-width: 1.5px;
}

/* 子分组专用样式 - 蓝色主题 */
.key-card.status-sub-group {
  border-color: #2080f0;
  background: #f0f7ff;
  border-width: 1.5px;
}

/* 暗黑模式下的子分组样式 */
:root.dark .key-card.status-sub-group {
  border-color: #4098fc;
  background: #1a2332;
}

/* 子分组名称样式 */
.sub-group-names {
  display: flex;
  align-items: baseline;
  flex: 1;
  min-width: 0;
}

.display-name {
  font-size: 16px;
  font-weight: 600;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex: 1;
}

.group-name {
  font-size: 13px;
  font-weight: 500;
  color: #2080f0;
  font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, Courier, monospace;
  background: #e6f4ff;
  padding: 2px 6px;
  border-radius: 4px;
  white-space: nowrap;
  flex-shrink: 0;
}

/* 暗黑模式下的分组名样式 */
:root.dark .group-name {
  background: #0f1419;
  color: #4098fc;
}

/* 权重显示样式 */
.weight-display {
  margin: 8px 0;
}

.weight-bar-container {
  display: flex;
  align-items: center;
  gap: 12px;
}

.weight-label {
  font-size: 12px;
  color: var(--text-secondary);
  white-space: nowrap;
}

.weight-label strong {
  color: var(--text-primary);
  font-weight: 600;
}

.key-main {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}

.key-section {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
}

.key-bottom {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}

.key-stats {
  display: flex;
  gap: 8px;
  font-size: 12px;
  overflow: hidden;
  color: var(--text-secondary);
  flex: 1;
  min-width: 0;
}

.stat-item {
  white-space: nowrap;
  color: var(--text-secondary);
}

.stat-item strong {
  color: var(--text-primary);
  font-weight: 600;
}

.key-actions {
  flex-shrink: 0;
}

.key-actions :deep(.n-button) {
  padding: 0 4px;
}

.key-text {
  font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, Courier, monospace;
  font-weight: 500;
  flex: 1;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
}

:root:not(.dark) .key-text {
  color: #495057;
  background: #f8f9fa;
}

:root.dark .key-text {
  color: var(--text-primary);
  background: var(--bg-tertiary);
}

:deep(.n-input__input-el) {
  font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, Courier, monospace;
  font-size: 13px;
}

.quick-actions {
  display: flex;
  gap: 4px;
  flex-shrink: 0;
}

.sub-group-id {
  font-size: 12px;
  color: var(--text-secondary);
  background: var(--bg-tertiary);
  padding: 2px 6px;
  border-radius: 4px;
}

.weight-bar {
  flex: 1;
  height: 8px;
  background: var(--bg-tertiary);
  border-radius: 4px;
  overflow: hidden;
}

.weight-fill {
  height: 100%;
  background: linear-gradient(90deg, #2080f0, #4098fc);
  border-radius: 4px;
  transition: width 0.3s ease;
}

.weight-text {
  font-weight: 600;
  color: var(--text-primary);
  font-size: 14px;
  min-width: 40px;
  text-align: right;
}

.pagination-container {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  background: var(--card-bg-solid);
  border-top: 1px solid var(--border-color);
  flex-shrink: 0;
  border-radius: 0 0 8px 8px;
}

.pagination-info {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 12px;
  color: var(--text-secondary);
}

.pagination-controls {
  display: flex;
  align-items: center;
  gap: 12px;
}

.page-info {
  font-size: 12px;
  color: var(--text-secondary);
}

.empty-container {
  display: flex;
  justify-content: center;
  align-items: center;
  height: 200px;
}

@media (max-width: 768px) {
  .toolbar {
    flex-direction: column;
    align-items: stretch;
    gap: 12px;
  }

  .toolbar-left,
  .toolbar-right {
    width: 100%;
    justify-content: space-between;
  }
}

/* 禁用状态样式 - 与密钥列表中禁用密钥的样式一致 */
.key-card.disabled {
  opacity: 0.6;
  background: var(--bg-secondary);
}

:root.dark .key-card.disabled {
  background: var(--bg-disabled);
}

.key-card.disabled .display-name,
.key-card.disabled .group-name,
.key-card.disabled .weight-label {
  color: var(--text-disabled);
}

.key-card.disabled .weight-fill {
  background: var(--color-disabled);
}
</style>
