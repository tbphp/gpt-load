<script setup lang="ts">
import { keysApi } from "@/api/keys";
import type { APIKey, Group, KeyStatus } from "@/types/models";
import { appState } from "@/utils/app-state";
import { getGroupDisplayName, maskKey } from "@/utils/display";
import {
  AddCircleOutline,
  AlertCircleOutline,
  CheckmarkCircle,
  CopyOutline,
  EyeOffOutline,
  EyeOutline,
  RemoveCircleOutline,
  Search,
} from "@vicons/ionicons5";
import {
  NButton,
  NDropdown,
  NEmpty,
  NIcon,
  NInput,
  NSelect,
  NSpace,
  NSpin,
  NTag,
  useDialog,
  type MessageReactive,
} from "naive-ui";
import { ref, watch } from "vue";
import KeyCreateDialog from "./KeyCreateDialog.vue";
import KeyDeleteDialog from "./KeyDeleteDialog.vue";

interface KeyRow extends APIKey {
  is_visible: boolean;
}

interface Props {
  selectedGroup: Group | null;
}

const props = defineProps<Props>();

const keys = ref<KeyRow[]>([]);
const loading = ref(false);
const searchText = ref("");
const statusFilter = ref<"all" | "active" | "invalid">("all");
const currentPage = ref(1);
const pageSize = ref(12);
const total = ref(0);
const totalPages = ref(0);
const dialog = useDialog();

// 状态过滤选项
const statusOptions = [
  { label: "全部", value: "all" },
  { label: "有效", value: "active" },
  { label: "无效", value: "invalid" },
];

// 更多操作下拉菜单选项
const moreOptions = [
  // { label: "导出所有密钥", key: "copyAll" },
  // { label: "导出有效密钥", key: "copyValid" },
  // { label: "导出无效密钥", key: "copyInvalid" },
  { type: "divider" },
  { label: "恢复所有无效密钥", key: "restoreAll" },
  { label: "清空所有无效密钥", key: "clearInvalid", props: { style: { color: "#d03050" } } },
  { type: "divider" },
  { label: "验证所有密钥", key: "validateAll" },
];

let testingMsg: MessageReactive | null = null;
const isDeling = ref(false);
const isRestoring = ref(false);

const createDialogShow = ref(false);
const deleteDialogShow = ref(false);

watch(
  () => props.selectedGroup,
  async newGroup => {
    if (newGroup) {
      // 检查重置页面是否会触发分页观察者。
      const willWatcherTrigger = currentPage.value !== 1 || statusFilter.value !== "all";
      resetPage();
      // 如果分页观察者不触发，则手动加载。
      if (!willWatcherTrigger) {
        await loadKeys();
      }
    }
  },
  { immediate: true }
);

watch([currentPage, pageSize, statusFilter], async () => {
  await loadKeys();
});

// 处理搜索输入的防抖
function handleSearchInput() {
  currentPage.value = 1; // 搜索时重置到第一页
  loadKeys();
}

// 处理更多操作菜单
function handleMoreAction(key: string) {
  switch (key) {
    case "copyAll":
      copyAllKeys();
      break;
    case "copyValid":
      copyValidKeys();
      break;
    case "copyInvalid":
      copyInvalidKeys();
      break;
    case "restoreAll":
      restoreAllInvalid();
      break;
    case "validateAll":
      validateAllKeys();
      break;
    case "clearInvalid":
      clearAllInvalid();
      break;
  }
}

async function loadKeys() {
  if (!props.selectedGroup?.id) {
    return;
  }

  try {
    loading.value = true;
    const result = await keysApi.getGroupKeys({
      group_id: props.selectedGroup.id,
      page: currentPage.value,
      page_size: pageSize.value,
      status: statusFilter.value === "all" ? undefined : (statusFilter.value as KeyStatus),
      key: searchText.value.trim() || undefined,
    });
    keys.value = result.items as KeyRow[];
    total.value = result.pagination.total_items;
    totalPages.value = result.pagination.total_pages;
  } catch (_error) {
    window.$message.error("加载密钥失败");
  } finally {
    loading.value = false;
  }
}

function copyKey(key: KeyRow) {
  navigator.clipboard
    .writeText(key.key_value)
    .then(() => {
      window.$message.success("密钥已复制到剪贴板");
    })
    .catch(() => {
      window.$message.error("复制失败");
    });
}

