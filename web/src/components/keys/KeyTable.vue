<script setup lang="ts">
import { keysApi } from "@/api/keys";
import type { APIKey, Group, KeyStatus } from "@/types/models";
import { appState, triggerSyncOperationRefresh } from "@/utils/app-state";
import { copy } from "@/utils/clipboard";
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
  useDialog,
  type MessageReactive,
} from "naive-ui";
import { computed, h, ref, watch } from "vue";
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
const statusFilter = ref<"all" | "active" | "invalid" | "rate_limited" | "auth_failed" | "forbidden" | "bad_request" | "server_error" | "network_error">("all");
const currentPage = ref(1);
const pageSize = ref(12);
const total = ref(0);
const totalPages = ref(0);
const dialog = useDialog();
const confirmInput = ref("");

// Status text mapping
const statusTextMap = {
  all: "所有",
  active: "有效",
  invalid: "其他错误",
  rate_limited: "频率限制",
  auth_failed: "认证失败",
  forbidden: "禁止访问",
  bad_request: "请求错误",
  server_error: "内部错误",
  network_error: "网络错误"
} as const;

type StatusKey = keyof typeof statusTextMap;

// 状态过滤选项 - 扩展支持更细粒度的状态分类
const statusOptions = Object.entries(statusTextMap).map(([value, label]) => ({
  label,
  value
}));

// 更多操作下拉菜单选项 - 静态定义避免重复计算
const moreOptions = [
  {
    label: "导出",
    key: "export",
    children: [
      { label: "导出所有密钥", key: "copyAll" },
      { label: "导出有效密钥", key: "copyActive" },
      { label: "导出其他错误密钥", key: "copyInvalid" },
      { label: "导出频率限制密钥", key: "copyRateLimited" },
      { label: "导出认证失败密钥", key: "copyAuthFailed" },
      { label: "导出禁止访问密钥", key: "copyForbidden" },
      { label: "导出请求错误密钥", key: "copyBadRequest" },
      { label: "导出内部错误密钥", key: "copyServerError" },
      { label: "导出网络错误密钥", key: "copyNetworkError" },
    ],
  },
  {
    label: "恢复",
    key: "restore",
    children: [
      { label: "恢复其他错误密钥", key: "restoreInvalid" },
      { label: "恢复频率限制密钥", key: "restoreRateLimited" },
      { label: "恢复认证失败密钥", key: "restoreAuthFailed" },
      { label: "恢复禁止访问密钥", key: "restoreForbidden" },
      { label: "恢复请求错误密钥", key: "restoreBadRequest" },
      { label: "恢复内部错误密钥", key: "restoreServerError" },
      { label: "恢复网络错误密钥", key: "restoreNetworkError" },
    ],
  },
  {
    label: "清空",
    key: "clear",
    children: [
      { label: "清空其他错误密钥", key: "clearInvalid", props: { style: { color: '#d03050' } } },
      { label: "清空频率限制密钥", key: "clearRateLimited", props: { style: { color: '#d03050' } } },
      { label: "清空认证失败密钥", key: "clearAuthFailed", props: { style: { color: '#d03050' } } },
      { label: "清空禁止访问密钥", key: "clearForbidden", props: { style: { color: '#d03050' } } },
      { label: "清空请求错误密钥", key: "clearBadRequest", props: { style: { color: '#d03050' } } },
      { label: "清空内部错误密钥", key: "clearServerError", props: { style: { color: '#d03050' } } },
      { label: "清空网络错误密钥", key: "clearNetworkError", props: { style: { color: '#d03050' } } },
      {
        label: "清空所有密钥",
        key: "clearAll",
        props: { style: { color: "red", fontWeight: "bold" } },
      },
    ],
  },
  {
    label: "验证",
    key: "validate",
    children: [
      { label: "验证所有密钥", key: "validateAll" },
      { label: "验证有效密钥", key: "validateActive" },
      { label: "验证其他错误密钥", key: "validateInvalid" },
      { label: "验证频率限制密钥", key: "validateRateLimited" },
      { label: "验证认证失败密钥", key: "validateAuthFailed" },
      { label: "验证禁止访问密钥", key: "validateForbidden" },
      { label: "验证请求错误密钥", key: "validateBadRequest" },
      { label: "验证内部错误密钥", key: "validateServerError" },
      { label: "验证网络错误密钥", key: "validateNetworkError" },
    ],
  },
];

