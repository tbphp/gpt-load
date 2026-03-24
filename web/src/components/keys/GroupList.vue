<script setup lang="ts">
import { keysApi } from "@/api/keys";
import type { Group } from "@/types/models";
import { getGroupDisplayName } from "@/utils/display";
import { Add, LinkOutline, Search } from "@vicons/ionicons5";
import { NButton, NCard, NEmpty, NInput, NSpin, NTag } from "naive-ui";
import { computed, onBeforeUpdate, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import AggregateGroupModal from "./AggregateGroupModal.vue";
import GroupFormModal from "./GroupFormModal.vue";

const { t } = useI18n();

interface Props {
  groups: Group[];
  selectedGroup: Group | null;
  loading?: boolean;
}

interface Emits {
  (e: "group-select", group: Group): void;
  (e: "refresh"): void;
  (e: "refresh-and-select", groupId: number): void;
}

const props = withDefaults(defineProps<Props>(), {
  loading: false,
});

const emit = defineEmits<Emits>();

const searchText = ref("");
const showGroupModal = ref(false);
// 存储分组项 DOM 元素的引用
const groupItemRefs = ref<Map<number, HTMLElement>>(new Map());
const showAggregateGroupModal = ref(false);
const displayGroups = ref<Group[]>([]);
const draggingGroupId = ref<number | null>(null);
const dropTarget = ref<{ groupId: number; position: "before" | "after" } | null>(null);
const savingOrder = ref(false);
const suspendAutoScroll = ref(false);

const isTouchDevice = computed(() => {
  if (typeof window === "undefined") {
    return false;
  }
  return "ontouchstart" in window || navigator.maxTouchPoints > 0;
});

const hasSearchFilter = computed(() => Boolean(searchText.value.trim()));

const canDrag = computed(
  () =>
    !props.loading &&
    !savingOrder.value &&
    !hasSearchFilter.value &&
    !isTouchDevice.value &&
    displayGroups.value.length > 1
);

const dragDisabledHint = computed(() => {
  if (hasSearchFilter.value) {
    return t("keys.dragSortHint");
  }
  if (isTouchDevice.value) {
    return t("keys.dragSortTouchDisabled");
  }
  return "";
});

watch(
  () => props.groups,
  groups => {
    if (savingOrder.value) {
      return;
    }
    displayGroups.value = groups.map(group => ({ ...group }));
    if (suspendAutoScroll.value) {
      suspendAutoScroll.value = false;
    }
  },
  {
    immediate: true,
    deep: true,
  }
);

onBeforeUpdate(() => {
  groupItemRefs.value.clear();
});

const filteredGroups = computed(() => {
  if (!searchText.value.trim()) {
    return displayGroups.value;
  }
  const search = searchText.value.toLowerCase().trim();
  return displayGroups.value.filter(
    group =>
      group.name.toLowerCase().includes(search) ||
      group.display_name?.toLowerCase().includes(search)
  );
});

// 监听选中项 ID 的变化，并自动滚动到该项
watch(
  () => props.selectedGroup?.id,
  id => {
    if (!id || displayGroups.value.length === 0 || suspendAutoScroll.value) {
      return;
    }

    const element = groupItemRefs.value.get(id);
    if (element) {
      element.scrollIntoView({
        behavior: "smooth", // 平滑滚动
        block: "nearest", // 将元素滚动到最近的边缘
      });
    }
  },
  {
    flush: "post", // 确保在 DOM 更新后执行回调
    immediate: true, // 立即执行一次以处理初始加载
  }
);

function handleGroupClick(group: Group) {
  if (draggingGroupId.value || savingOrder.value) {
    return;
  }
  emit("group-select", group);
}

// 获取渠道类型的标签颜色
function getChannelTagType(channelType: string) {
  switch (channelType) {
    case "openai":
    case "openai-response":
      return "success";
    case "gemini":
      return "info";
    case "anthropic":
      return "warning";
    default:
      return "default";
  }
}

function openCreateGroupModal() {
  showGroupModal.value = true;
}

function openCreateAggregateGroupModal() {
  showAggregateGroupModal.value = true;
}

function handleGroupCreated(group: Group) {
  showGroupModal.value = false;
  showAggregateGroupModal.value = false;
  if (group?.id) {
    emit("refresh-and-select", group.id);
  }
}

function setGroupItemRef(el: Element | null, groupId?: number) {
  if (el instanceof HTMLElement && groupId) {
    groupItemRefs.value.set(groupId, el);
  }
}

function reorderInMemory(
  sourceGroupId: number,
  targetGroupId: number,
  position: "before" | "after"
): boolean {
  const sourceIndex = displayGroups.value.findIndex(group => group.id === sourceGroupId);
  const targetIndex = displayGroups.value.findIndex(group => group.id === targetGroupId);

  if (sourceIndex < 0 || targetIndex < 0) {
    return false;
  }

  const reordered = [...displayGroups.value];
  const [moved] = reordered.splice(sourceIndex, 1);

  let insertIndex = targetIndex;
  if (sourceIndex < targetIndex) {
    insertIndex -= 1;
  }
  if (position === "after") {
    insertIndex += 1;
  }

  if (insertIndex < 0) {
    insertIndex = 0;
  }
  if (insertIndex > reordered.length) {
    insertIndex = reordered.length;
  }

  if (insertIndex === sourceIndex) {
    return false;
  }

  reordered.splice(insertIndex, 0, moved);
  displayGroups.value = reordered;
  return true;
}

async function persistGroupOrder(previousOrder: Group[]) {
  const previousSortMap = new Map<number, number>();
  previousOrder.forEach(group => {
    if (group.id) {
      previousSortMap.set(group.id, group.sort);
    }
  });

  const items: { id: number; sort: number }[] = [];
  displayGroups.value.forEach((group, index) => {
    if (!group.id) {
      return;
    }
    const targetSort = (index + 1) * 10;
    if (previousSortMap.get(group.id) !== targetSort) {
      items.push({ id: group.id, sort: targetSort });
    }
    group.sort = targetSort;
  });

  if (items.length === 0) {
    suspendAutoScroll.value = false;
    return;
  }

  try {
    savingOrder.value = true;
    await keysApi.reorderGroups(items);
    window.$message?.success(t("keys.dragSortSaved"));
    emit("refresh");
  } catch (error) {
    console.error("Failed to reorder groups:", error);
    displayGroups.value = previousOrder.map(group => ({ ...group }));
    window.$message?.error(t("keys.dragSortSaveFailed"));
    emit("refresh");
  } finally {
    savingOrder.value = false;
    suspendAutoScroll.value = false;
  }
}

function handleDragStart(event: DragEvent, groupId?: number) {
  if (!canDrag.value || !groupId) {
    return;
  }

  event.stopPropagation();
  draggingGroupId.value = groupId;
  dropTarget.value = null;
  suspendAutoScroll.value = true;

  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("text/plain", String(groupId));
  }
}