async function testKey(_key: KeyRow) {
  if (!props.selectedGroup?.id || !_key.key_value || testingMsg) {
    return;
  }

  testingMsg = window.$message.info("正在测试密钥...", {
    duration: 0,
  });

  try {
    const res = await keysApi.testKeys(props.selectedGroup.id, _key.key_value);
    const curValid = res?.[0] || {};
    _key.status = curValid.is_valid ? "active" : "invalid";
    if (curValid.is_valid) {
      window.$message.success("密钥测试成功");
    } else {
      window.$message.error(curValid.error || "密钥测试失败: 无效的API密钥", {
        keepAliveOnHover: true,
        duration: 5000,
        closable: true,
      });
    }
  } catch (_error) {
    console.error("测试失败");
  } finally {
    testingMsg?.destroy();
    testingMsg = null;
  }
}

function toggleKeyVisibility(key: KeyRow) {
  key.is_visible = !key.is_visible;
}

async function restoreKey(key: KeyRow) {
  if (!props.selectedGroup?.id || !key.key_value || isRestoring.value) {
    return;
  }

  const d = dialog.warning({
    title: "恢复密钥",
    content: `确定要恢复密钥"${maskKey(key.key_value)}"吗？`,
    positiveText: "确定",
    negativeText: "取消",
    onPositiveClick: async () => {
      if (!props.selectedGroup?.id) {
        return;
      }

      isRestoring.value = true;
      d.loading = true;

      try {
        await keysApi.restoreKeys(props.selectedGroup.id, key.key_value);
        await loadKeys();
      } catch (_error) {
        console.error("恢复失败");
      } finally {
        d.loading = false;
        isRestoring.value = false;
      }
    },
  });
}

async function deleteKey(key: KeyRow) {
  if (!props.selectedGroup?.id || !key.key_value || isDeling.value) {
    return;
  }

  const d = dialog.warning({
    title: "删除密钥",
    content: `确定要删除密钥"${maskKey(key.key_value)}"吗？`,
    positiveText: "确定",
    negativeText: "取消",
    onPositiveClick: async () => {
      if (!props.selectedGroup?.id) {
        return;
      }

      d.loading = true;
      isDeling.value = true;

      try {
        await keysApi.deleteKeys(props.selectedGroup.id, key.key_value);
        await loadKeys();
      } catch (_error) {
        console.error("删除失败");
      } finally {
        d.loading = false;
        isDeling.value = false;
      }
    },
  });
}

function formatRelativeTime(date: string) {
  if (!date) {
    return "从未";
  }
  const now = new Date();
  const target = new Date(date);
  const diffSeconds = Math.floor((now.getTime() - target.getTime()) / 1000);
  const diffMinutes = Math.floor(diffSeconds / 60);
  const diffHours = Math.floor(diffMinutes / 60);
  const diffDays = Math.floor(diffHours / 24);

  if (diffDays > 0) {
    return `${diffDays}天前`;
  }
  if (diffHours > 0) {
    return `${diffHours}小时前`;
  }
  if (diffMinutes > 0) {
    return `${diffMinutes}分钟前`;
  }
  if (diffSeconds > 0) {
    return `${diffSeconds}秒前`;
  }
  return "刚刚";
}

function getStatusClass(status: KeyStatus): string {
  switch (status) {
    case "active":
      return "status-valid";
    case "invalid":
      return "status-invalid";
    default:
      return "status-unknown";
  }
}

async function copyAllKeys() {
  if (!props.selectedGroup?.id) {
    return;
  }

  keysApi.exportKeys(props.selectedGroup.id, "all");
}

async function copyValidKeys() {
  if (!props.selectedGroup?.id) {
    return;
  }

  keysApi.exportKeys(props.selectedGroup.id, "active");
}

async function copyInvalidKeys() {
  if (!props.selectedGroup?.id) {
    return;
  }

  keysApi.exportKeys(props.selectedGroup.id, "invalid");
}

async function restoreAllInvalid() {
  if (!props.selectedGroup?.id || isRestoring.value) {
    return;
  }

  const d = dialog.warning({
    title: "恢复密钥",
    content: "确定要恢复所有无效密钥吗？",
    positiveText: "确定",
    negativeText: "取消",
    onPositiveClick: async () => {
      if (!props.selectedGroup?.id) {
        return;
      }

      isRestoring.value = true;
      d.loading = true;
      try {
        await keysApi.restoreAllInvalidKeys(props.selectedGroup.id);
        await loadKeys();
      } catch (_error) {
        console.error("恢复失败");
      } finally {
        d.loading = false;
        isRestoring.value = false;
      }
    },
  });
}