let testingMsg: MessageReactive | null = null;
const isDeling = ref(false);
const isRestoring = ref(false);

const createDialogShow = ref(false);
const deleteDialogShow = ref(false);

// Computed properties for better performance
const hasKeys = computed(() => keys.value.length > 0);
const isLoading = computed(() => loading.value);
const canLoadKeys = computed(() => props.selectedGroup?.id);
const paginationInfo = computed(() => ({
  current: currentPage.value,
  total: totalPages.value,
  hasNext: currentPage.value < totalPages.value,
  hasPrev: currentPage.value > 1
}));

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

watch([currentPage, pageSize], async () => {
  await loadKeys();
});

watch(statusFilter, async () => {
  if (currentPage.value !== 1) {
    currentPage.value = 1;
  } else {
    await loadKeys();
  }
});

// 监听任务完成事件，自动刷新密钥列表
// 优化监听器，减少不必要的检查
const refreshTaskTypes = new Set(["KEY_VALIDATION", "KEY_IMPORT", "KEY_DELETE"]);

watch(
  () => appState.groupDataRefreshTrigger,
  () => {
    const { lastCompletedTask } = appState;
    if (!lastCompletedTask || !props.selectedGroup) return;

    const isCurrentGroup = lastCompletedTask.groupName === props.selectedGroup.name;
    const shouldRefresh = refreshTaskTypes.has(lastCompletedTask.taskType);

    if (isCurrentGroup && shouldRefresh) {
      loadKeys();
    }
  }
);

// 处理搜索输入的防抖
let searchTimeout: number | null = null;
function handleSearchInput() {
  if (searchTimeout) {
    clearTimeout(searchTimeout);
  }

  searchTimeout = setTimeout(() => {
    if (currentPage.value !== 1) {
      currentPage.value = 1;
    } else {
      loadKeys();
    }
  }, 300);
}

// 处理更多操作菜单 - 使用Map提升查找性能
const actionMap = new Map([
  // Copy actions
  ["copyAll", () => copyKeys("all")],
  ["copyActive", () => copyKeys("active")],
  ["copyInvalid", () => copyKeys("invalid")],
  ["copyRateLimited", () => copyKeys("rate_limited")],
  ["copyAuthFailed", () => copyKeys("auth_failed")],
  ["copyForbidden", () => copyKeys("forbidden")],
  ["copyBadRequest", () => copyKeys("bad_request")],
  ["copyServerError", () => copyKeys("server_error")],
  ["copyNetworkError", () => copyKeys("network_error")],

  // Restore actions
  ["restoreAll", () => restoreKeys("invalid", "RESTORE_ALL_INVALID", () => {
    if (!props.selectedGroup?.id) throw new Error("No group selected");
    return keysApi.restoreAllInvalidKeys(props.selectedGroup.id);
  })],
  ["restoreActive", () => restoreKeys("active", "RESTORE_ACTIVE")],
  ["restoreInvalid", () => restoreKeys("invalid", "RESTORE_INVALID")],
  ["restoreRateLimited", () => restoreKeys("rate_limited", "RESTORE_RATE_LIMITED")],
  ["restoreAuthFailed", () => restoreKeys("auth_failed", "RESTORE_AUTH_FAILED")],
  ["restoreForbidden", () => restoreKeys("forbidden", "RESTORE_FORBIDDEN")],
  ["restoreBadRequest", () => restoreKeys("bad_request", "RESTORE_BAD_REQUEST")],
  ["restoreServerError", () => restoreKeys("server_error", "RESTORE_SERVER_ERROR")],
  ["restoreNetworkError", () => restoreKeys("network_error", "RESTORE_NETWORK_ERROR")],

  // Clear actions
  ["clearAll", clearAll],
  ["clearActive", () => clearKeys("active", "CLEAR_ACTIVE")],
  ["clearInvalid", () => clearKeys("invalid", "CLEAR_ALL_INVALID", () => {
    if (!props.selectedGroup?.id) throw new Error("No group selected");
    return keysApi.clearAllInvalidKeys(props.selectedGroup.id);
  })],
  ["clearRateLimited", () => clearKeys("rate_limited", "CLEAR_RATE_LIMITED")],
  ["clearAuthFailed", () => clearKeys("auth_failed", "CLEAR_AUTH_FAILED")],
  ["clearForbidden", () => clearKeys("forbidden", "CLEAR_FORBIDDEN")],
  ["clearBadRequest", () => clearKeys("bad_request", "CLEAR_BAD_REQUEST")],
  ["clearServerError", () => clearKeys("server_error", "CLEAR_SERVER_ERROR")],
  ["clearNetworkError", () => clearKeys("network_error", "CLEAR_NETWORK_ERROR")],

  // Validate actions
  ["validateAll", () => validateKeys("all")],
  ["validateActive", () => validateKeys("active")],
  ["validateInvalid", () => validateKeys("invalid")],
  ["validateRateLimited", () => validateKeys("rate_limited")],
  ["validateAuthFailed", () => validateKeys("auth_failed")],
  ["validateForbidden", () => validateKeys("forbidden")],
  ["validateBadRequest", () => validateKeys("bad_request")],
  ["validateServerError", () => validateKeys("server_error")],
  ["validateNetworkError", () => validateKeys("network_error")],
]);