function resolveDropPosition(event: DragEvent, targetGroupId: number): "before" | "after" {
  const element = groupItemRefs.value.get(targetGroupId);
  if (!element) {
    return "after";
  }
  const rect = element.getBoundingClientRect();
  return event.clientY < rect.top + rect.height / 2 ? "before" : "after";
}

function handleDragOver(event: DragEvent, targetGroupId?: number) {
  if (!canDrag.value || !draggingGroupId.value || !targetGroupId) {
    return;
  }

  event.preventDefault();
  event.stopPropagation();

  const nextPosition = resolveDropPosition(event, targetGroupId);
  if (
    !dropTarget.value ||
    dropTarget.value.groupId !== targetGroupId ||
    dropTarget.value.position !== nextPosition
  ) {
    dropTarget.value = { groupId: targetGroupId, position: nextPosition };
  }

  if (event.dataTransfer) {
    event.dataTransfer.dropEffect = "move";
  }
}

async function handleDrop(event: DragEvent, targetGroupId?: number) {
  event.preventDefault();
  event.stopPropagation();

  const sourceGroupId = draggingGroupId.value;
  const target = dropTarget.value;
  draggingGroupId.value = null;
  dropTarget.value = null;

  if (
    !canDrag.value ||
    !sourceGroupId ||
    !targetGroupId ||
    !target ||
    sourceGroupId === targetGroupId
  ) {
    if (!savingOrder.value) {
      suspendAutoScroll.value = false;
    }
    return;
  }

  const previousOrder = displayGroups.value.map(group => ({ ...group }));
  const changed = reorderInMemory(sourceGroupId, targetGroupId, target.position);
  if (!changed) {
    suspendAutoScroll.value = false;
    return;
  }

  await persistGroupOrder(previousOrder);
}

function handleDragEnd() {
  draggingGroupId.value = null;
  dropTarget.value = null;
  if (!savingOrder.value) {
    suspendAutoScroll.value = false;
  }
}
</script>