async function validateAllKeys() {
  if (!props.selectedGroup?.id || testingMsg) {
    return;
  }

  testingMsg = window.$message.info("正在验证密钥...", {
    duration: 0,
  });

  try {
    await keysApi.validateGroupKeys(props.selectedGroup.id);
    localStorage.removeItem("last_closed_task");
    appState.taskPollingTrigger++;
  } catch (_error) {
    console.error("测试失败");
  } finally {
    testingMsg?.destroy();
    testingMsg = null;
  }
}

async function clearAllInvalid() {
  if (!props.selectedGroup?.id || isDeling.value) {
    return;
  }

  const d = dialog.warning({
    title: "清除密钥",
    content: "确定要清除所有无效密钥吗？此操作不可恢复！",
    positiveText: "确定",
    negativeText: "取消",
    onPositiveClick: async () => {
      if (!props.selectedGroup?.id) {
        return;
      }

      isDeling.value = true;
      d.loading = true;
      try {
        const { data } = await keysApi.clearAllInvalidKeys(props.selectedGroup.id);
        window.$message.success(data?.message || "清除成功");
        await loadKeys();
      } catch (_error) {
        console.error("删除失败");
      } finally {
        d.loading = false;
        isDeling.value = false;
      }
    },
  });
}

function changePage(page: number) {
  currentPage.value = page;
}

function changePageSize(size: number) {
  pageSize.value = size;
  currentPage.value = 1;
}

function resetPage() {
  currentPage.value = 1;
  searchText.value = "";
  statusFilter.value = "all";
}
</script>