function handleMoreAction(key: string) {
  const action = actionMap.get(key);
  if (action) {
    action();
  } else {
    console.warn(`Unhandled action: ${key}`);
  }
}

// 缓存API请求避免重复调用（带请求签名，避免串音/过期覆盖）
let currentRequest: Promise<any> | null = null;
let currentRequestKey: string | null = null;

// 请求参数类型定义
interface RequestParams {
  group_id: number;
  page: number;
  page_size: number;
  status: KeyStatus | undefined;
  key_value: string | undefined;
}

// 生成请求签名的辅助函数
function generateRequestKey(params: RequestParams): string {
  return `${params.group_id}_${params.page}_${params.page_size}_${params.status}_${params.key_value}`;
}

async function loadKeys() {
  if (!props.selectedGroup?.id) {
    return;
  }

  const requestParams: RequestParams = {
    group_id: props.selectedGroup.id,
    page: currentPage.value,
    page_size: pageSize.value,
    status: statusFilter.value === "all" ? undefined : (statusFilter.value as KeyStatus),
    key_value: searchText.value.trim() || undefined,
  };

  const reqKey = generateRequestKey(requestParams);

  // 若已有同签名请求在飞，直接复用
  if (currentRequest && currentRequestKey === reqKey) {
    return currentRequest;
  }

  try {
    loading.value = true;
    currentRequestKey = reqKey;
    const thisRequest = keysApi.getGroupKeys(requestParams);
    currentRequest = thisRequest;
    const result = await thisRequest;

    // 仅当本次返回仍是最新签名时才落盘，避免旧响应覆盖新状态
    if (currentRequestKey !== reqKey) {
      console.debug('Discarding stale response for request:', reqKey);
      return;
    }

    // 添加is_visible属性到每个key
    keys.value = result.items.map((key: APIKey) => ({
      ...key,
      is_visible: false
    }));
    total.value = result.pagination.total_items;
    totalPages.value = result.pagination.total_pages;
  } catch (_error) {
    window.$message.error("加载密钥失败");
  } finally {
    loading.value = false;
    // 只在当前签名仍一致时清理指针
    if (currentRequestKey === reqKey) {
      currentRequest = null;
      currentRequestKey = null;
    }
  }
}

// 处理批量删除成功后的刷新
async function handleBatchDeleteSuccess() {
  await loadKeys();
  // 触发同步操作刷新
  if (props.selectedGroup) {
    triggerSyncOperationRefresh(props.selectedGroup.name, "BATCH_DELETE");
  }
}