<template>
  <div class="group-list-container">
    <n-card class="group-list-card modern-card" :bordered="false" size="small">
      <!-- 搜索框 -->
      <div class="search-section">
        <n-input
          v-model:value="searchText"
          :placeholder="t('keys.searchGroupPlaceholder')"
          size="small"
          clearable
        >
          <template #prefix>
            <n-icon :component="Search" />
          </template>
        </n-input>
      </div>

      <!-- 分组列表 -->
      <div class="groups-section">
        <n-spin :show="loading" size="small">
          <div v-if="filteredGroups.length === 0 && !loading" class="empty-container">
            <n-empty
              size="small"
              :description="searchText ? t('keys.noMatchingGroups') : t('keys.noGroups')"
            />
          </div>
          <div v-else class="groups-list">
            <div
              v-for="group in filteredGroups"
              :key="group.id"
              class="group-item"
              :class="{
                active: selectedGroup?.id === group.id,
                aggregate: group.group_type === 'aggregate',
                dragging: draggingGroupId === group.id,
                'drop-before':
                  dropTarget?.groupId === group.id &&
                  dropTarget?.position === 'before' &&
                  draggingGroupId !== group.id,
                'drop-after':
                  dropTarget?.groupId === group.id &&
                  dropTarget?.position === 'after' &&
                  draggingGroupId !== group.id,
              }"
              @click="handleGroupClick(group)"
              @dragover="handleDragOver($event, group.id)"
              @drop="handleDrop($event, group.id)"
              :ref="
                el => {
                  setGroupItemRef(el as Element | null, group.id);
                }
              "
            >
              <div
                class="group-icon"
                :class="{ 'drag-disabled': !canDrag }"
                :draggable="canDrag"
                :role="'button'"
                :aria-label="t('keys.dragHandle')"
                :aria-describedby="dragDisabledHint ? `drag-hint-${group.id}` : undefined"
                @dragstart="handleDragStart($event, group.id)"
                @dragend="handleDragEnd"
              >
                <span v-if="group.group_type === 'aggregate'">🔗</span>
                <span v-else-if="group.channel_type === 'openai'">🤖</span>
                <span v-else-if="group.channel_type === 'openai-response'">🔁</span>
                <span v-else-if="group.channel_type === 'gemini'">💎</span>
                <span v-else-if="group.channel_type === 'anthropic'">🧠</span>
                <span v-else>🔧</span>
              </div>
              <div class="group-content">
                <div class="group-name">{{ getGroupDisplayName(group) }}</div>
                <div class="group-meta">
                  <n-tag size="tiny" :type="getChannelTagType(group.channel_type)">
                    {{ group.channel_type }}
                  </n-tag>
                  <n-tag v-if="group.group_type === 'aggregate'" size="tiny" type="warning" round>
                    {{ t("keys.aggregateGroup") }}
                  </n-tag>
                  <span v-if="group.group_type !== 'aggregate'" class="group-id">
                    #{{ group.name }}
                  </span>
                </div>
              </div>
              <span v-if="dragDisabledHint" :id="`drag-hint-${group.id}`" class="sr-only">
                {{ dragDisabledHint }}
              </span>
            </div>
          </div>
        </n-spin>
      </div>

      <!-- 添加分组按钮 -->
      <div class="add-section">
        <n-button type="success" size="small" block @click="openCreateGroupModal">
          <template #icon>
            <n-icon :component="Add" />
          </template>
          {{ t("keys.createGroup") }}
        </n-button>
        <n-button type="info" size="small" block @click="openCreateAggregateGroupModal">
          <template #icon>
            <n-icon :component="LinkOutline" />
          </template>
          {{ t("keys.createAggregateGroup") }}
        </n-button>
      </div>
    </n-card>
    <group-form-modal v-model:show="showGroupModal" @success="handleGroupCreated" />
    <aggregate-group-modal
      v-model:show="showAggregateGroupModal"
      :groups="groups"
      @success="handleGroupCreated"
    />
  </div>
</template>

<style scoped>
:deep(.n-card__content) {
  height: 100%;
}

.groups-section::-webkit-scrollbar {
  width: 1px;
  height: 1px;
}

.group-list-container {
  height: 100%;
}

.group-list-card {
  height: 100%;
  display: flex;
  flex-direction: column;
  background: var(--card-bg-solid);
}

.group-list-card:hover {
  transform: none;
  box-shadow: var(--shadow-lg);
}

.search-section {
  height: 41px;
}

.groups-section {
  flex: 1;
  height: calc(100% - 120px);
  overflow: auto;
}

.empty-container {
  padding: 20px 0;
}

.groups-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-height: 100%;
  overflow-y: auto;
  width: 100%;
}

.group-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s ease;
  border: 1px solid var(--border-color);
  font-size: 12px;
  color: var(--text-primary);
  background: transparent;
  box-sizing: border-box;
  position: relative;
}

