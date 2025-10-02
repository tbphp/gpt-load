<script setup lang="ts">
import type { Group } from "@/types/models";
import { getGroupDisplayName } from "@/utils/display";
import { Add, Search } from "@vicons/ionicons5";
import { NButton, NCard, NEmpty, NInput, NSpin, NTag } from "naive-ui";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
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
const groupItemRefs = ref(new Map());

// 过滤后的分组列表
const filteredGroups = computed(() => {
  if (!searchText.value) {
    return props.groups;
  }
  const search = searchText.value.toLowerCase();
  return props.groups.filter(
    group =>
      group.name.toLowerCase().includes(search) ||
      (group.display_name && group.display_name.toLowerCase().includes(search))
  );
});

// 监听选中项 ID 的变化，并自动滚动到该项
watch(
  () => props.selectedGroup?.id,
  id => {
    if (!id || props.groups.length === 0) {
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
  emit("group-select", group);
}

// 获取渠道类型的标签颜色
function getChannelTagType(channelType: string) {
  switch (channelType) {
    case "openai":
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

function handleGroupCreated(group: Group) {
  showGroupModal.value = false;
  if (group && group.id) {
    // 创建成功后，通知父组件刷新并切换到新创建的分组
    emit("refresh-and-select", group.id);
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
              :class="{ active: selectedGroup?.id === group.id }"
              @click="handleGroupClick(group)"
              :ref="
                el => {
                  if (el) groupItemRefs.set(group.id, el);
                }
              "
            >
              <div class="group-icon">
                <svg
                  v-if="group.channel_type === 'openai'"
                  fill="currentColor"
                  fill-rule="evenodd"
                  height="14"
                  viewBox="0 0 24 24"
                  width="14"
                  xmlns="http://www.w3.org/2000/svg"
                  style="flex: 0 0 auto; line-height: 1"
                >
                  <title>OpenAI</title>
                  <path
                    d="M21.55 10.004a5.416 5.416 0 00-.478-4.501c-1.217-2.09-3.662-3.166-6.05-2.66A5.59 5.59 0 0010.831 1C8.39.995 6.224 2.546 5.473 4.838A5.553 5.553 0 001.76 7.496a5.487 5.487 0 00.691 6.5 5.416 5.416 0 00.477 4.502c1.217 2.09 3.662 3.165 6.05 2.66A5.586 5.586 0 0013.168 23c2.443.006 4.61-1.546 5.361-3.84a5.553 5.553 0 003.715-2.66 5.488 5.488 0 00-.693-6.497v.001zm-8.381 11.558a4.199 4.199 0 01-2.675-.954c.034-.018.093-.05.132-.074l4.44-2.53a.71.71 0 00.364-.623v-6.176l1.877 1.069c.02.01.033.029.036.05v5.115c-.003 2.274-1.87 4.118-4.174 4.123zM4.192 17.78a4.059 4.059 0 01-.498-2.763c.032.02.09.055.131.078l4.44 2.53c.225.13.504.13.73 0l5.42-3.088v2.138a.068.068 0 01-.027.057L9.9 19.288c-1.999 1.136-4.552.46-5.707-1.51h-.001zM3.023 8.216A4.15 4.15 0 015.198 6.41l-.002.151v5.06a.711.711 0 00.364.624l5.42 3.087-1.876 1.07a.067.067 0 01-.063.005l-4.489-2.559c-1.995-1.14-2.679-3.658-1.53-5.63h.001zm15.417 3.54l-5.42-3.088L14.896 7.6a.067.067 0 01.063-.006l4.489 2.557c1.998 1.14 2.683 3.662 1.529 5.633a4.163 4.163 0 01-2.174 1.807V12.38a.71.71 0 00-.363-.623zm1.867-2.773a6.04 6.04 0 00-.132-.078l-4.44-2.53a.731.731 0 00-.729 0l-5.42 3.088V7.325a.068.068 0 01.027-.057L14.1 4.713c2-1.137 4.555-.46 5.707 1.513.487.833.664 1.809.499 2.757h.001zm-11.741 3.81l-1.877-1.068a.065.065 0 01-.036-.051V6.559c.001-2.277 1.873-4.122 4.181-4.12.976 0 1.92.338 2.671.954-.034.018-.092.05-.131.073l-4.44 2.53a.71.71 0 00-.365.623l-.003 6.173v.002zm1.02-2.168L12 9.25l2.414 1.375v2.75L12 14.75l-2.415-1.375v-2.75z"
                  />
                </svg>
                <svg
                  v-else-if="group.channel_type === 'gemini'"
                  height="14"
                  viewBox="0 0 24 24"
                  width="14"
                  xmlns="http://www.w3.org/2000/svg"
                  style="flex: 0 0 auto; line-height: 1"
                  aria-hidden="true"
                  focusable="false"
                >
                  <defs>
                    <linearGradient
                      :id="`gemini-fill-${group.id}`"
                      x1="0%"
                      x2="68.73%"
                      y1="100%"
                      y2="30.395%"
                    >
                      <stop offset="0%" stop-color="#1C7DFF" />
                      <stop offset="52.021%" stop-color="#1C69FF" />
                      <stop offset="100%" stop-color="#F0DCD6" />
                    </linearGradient>
                  </defs>
                  <path
                    d="M12 24A14.304 14.304 0 000 12 14.304 14.304 0 0012 0a14.305 14.305 0 0012 12 14.305 14.305 0 00-12 12"
                    :fill="`url(#gemini-fill-${group.id})`"
                    fill-rule="nonzero"
                  />
                </svg>
                <svg
                  v-else-if="group.channel_type === 'anthropic'"
                  height="14"
                  viewBox="0 0 24 24"
                  width="14"
                  xmlns="http://www.w3.org/2000/svg"
                  style="flex: 0 0 auto; line-height: 1"
                >
                  <title>Claude</title>
                  <path
                    d="M4.709 15.955l4.72-2.647.08-.23-.08-.128H9.2l-.79-.048-2.698-.073-2.339-.097-2.266-.122-.571-.121L0 11.784l.055-.352.48-.321.686.06 1.52.103 2.278.158 1.652.097 2.449.255h.389l.055-.157-.134-.098-.103-.097-2.358-1.596-2.552-1.688-1.336-.972-.724-.491-.364-.462-.158-1.008.656-.722.881.06.225.061.893.686 1.908 1.476 2.491 1.833.365.304.145-.103.019-.073-.164-.274-1.355-2.446-1.446-2.49-.644-1.032-.17-.619a2.97 2.97 0 01-.104-.729L6.283.134 6.696 0l.996.134.42.364.62 1.414 1.002 2.229 1.555 3.03.456.898.243.832.091.255h.158V9.01l.128-1.706.237-2.095.23-2.695.08-.76.376-.91.747-.492.584.28.48.685-.067.444-.286 1.851-.559 2.903-.364 1.942h.212l.243-.242.985-1.306 1.652-2.064.73-.82.85-.904.547-.431h1.033l.76 1.129-.34 1.166-1.064 1.347-.881 1.142-1.264 1.7-.79 1.36.073.11.188-.02 2.856-.606 1.543-.28 1.841-.315.833.388.091.395-.328.807-1.969.486-2.309.462-3.439.813-.042.03.049.061 1.549.146.662.036h1.622l3.02.225.79.522.474.638-.079.485-1.215.62-1.64-.389-3.829-.91-1.312-.329h-.182v.11l1.093 1.068 2.006 1.81 2.509 2.33.127.578-.322.455-.34-.049-2.205-1.657-.851-.747-1.926-1.62h-.128v.17l.444.649 2.345 3.521.122 1.08-.17.353-.608.213-.668-.122-1.374-1.925-1.415-2.167-1.143-1.943-.14.08-.674 7.254-.316.37-.729.28-.607-.461-.322-.747.322-1.476.389-1.924.315-1.53.286-1.9.17-.632-.012-.042-.14.018-1.434 1.967-2.18 2.945-1.726 1.845-.414.164-.717-.37.067-.662.401-.589 2.388-3.036 1.44-1.882.93-1.086-.006-.158h-.055L4.132 18.56l-1.13.146-.487-.456.061-.746.231-.243 1.908-1.312-.006.006z"
                    fill="#D97757"
                    fill-rule="nonzero"
                  />
                </svg>
                <svg
                  v-else
                  height="14"
                  viewBox="0 0 24 24"
                  width="14"
                  xmlns="http://www.w3.org/2000/svg"
                  style="flex: 0 0 auto; line-height: 1"
                  aria-hidden="true"
                  focusable="false"
                  fill="currentColor"
                >
                  <title>Settings</title>
                  <circle cx="12" cy="12" r="3" />
                  <path
                    d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"
                  />
                </svg>
              </div>
              <div class="group-content">
                <div class="group-name">{{ getGroupDisplayName(group) }}</div>
                <div class="group-meta">
                  <n-tag size="tiny" :type="getChannelTagType(group.channel_type)">
                    {{ group.channel_type }}
                  </n-tag>
                  <span class="group-id">#{{ group.name }}</span>
                </div>
              </div>
            </div>
          </div>
        </n-spin>
      </div>

      <!-- 添加分组按钮 -->
      <div class="add-section">
        <n-button type="primary" size="small" block @click="openCreateGroupModal">
          <template #icon>
            <n-icon :component="Add" />
          </template>
          {{ t("keys.createGroup") }}
        </n-button>
      </div>
    </n-card>
    <group-form-modal v-model:show="showGroupModal" @success="handleGroupCreated" />
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
  height: calc(100% - 82px);
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
}

.group-item:hover {
  background: var(--bg-tertiary);
  border-color: var(--border-color-light);
}

:root.dark .group-item:hover {
  background: rgba(255, 255, 255, 0.05);
  border-color: rgba(255, 255, 255, 0.1);
}

.group-item.active,
:root.dark .group-item.active {
  background: #f6f8fa;
  color: var(--text-primary);
  border-color: #d1d9e0;
  box-shadow: none;
}

:root.dark .group-item.active {
  background: rgba(255, 255, 255, 0.08);
  color: var(--text-primary);
  border-color: rgba(255, 255, 255, 0.15);
}

:root:not(.dark) .group-item.active {
  background: #e3f2fd;
  color: var(--text-primary);
  border-color: #2196f3;
  box-shadow: 0 0 0 1px rgba(33, 150, 243, 0.1);
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
}

.group-item.active .group-icon {
  background: white;
  border: 1px solid #d1d9e0;
}

:root:not(.dark) .group-item.active .group-icon {
  background: white;
  border: 1px solid #2196f3;
  box-shadow: 0 1px 3px rgba(33, 150, 243, 0.1);
}

:root.dark .group-item.active .group-icon {
  background: rgba(255, 255, 255, 0.12);
  border: 1px solid rgba(255, 255, 255, 0.2);
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
}

.group-id {
  opacity: 0.8;
  color: var(--text-secondary);
}

.group-item.active .group-id {
  opacity: 0.8;
  color: var(--text-secondary);
}

.add-section {
  border-top: 1px solid var(--border-color);
  padding-top: 12px;
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
</style>