// Key action handlers - optimized and organized
const keyActions = {
  async copy(key: KeyRow) {
    const success = await copy(key.key_value);
    window.$message[success ? 'success' : 'error'](
      success ? "密钥已复制到剪贴板" : "复制失败"
    );
  },

  toggle(key: KeyRow) {
    key.is_visible = !key.is_visible;
  },

  async test(key: KeyRow) {
    if (!canLoadKeys.value || !key.key_value || testingMsg) return;

    testingMsg = window.$message.info("正在测试密钥...", { duration: 0 });

    try {
      const response = await keysApi.testKeys(props.selectedGroup?.id!, key.key_value);
      const result = response.results?.[0] || {};

      if (result.is_valid) {
        window.$message.success(`密钥测试成功 (耗时: ${formatDuration(response.total_duration)})`);
      } else {
        const errorMsg = result.error || "密钥测试失败: 无效的API密钥";
        window.$message.error(errorMsg, {
          keepAliveOnHover: true,
          duration: 5000,
          closable: true,
        });
        console.debug("测试失败 - 错误详情:", errorMsg);
      }

      await loadKeys();
      triggerSyncOperationRefresh(props.selectedGroup?.name!, "TEST_SINGLE");

      // 延迟刷新确保状态更新
      setTimeout(loadKeys, 500);
    } catch (error) {
      console.error("测试失败", error);
    } finally {
      testingMsg?.destroy();
      testingMsg = null;
    }
  },

  async restore(key: KeyRow) {
    if (!canLoadKeys.value || !key.key_value || isRestoring.value) return;

    const d = dialog.warning({
      title: "恢复密钥",
      content: `确定要恢复密钥"${maskKey(key.key_value)}"吗？`,
      positiveText: "确定",
      negativeText: "取消",
      onPositiveClick: async () => {
        if (!canLoadKeys.value) return;

        isRestoring.value = true;
        d.loading = true;

        try {
          await keysApi.restoreKeys(props.selectedGroup?.id!, key.key_value);
          await loadKeys();
          triggerSyncOperationRefresh(props.selectedGroup?.name!, "RESTORE_SINGLE");
        } catch (error) {
          console.error("恢复失败", error);
        } finally {
          d.loading = false;
          isRestoring.value = false;
        }
      },
    });
  },

  async delete(key: KeyRow) {
    if (!canLoadKeys.value || !key.key_value || isDeling.value) return;

    const d = dialog.warning({
      title: "删除密钥",
      content: `确定要删除密钥"${maskKey(key.key_value)}"吗？`,
      positiveText: "确定",
      negativeText: "取消",
      onPositiveClick: async () => {
        if (!canLoadKeys.value) return;

        d.loading = true;
        isDeling.value = true;

        try {
          await keysApi.deleteKeys(props.selectedGroup?.id!, key.key_value);
          await loadKeys();
          triggerSyncOperationRefresh(props.selectedGroup?.name!, "DELETE_SINGLE");
        } catch (error) {
          console.error("删除失败", error);
        } finally {
          d.loading = false;
          isDeling.value = false;
        }
      },
    });
  }
};

// Utility functions
const formatRelativeTime = (date: string): string => {
  if (!date) return "从未";

  const now = Date.now();
  const target = new Date(date).getTime();
  const diffSeconds = Math.floor((now - target) / 1000);

  if (diffSeconds < 60) return diffSeconds > 0 ? `${diffSeconds}秒前` : "刚刚";

  const diffMinutes = Math.floor(diffSeconds / 60);
  if (diffMinutes < 60) return `${diffMinutes}分钟前`;

  const diffHours = Math.floor(diffMinutes / 60);
  if (diffHours < 24) return `${diffHours}小时前`;

  const diffDays = Math.floor(diffHours / 24);
  return `${diffDays}天前`;
};

const formatDuration = (ms: number): string => {
  if (ms < 0) return "0ms";

  const minutes = Math.floor(ms / 60000);
  const seconds = Math.floor((ms % 60000) / 1000);
  const milliseconds = ms % 1000;

  let result = "";
  if (minutes > 0) result += `${minutes}m`;
  if (seconds > 0) result += `${seconds}s`;
  if (milliseconds > 0 || result === "") result += `${milliseconds}ms`;

  return result;
};