<template>
  <div class="key-table-container">
    <!-- 工具栏 -->
    <div class="toolbar">
      <div class="toolbar-left">
        <n-button type="success" size="small" @click="createDialogShow = true">
          <template #icon>
            <n-icon :component="AddCircleOutline" />
          </template>
          添加密钥
        </n-button>
        <n-button type="error" size="small" @click="deleteDialogShow = true">
          <template #icon>
            <n-icon :component="RemoveCircleOutline" />
          </template>
          删除密钥
        </n-button>
      </div>
      <div class="toolbar-right">
        <n-space :size="12">
          <n-select
            v-model:value="statusFilter"
            :options="statusOptions"
            size="small"
            style="width: 100px"
          />
          <n-input-group>
            <n-input
              v-model:value="searchText"
              placeholder="Key 模糊查询"
              size="small"
              style="width: 180px"
              clearable
              @keyup.enter="handleSearchInput"
            />
            <n-button ghost size="small" :disabled="loading" @click="handleSearchInput">
              <n-icon :component="Search" />
            </n-button>
          </n-input-group>
          <n-dropdown :options="moreOptions" trigger="click" @select="handleMoreAction">
            <n-button size="small" secondary>
              <template #icon>
                <span style="font-size: 16px; font-weight: bold">⋯</span>
              </template>
            </n-button>
          </n-dropdown>
        </n-space>
      </div>
    </div>

    <!-- 密钥卡片网格 -->
    <div class="keys-grid-container">
      <n-spin :show="loading">
        <div v-if="keys.length === 0 && !loading" class="empty-container">
          <n-empty description="没有找到匹配的密钥" />
        </div>
        <div v-else class="keys-grid">
          <div
            v-for="key in keys"
            :key="key.id"
            class="key-card"
            :class="getStatusClass(key.status)"
          >
            <!-- 主要信息行：Key + 快速操作 -->
            <div class="key-main">
              <div class="key-section">
                <n-tag v-if="key.status === 'active'" type="success" :bordered="false" round>
                  <template #icon>
                    <n-icon :component="CheckmarkCircle" />
                  </template>
                  有效
                </n-tag>
                <n-tag v-else :bordered="false" round>
                  <template #icon>
                    <n-icon :component="AlertCircleOutline" />
                  </template>
                  无效
                </n-tag>
                <n-input
                  class="key-text"
                  :value="key.is_visible ? key.key_value : maskKey(key.key_value)"
                  readonly
                  size="small"
                />
                <div class="quick-actions">
                  <n-button size="tiny" text @click="toggleKeyVisibility(key)" title="显示/隐藏">
                    <template #icon>
                      <n-icon :component="key.is_visible ? EyeOffOutline : EyeOutline" />
                    </template>
                  </n-button>
                  <n-button size="tiny" text @click="copyKey(key)" title="复制">
                    <template #icon>
                      <n-icon :component="CopyOutline" />
                    </template>
                  </n-button>
                </div>
              </div>
            </div>

            <!-- 统计信息 + 操作按钮行 -->
            <div class="key-bottom">
              <div class="key-stats">
                <span class="stat-item">
                  请求
                  <strong>{{ key.request_count }}</strong>
                </span>
                <span class="stat-item">
                  失败
                  <strong>{{ key.failure_count }}</strong>
                </span>
                <span class="stat-item">
                  {{ key.last_used_at ? formatRelativeTime(key.last_used_at) : "未使用" }}
                </span>
                <span class="stat-item upstream-filter">
                  <n-tag
                    size="tiny"
                    :type="key.upstream_filter === 'Default' ? 'default' : 'info'"
                    :bordered="false"
                  >
                    {{
                      key.upstream_filter === "Default"
                        ? "上游ID: 默认"
                        : `上游ID: ${key.upstream_filter}`
                    }}
                  </n-tag>
                </span>
              </div>
              <n-button-group class="key-actions">
                <n-button
                  round
                  tertiary
                  type="info"
                  size="tiny"
                  @click="testKey(key)"
                  title="测试密钥"
                >
                  测试
                </n-button>
                <n-button
                  v-if="key.status !== 'active'"
                  tertiary
                  size="tiny"
                  @click="restoreKey(key)"
                  title="恢复密钥"
                  type="warning"
                >
                  恢复
                </n-button>
                <n-button
                  round
                  tertiary
                  size="tiny"
                  type="error"
                  @click="deleteKey(key)"
                  title="删除密钥"
                >
                  删除
                </n-button>
              </n-button-group>
            </div>
          </div>
        </div>
      </n-spin>
    </div>

    <!-- 分页 -->
    <div class="pagination-container">
      <div class="pagination-info">
        <span>共 {{ total }} 条记录</span>
        <n-select
          v-model:value="pageSize"
          :options="[
            { label: '12条/页', value: 12 },
            { label: '24条/页', value: 24 },
            { label: '60条/页', value: 60 },
            { label: '120条/页', value: 120 },
          ]"
          size="small"
          style="width: 100px; margin-left: 12px"
          @update:value="changePageSize"
        />
      </div>
      <div class="pagination-controls">
        <n-button size="small" :disabled="currentPage <= 1" @click="changePage(currentPage - 1)">
          上一页
        </n-button>
        <span class="page-info">第 {{ currentPage }} 页，共 {{ totalPages }} 页</span>
        <n-button
          size="small"
          :disabled="currentPage >= totalPages"
          @click="changePage(currentPage + 1)"
        >
          下一页
        </n-button>
      </div>
    </div>

    <key-create-dialog
      v-if="selectedGroup?.id"
      v-model:show="createDialogShow"
      :group-id="selectedGroup.id"
      :group-name="getGroupDisplayName(selectedGroup!)"
      @success="loadKeys"
    />

    <key-delete-dialog
      v-if="selectedGroup?.id"
      v-model:show="deleteDialogShow"
      :group-id="selectedGroup.id"
      :group-name="getGroupDisplayName(selectedGroup!)"
      @success="loadKeys"
    />
  </div>
</template>

<style scoped>
.key-table-container {
  background: white;
  border-radius: 8px;
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);
  overflow: hidden;
  height: 100%;
  display: flex;
  flex-direction: column;
}

.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  background: #f8f9fa;
  border-bottom: 1px solid #e9ecef;
  flex-shrink: 0;
}

.toolbar-left {
  display: flex;
  gap: 8px;
}

.toolbar-right {
  display: flex;
  gap: 12px;
  align-items: center;
}

.filter-group {
  display: flex;
  align-items: center;
  gap: 8px;
}

.more-actions {
  position: relative;
}

.more-menu {
  position: absolute;
  top: 100%;
  right: 0;
  background: white;
  border: 1px solid #e9ecef;
  border-radius: 6px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
  min-width: 180px;
  z-index: 1000;
  overflow: hidden;
}

.menu-item {
  display: block;
  width: 100%;
  padding: 8px 12px;
  border: none;
  background: none;
  text-align: left;
  cursor: pointer;
  font-size: 14px;
  color: #333;
  transition: background-color 0.2s;
}

.menu-item:hover {
  background: #f8f9fa;
}

.menu-item.danger {
  color: #dc3545;
}

.menu-item.danger:hover {
  background: #f8d7da;
}

.menu-divider {
  height: 1px;
  background: #e9ecef;
  margin: 4px 0;
}