.group-item.dragging {
  opacity: 0.6;
}

.group-item.drop-before::before,
.group-item.drop-after::after {
  content: "";
  position: absolute;
  left: 8px;
  right: 8px;
  height: 3px;
  border-radius: 3px;
  background: var(--primary-color);
  box-shadow: 0 0 0 2px rgba(102, 126, 234, 0.15);
  pointer-events: none;
}

.group-item.drop-before::before {
  top: -4px;
}

.group-item.drop-after::after {
  bottom: -4px;
}

:root.dark .group-item.drop-before::before,
:root.dark .group-item.drop-after::after {
  box-shadow: 0 0 0 2px rgba(102, 126, 234, 0.3);
}

/* 聚合分组样式 */
.group-item.aggregate {
  border-style: dashed;
  background: linear-gradient(135deg, rgba(102, 126, 234, 0.02) 0%, rgba(102, 126, 234, 0.05) 100%);
}

:root.dark .group-item.aggregate {
  background: linear-gradient(135deg, rgba(102, 126, 234, 0.05) 0%, rgba(102, 126, 234, 0.1) 100%);
  border-color: rgba(102, 126, 234, 0.2);
}

.group-item:hover,
.group-item.aggregate:hover {
  background: var(--bg-tertiary);
  border-color: var(--primary-color);
}

.group-item.aggregate:hover {
  background: linear-gradient(135deg, rgba(102, 126, 234, 0.05) 0%, rgba(102, 126, 234, 0.1) 100%);
  border-style: dashed;
}

:root.dark .group-item:hover {
  background: rgba(102, 126, 234, 0.1);
  border-color: rgba(102, 126, 234, 0.3);
}

:root.dark .group-item.aggregate:hover {
  background: linear-gradient(135deg, rgba(102, 126, 234, 0.1) 0%, rgba(102, 126, 234, 0.15) 100%);
  border-color: rgba(102, 126, 234, 0.4);
}

.group-item.aggregate.active {
  background: var(--primary-gradient);
  border-style: solid;
}

.group-item.active,
:root.dark .group-item.active,
:root.dark .group-item.aggregate.active {
  background: var(--primary-gradient);
  color: white;
  border-color: transparent;
  box-shadow: var(--shadow-md);
  border-style: solid;
}

.group-icon {
  font-size: 16px;
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg-secondary);
  border-radius: 6px;
  flex-shrink: 0;
  box-sizing: border-box;
  cursor: grab;
  user-select: none;
}

.group-item.active .group-icon {
  background: rgba(255, 255, 255, 0.2);
}

.group-item.dragging .group-icon {
  cursor: grabbing;
}

.group-icon.drag-disabled {
  cursor: not-allowed;
  opacity: 0.65;
}

.group-content {
  flex: 1;
  min-width: 0;
}

.group-name {
  font-weight: 600;
  font-size: 14px;
  line-height: 1.2;
  margin-bottom: 4px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.group-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 10px;
  flex-wrap: wrap;
}

.group-id {
  opacity: 0.8;
  color: var(--text-secondary);
}

.group-item.active .group-id {
  opacity: 0.9;
  color: white;
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}

.add-section {
  border-top: 1px solid var(--border-color);
  padding-top: 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

/* 滚动条样式 */
.groups-list::-webkit-scrollbar {
  width: 4px;
}

.groups-list::-webkit-scrollbar-track {
  background: transparent;
}

.groups-list::-webkit-scrollbar-thumb {
  background: var(--scrollbar-bg);
  border-radius: 2px;
}

.groups-list::-webkit-scrollbar-thumb:hover {
  background: var(--border-color);
}

/* 暗黑模式特殊样式 */
:root.dark .group-item {
  border-color: rgba(255, 255, 255, 0.05);
}

:root.dark .group-icon {
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.08);
}

:root.dark .search-section :deep(.n-input) {
  --n-border: 1px solid rgba(255, 255, 255, 0.08);
  --n-border-hover: 1px solid rgba(102, 126, 234, 0.4);
  --n-border-focus: 1px solid var(--primary-color);
  background: rgba(255, 255, 255, 0.03);
}

/* 标签样式优化 */
:root.dark .group-meta :deep(.n-tag) {
  background: rgba(102, 126, 234, 0.15);
  border: 1px solid rgba(102, 126, 234, 0.3);
}

:root.dark .group-item.active .group-meta :deep(.n-tag) {
  background: rgba(255, 255, 255, 0.2);
  border-color: rgba(255, 255, 255, 0.3);
}
</style>