// Status configuration for consistent styling and display - 使用Map提升查找性能
const statusConfig = new Map([
  ["active", {
    class: "status-valid",
    tag: { type: "success" as const, icon: CheckmarkCircle, text: "有效" }
  }],
  ["rate_limited", {
    class: "status-rate-limited",
    tag: { type: "warning" as const, icon: AlertCircleOutline, text: "频率限制" }
  }],
  ["auth_failed", {
    class: "status-auth-failed",
    tag: { type: "error" as const, icon: AlertCircleOutline, text: "认证失败" }
  }],
  ["forbidden", {
    class: "status-forbidden",
    tag: { type: "error" as const, icon: RemoveCircleOutline, text: "禁止访问" }
  }],
  ["bad_request", {
    class: "status-bad-request",
    tag: { type: "warning" as const, icon: AlertCircleOutline, text: "请求错误" }
  }],
  ["server_error", {
    class: "status-server-error",
    tag: { type: "error" as const, icon: RemoveCircleOutline, text: "内部错误" }
  }],
  ["network_error", {
    class: "status-network-error",
    tag: { type: "default" as const, icon: AlertCircleOutline, text: "网络错误" }
  }],
  ["invalid", {
    class: "status-invalid",
    tag: { type: "default" as const, icon: AlertCircleOutline, text: "其他错误" }
  }]
]);

const defaultStatusConfig = {
  class: "status-unknown",
  tag: { type: "default" as const, icon: AlertCircleOutline, text: "未知" }
};

function getStatusClass(status: KeyStatus): string {
  if (!status) return defaultStatusConfig.class;
  return statusConfig.get(status)?.class || defaultStatusConfig.class;
}

function getStatusTag(status: KeyStatus) {
  if (!status) return defaultStatusConfig.tag;
  return statusConfig.get(status)?.tag || defaultStatusConfig.tag;
}

// Unified copy function
function copyKeys(status: StatusKey) {
  if (!props.selectedGroup?.id) {
    return;
  }
  keysApi.exportKeys(props.selectedGroup.id, status as any);
}

// Unified restore function
async function restoreKeys(status: StatusKey, syncAction: string, apiCall?: () => Promise<any>) {
  if (!props.selectedGroup?.id || isRestoring.value) {
    return;
  }

  const statusText = statusTextMap[status];
  const d = dialog.warning({
    title: "恢复密钥",
    content: `确定要恢复${status === "all" ? "其他" : "所有"}${statusText}密钥吗？`,
    positiveText: "确定",
    negativeText: "取消",
    onPositiveClick: async () => {
      if (!props.selectedGroup?.id) {
        return;
      }

      isRestoring.value = true;
      d.loading = true;
      try {
        if (apiCall) {
          await apiCall();
        } else {
          await keysApi.restoreKeysByStatus(props.selectedGroup.id, status as any);
        }
        await loadKeys();
        triggerSyncOperationRefresh(props.selectedGroup.name, syncAction);
      } catch (_error) {
        console.error("恢复失败");
      } finally {
        d.loading = false;
        isRestoring.value = false;
      }
    },
  });
}

async function validateKeys(status: StatusKey) {
  if (!props.selectedGroup?.id || testingMsg) {
    return;
  }

  const statusText = statusTextMap[status];
  testingMsg = window.$message.info(`正在验证${statusText}密钥...`, {
    duration: 0,
  });

  try {
    await keysApi.validateGroupKeys(props.selectedGroup.id, status === "all" ? undefined : status);
    localStorage.removeItem("last_closed_task");
    appState.taskPollingTrigger++;
  } catch (_error) {
    console.error("测试失败");
  } finally {
    testingMsg?.destroy();
    testingMsg = null;
  }
}

// Unified clear function
async function clearKeys(status: StatusKey, syncAction: string, apiCall?: () => Promise<any>) {
  if (!props.selectedGroup?.id || isDeling.value) {
    return;
  }

  const statusText = statusTextMap[status];
  const d = dialog.warning({
    title: "清除密钥",
    content: `确定要清除${status === "all" ? "" : "所有"}${statusText}密钥吗？此操作不可恢复！`,
    positiveText: "确定",
    negativeText: "取消",
    onPositiveClick: async () => {
      if (!props.selectedGroup?.id) {
        return;
      }

      isDeling.value = true;
      d.loading = true;
      try {
        let result;
        if (apiCall) {
          result = await apiCall();
        } else {
          await keysApi.clearKeysByStatus(props.selectedGroup.id, status as any);
        }

        const message = result?.data?.message || "清除成功";
        window.$message.success(message);
        await loadKeys();
        triggerSyncOperationRefresh(props.selectedGroup.name, syncAction);
      } catch (_error) {
        console.error("删除失败");
      } finally {
        d.loading = false;
        isDeling.value = false;
      }
    },
  });
}