.btn {
  padding: 6px 12px;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 14px;
  transition: all 0.2s;
  white-space: nowrap;
}

.btn-sm {
  padding: 4px 8px;
  font-size: 12px;
}

.btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.btn-primary {
  background: #007bff;
  color: white;
}

.btn-primary:hover:not(:disabled) {
  background: #0056b3;
}

.btn-secondary {
  background: #6c757d;
  color: white;
}

.btn-secondary:hover:not(:disabled) {
  background: #545b62;
}

.more-icon {
  font-size: 16px;
  font-weight: bold;
}

.filter-select,
.search-input,
.page-size-select {
  padding: 4px 8px;
  border: 1px solid #ced4da;
  border-radius: 4px;
  font-size: 12px;
}

.search-input {
  width: 180px;
}

.filter-select:focus,
.search-input:focus,
.page-size-select:focus {
  outline: none;
  border-color: #007bff;
  box-shadow: 0 0 0 2px rgba(0, 123, 255, 0.25);
}

/* 密钥卡片网格 */
.keys-grid-container {
  flex: 1;
  overflow-y: auto;
  padding: 16px;
}

.keys-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}

@media (max-width: 1200px) {
  .keys-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 768px) {
  .keys-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}

.key-card {
  background: white;
  border: 1px solid #e9ecef;
  border-radius: 6px;
  padding: 12px;
  transition: all 0.2s;
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
  overflow: hidden;
}

.key-card:hover {
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
}

/* 状态相关样式 */
.key-card.status-valid {
  border-color: #18a0584d;
  background: #18a0581a;
}

.key-card.status-invalid {
  border-color: #ddd;
  background: rgb(250, 250, 252);
}

.key-card.status-error {
  border-color: #ffc107;
  background: #fffdf0;
}

/* 主要信息行 */
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

/* 底部统计和按钮行 */
.key-bottom {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}

.key-stats {
  display: flex;
  gap: 6px;
  font-size: 11px;
  color: #6c757d;
  flex: 1;
  min-width: 0;
  overflow: hidden;
}

.stat-item {
  white-space: nowrap;
  flex-shrink: 1;
  overflow: hidden;
  text-overflow: ellipsis;
}

.stat-item strong {
  color: #495057;
  font-weight: 600;
}

.stat-item.upstream-filter {
  flex-shrink: 0;
  margin-left: auto;
}

.key-actions {
  flex-shrink: 0;
  &:deep(.n-button) {
    padding: 0 4px;
  }
}

.key-text {
  font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, Courier, monospace;
  font-weight: 600;
  color: #495057;
  background: #fff;
  border-radius: 4px;
  flex: 1;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
}

.quick-actions {
  display: flex;
  gap: 4px;
  flex-shrink: 0;
}

.quick-btn {
  padding: 4px 6px;
  border: none;
  background: transparent;
  cursor: pointer;
  border-radius: 3px;
  font-size: 12px;
  transition: background-color 0.2s;
}

.quick-btn:hover {
  background: #e9ecef;
}

/* 统计信息行 */

.action-btn {
  padding: 2px 6px;
  border: 1px solid #dee2e6;
  background: white;
  border-radius: 3px;
  cursor: pointer;
  font-size: 10px;
  font-weight: 500;
  transition: all 0.2s;
  white-space: nowrap;
}

.action-btn:hover {
  background: #f8f9fa;
}

.action-btn.primary {
  border-color: #007bff;
  color: #007bff;
}

.action-btn.primary:hover {
  background: #007bff;
  color: white;
}

.action-btn.secondary {
  border-color: #6c757d;
  color: #6c757d;
}

.action-btn.secondary:hover {
  background: #6c757d;
  color: white;
}

.action-btn.danger {
  border-color: #dc3545;
  color: #dc3545;
}

.action-btn.danger:hover {
  background: #dc3545;
  color: white;
}

/* 加载和空状态 */
.loading-state,
.empty-state {
  display: flex;
  justify-content: center;
  align-items: center;
  height: 200px;
  color: #6c757d;
}

.loading-spinner {
  font-size: 14px;
}

.empty-text {
  font-size: 14px;
}

/* 分页 */
.pagination-container {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  background: #f8f9fa;
  border-top: 1px solid #e9ecef;
  flex-shrink: 0;
}

.pagination-info {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 12px;
  color: #6c757d;
}

.pagination-controls {
  display: flex;
  align-items: center;
  gap: 12px;
}

.page-info {
  font-size: 12px;
  color: #6c757d;
}
</style>