async function clearAll() {
  if (!props.selectedGroup?.id || isDeling.value) {
    return;
  }

  dialog.warning({
    title: "清空所有密钥",
    content: "此操作将永久删除该分组下的所有密钥，且不可恢复！确定要继续吗？",
    positiveText: "确定",
    negativeText: "取消",
    onPositiveClick: () => {
      confirmInput.value = ""; // Reset before opening second dialog
      dialog.create({
        title: "请输入分组名称以确认",
        content: () =>
          h("div", null, [
            h("p", null, [
              "这是一个非常危险的操作，将删除此分组下的",
              h("strong", null, "所有"),
              "密钥。为防止误操作，请输入分组名称 ",
              h("strong", { style: { color: "#d03050" } }, props.selectedGroup?.name),
              " 以确认。",
            ]),
            h(NInput, {
              value: confirmInput.value,
              "onUpdate:value": v => {
                confirmInput.value = v;
              },
              placeholder: "请输入分组名称",
            }),
          ]),
        positiveText: "确认清空",
        negativeText: "取消",
        onPositiveClick: async () => {
          if (confirmInput.value !== props.selectedGroup?.name) {
            window.$message.error("分组名称输入不正确");
            return false; // Prevent dialog from closing
          }

          if (!props.selectedGroup?.id) {
            return;
          }

          isDeling.value = true;
          try {
            await keysApi.clearAllKeys(props.selectedGroup.id);
            window.$message.success("已成功清空所有密钥");
            await loadKeys();
            // Trigger sync operation refresh
            triggerSyncOperationRefresh(props.selectedGroup.name, "CLEAR_ALL");
          } catch (_error) {
            console.error("清空失败", _error);
          } finally {
            isDeling.value = false;
          }
        },
      });
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
        <n-space :size="12" align="center">
          <n-select v-model:value="statusFilter" :options="statusOptions" size="small" style="width: 100px" placeholder="全部状态" />
          <n-input-group>
            <n-input v-model:value="searchText" placeholder="Key 精确匹配" size="small" style="width: 180px" clearable
              @keyup.enter="handleSearchInput" />
            <n-button ghost size="small" :disabled="loading" @click="handleSearchInput">
              <n-icon :component="Search" />
            </n-button>
          </n-input-group>
          <n-dropdown :options="moreOptions" trigger="click" @select="handleMoreAction" :show-arrow="false">
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
      <n-spin :show="isLoading">
        <div v-if="!hasKeys && !isLoading" class="empty-container">
          <n-empty description="没有找到匹配的密钥" />
        </div>
        <div v-else class="keys-grid">
          <div v-for="key in keys" :key="key.id" class="key-card" :class="getStatusClass(key.status)">
            <!-- 主要信息行：Key + 快速操作 -->
            <div class="key-main">
              <div class="key-section">
                <n-tag :type="getStatusTag(key.status).type" :bordered="false" round>
                  <template #icon>
                    <n-icon :component="getStatusTag(key.status).icon" />
                  </template>
                  {{ getStatusTag(key.status).text }}
                </n-tag>
                <n-input class="key-text" :value="key.is_visible ? key.key_value : maskKey(key.key_value)" readonly
                  size="small" />
                <div class="quick-actions">
                  <n-button size="tiny" text @click="keyActions.toggle(key)" title="显示/隐藏">
                    <template #icon>
                      <n-icon :component="key.is_visible ? EyeOffOutline : EyeOutline" />
                    </template>
                  </n-button>
                  <n-button size="tiny" text @click="keyActions.copy(key)" title="复制">
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
              </div>
              <n-button-group class="key-actions">
                <n-button round tertiary type="info" size="tiny" @click="keyActions.test(key)" title="测试密钥">
                  测试
                </n-button>
                <n-button v-if="key.status !== 'active'" tertiary size="tiny" @click="keyActions.restore(key)"
                  title="恢复密钥" type="warning">
                  恢复
                </n-button>
                <n-button round tertiary size="tiny" type="error" @click="keyActions.delete(key)" title="删除密钥">
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
        <n-select v-model:value="pageSize" :options="[
          { label: '12条/页', value: 12 },
          { label: '24条/页', value: 24 },
          { label: '60条/页', value: 60 },
          { label: '120条/页', value: 120 },
        ]" size="small" style="width: 100px; margin-left: 12px" @update:value="changePageSize" />
      </div>
      <div class="pagination-controls">
        <n-button size="small" :disabled="!paginationInfo.hasPrev" @click="changePage(currentPage - 1)">
          上一页
        </n-button>
        <span class="page-info">第 {{ paginationInfo.current }} 页，共 {{ paginationInfo.total }} 页</span>
        <n-button size="small" :disabled="!paginationInfo.hasNext" @click="changePage(currentPage + 1)">
          下一页
        </n-button>
      </div>
    </div>

    <key-create-dialog v-if="selectedGroup?.id" v-model:show="createDialogShow" :group-id="selectedGroup.id"
      :group-name="getGroupDisplayName(selectedGroup!)" @success="loadKeys" />

    <key-delete-dialog v-if="selectedGroup?.id" v-model:show="deleteDialogShow" :group-id="selectedGroup.id"
      :group-name="getGroupDisplayName(selectedGroup!)" @success="handleBatchDeleteSuccess" />
  </div>
</template>

<style scoped>
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

/* 确保按钮在暗黑模式下有正确的对比度 */
.toolbar :deep(.n-button) {
  font-weight: 500;
}

/* 搜索输入框样式 */
.toolbar :deep(.n-input-group) {
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.05);
}

/* 暗黑模式下的输入框 */
:root.dark .toolbar :deep(.n-input-group) {
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.2);
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
  background: var(--card-bg-solid);
  border: 1px solid var(--border-color);
  border-radius: 6px;
  box-shadow: var(--shadow-lg);
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
  gap: 10px;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.05);
}

.key-card:hover {
  box-shadow: var(--shadow-md);
  transform: translateY(-1px);
}

/* 状态相关样式 */
.key-card.status-valid {
  border-color: var(--success-border);
  background: var(--success-bg);
  border-width: 1.5px;
}

.key-card.status-rate-limited {
  border-color: #fca9034d;
  background: #fca9031a;
}

.key-card.status-auth-failed {
  border-color: #d030504d;
  background: #d030501a;
}

.key-card.status-forbidden {
  border-color: #d030504d;
  background: #d030501a;
}

.key-card.status-bad-request {
  border-color: #fca9034d;
  background: #fca9031a;
}

.key-card.status-server-error {
  border-color: #d03f504d;
  background: #d03f501a;
}

.key-card.status-network-error {
  border-color: #6666664d;
  background: #6666661a;
}

.key-card.status-invalid {
  border-color: var(--invalid-border);
  background: var(--card-bg-solid);
  opacity: 0.85;
}

.key-card.status-error {
  border-color: var(--error-border);
  background: var(--error-bg);
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

  &:deep(.n-button) {
    padding: 0 4px;
  }
}

.key-text {
  font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, Courier, monospace;
  font-weight: 500;
  flex: 1;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
}

/* 浅色主题 */
:root:not(.dark) .key-text {
  color: #495057;
  background: #f8f9fa;
}

/* 暗黑主题 */
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

.quick-btn {
  padding: 4px 6px;
  border: none;
  background: transparent;
  cursor: pointer;
  border-radius: 3px;
  font-size: 12px;
  transition: background-color 0.2s;
}

/* 浅色主题 */
:root:not(.dark) .quick-btn:hover {
  background: #e9ecef;
}

/* 暗黑主题 */
:root.dark .quick-btn:hover {
  background: var(--bg-tertiary);
}

/* 统计信息行 */

.action-btn {
  padding: 2px 6px;
  border: 1px solid var(--border-color);
  background: var(--card-bg-solid);
  border-radius: 3px;
  cursor: pointer;
  font-size: 10px;
  font-weight: 500;
  transition: all 0.2s;
  white-space: nowrap;
  color: var(--text-primary);
}

.action-btn:hover {
  background: var(--bg-secondary);
}

.action-btn.primary {
  border-color: var(--primary-color);
  color: var(--primary-color);
}

.action-btn.primary:hover {
  background: var(--primary-color);
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

  .toolbar-right .n-space {
    width: 100%;
    justify-content: space-between;
  }
}
</style>
