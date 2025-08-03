<script setup lang="ts">
import { logApi } from "@/api/logs";
import type { LogFilter, RequestLog } from "@/types/models";
import { maskKey } from "@/utils/display";
import { DownloadOutline, EyeOffOutline, EyeOutline, Search } from "@vicons/ionicons5";
import {
  NButton,
  NDataTable,
  NDatePicker,
  NEllipsis,
  NIcon,
  NInput,
  NSelect,
  NSpace,
  NSpin,
  NTag,
} from "naive-ui";
import { computed, h, onMounted, reactive, ref, watch } from "vue";

interface LogRow extends RequestLog {
  is_key_visible: boolean;
}

// Data
const loading = ref(false);
const logs = ref<LogRow[]>([]);
const currentPage = ref(1);
const pageSize = ref(15);
const total = ref(0);
const totalPages = computed(() => Math.ceil(total.value / pageSize.value));

// Filters
const filters = reactive({
  group_name: "",
  key_value: "",
  model: "",
  is_success: "" as "true" | "false" | "",
  status_code: "",
  source_ip: "",
  error_contains: "",
  start_time: null as number | null,
  end_time: null as number | null,
});

const successOptions = [
  { label: "状态", value: "" },
  { label: "成功", value: "true" },
  { label: "失败", value: "false" },
];

// Fetch data
const loadLogs = async () => {
  loading.value = true;
  try {
    const params: LogFilter = {
      page: currentPage.value,
      page_size: pageSize.value,
      group_name: filters.group_name || undefined,
      key_value: filters.key_value || undefined,
      model: filters.model || undefined,
      is_success: filters.is_success === "" ? undefined : filters.is_success === "true",
      status_code: filters.status_code ? parseInt(filters.status_code, 10) : undefined,
      source_ip: filters.source_ip || undefined,
      error_contains: filters.error_contains || undefined,
      start_time: filters.start_time ? new Date(filters.start_time).toISOString() : undefined,
      end_time: filters.end_time ? new Date(filters.end_time).toISOString() : undefined,
    };

    const res = await logApi.getLogs(params);
    if (res.code === 0 && res.data) {
      logs.value = res.data.items.map(log => ({ ...log, is_key_visible: false }));
      total.value = res.data.pagination.total_items;
    } else {
      logs.value = [];
      total.value = 0;
      window.$message.error(res.message || "加载日志失败", {
        keepAliveOnHover: true,
        duration: 5000,
        closable: true,
      });
    }
  } catch (_error) {
    window.$message.error("加载日志请求失败");
  } finally {
    loading.value = false;
  }
};

const formatDateTime = (timestamp: string) => {
  if (!timestamp) {
    return "-";
  }
  const date = new Date(timestamp);
  return date.toLocaleString("zh-CN", { hour12: false }).replace(/\//g, "-");
};

const toggleKeyVisibility = (row: LogRow) => {
  row.is_key_visible = !row.is_key_visible;
};

// Columns definition
const createColumns = () => [
  {
    title: "时间",
    key: "timestamp",
    width: 160,
    render: (row: LogRow) => formatDateTime(row.timestamp),
  },
  {
    title: "状态",
    key: "is_success",
    width: 50,
    render: (row: LogRow) =>
      h(
        NTag,
        { type: row.is_success ? "success" : "error", size: "small", round: true },
        { default: () => (row.is_success ? "成功" : "失败") }
      ),
  },
  {
    title: "类型",
    key: "is_stream",
    width: 50,
    render: (row: LogRow) =>
      h(
        NTag,
        { type: row.is_stream ? "info" : "default", size: "small", round: true },
        { default: () => (row.is_stream ? "流式" : "非流") }
      ),
  },
  { title: "状态码", key: "status_code", width: 60 },
  { title: "耗时(ms)", key: "duration_ms", width: 80 },
  { title: "重试", key: "retries", width: 50 },
  { title: "分组", key: "group_name", width: 120 },
  { title: "模型", key: "model", width: 300 },
  {
    title: "Key",
    key: "key_value",
    width: 200,
    render: (row: LogRow) =>
      h(NSpace, { align: "center", wrap: false }, () => [
        h(
          NEllipsis,
          { style: "max-width: 150px" },
          { default: () => (row.is_key_visible ? row.key_value : maskKey(row.key_value || "")) }
        ),
        h(
          NButton,
          { size: "tiny", text: true, onClick: () => toggleKeyVisibility(row) },
          {
            icon: () =>
              h(NIcon, null, { default: () => h(row.is_key_visible ? EyeOffOutline : EyeOutline) }),
          }
        ),
      ]),
  },
  {
    title: "请求路径",
    key: "request_path",
    width: 220,
    render: (row: LogRow) =>
      h(NEllipsis, { style: "max-width: 200px" }, { default: () => row.request_path }),
  },
  {
    title: "上游地址",
    key: "upstream_addr",
    width: 220,
    render: (row: LogRow) =>
      h(NEllipsis, { style: "max-width: 200px" }, { default: () => row.upstream_addr }),
  },
  { title: "源IP", key: "source_ip", width: 140 },
  {
    title: "错误信息",
    width: 270,
    key: "error_message",
    render: (row: LogRow) =>
      h(NEllipsis, { style: "max-width: 250px" }, { default: () => row.error_message || "-" }),
  },
  {
    title: "User Agent",
    key: "user_agent",
    width: 220,
    render: (row: LogRow) =>
      h(NEllipsis, { style: "max-width: 200px" }, { default: () => row.user_agent }),
  },
];

const columns = createColumns();

// Lifecycle and Watchers
onMounted(loadLogs);
watch([currentPage, pageSize], loadLogs);

const handleSearch = () => {
  currentPage.value = 1;
  loadLogs();
};

const resetFilters = () => {
  filters.group_name = "";
  filters.key_value = "";
  filters.model = "";
  filters.is_success = "";
  filters.status_code = "";
  filters.source_ip = "";
  filters.error_contains = "";
  filters.start_time = null;
  filters.end_time = null;
  handleSearch();
};

const exportLogs = () => {
  const params: Omit<LogFilter, "page" | "page_size"> = {
    group_name: filters.group_name || undefined,
    key_value: filters.key_value || undefined,
    model: filters.model || undefined,
    is_success: filters.is_success === "" ? undefined : filters.is_success === "true",
    status_code: filters.status_code ? parseInt(filters.status_code, 10) : undefined,
    source_ip: filters.source_ip || undefined,
    error_contains: filters.error_contains || undefined,
    start_time: filters.start_time ? new Date(filters.start_time).toISOString() : undefined,
    end_time: filters.end_time ? new Date(filters.end_time).toISOString() : undefined,
  };
  logApi.exportLogs(params);
};

function changePage(page: number) {
  currentPage.value = page;
}

function changePageSize(size: number) {
  pageSize.value = size;
  currentPage.value = 1;
}
</script>

<template>
  <div class="log-table-container">
    <n-space vertical>
      <!-- 工具栏 -->
      <div class="toolbar">
        <div class="filter-section">
          <div class="filter-row">
            <div class="filter-grid">
              <div class="filter-item">
                <n-select
                  v-model:value="filters.is_success"
                  :options="successOptions"
                  size="small"
                  @update:value="handleSearch"
                />
              </div>
              <div class="filter-item">
                <n-input
                  v-model:value="filters.status_code"
                  placeholder="状态码"
                  size="small"
                  clearable
                  @keyup.enter="handleSearch"
                />
              </div>
              <div class="filter-item">
                <n-input
                  v-model:value="filters.group_name"
                  placeholder="分组名"
                  size="small"
                  clearable
                  @keyup.enter="handleSearch"
                />
              </div>
              <div class="filter-item">
                <n-input
                  v-model:value="filters.model"
                  placeholder="模型"
                  size="small"
                  clearable
                  @keyup.enter="handleSearch"
                />
              </div>
              <div class="filter-item">
                <n-input
                  v-model:value="filters.key_value"
                  placeholder="密钥"
                  size="small"
                  clearable
                  @keyup.enter="handleSearch"
                />
              </div>
              <div class="filter-item">
                <n-date-picker
                  v-model:value="filters.start_time"
                  type="datetime"
                  clearable
                  size="small"
                  placeholder="开始时间"
                />
              </div>
              <div class="filter-item">
                <n-date-picker
                  v-model:value="filters.end_time"
                  type="datetime"
                  clearable
                  size="small"
                  placeholder="结束时间"
                />
              </div>
              <div class="filter-item">
                <n-input
                  v-model:value="filters.error_contains"
                  placeholder="错误信息"
                  size="small"
                  clearable
                  @keyup.enter="handleSearch"
                />
              </div>
              <div class="filter-actions">
                <n-button ghost size="small" :disabled="loading" @click="handleSearch">
                  <template #icon>
                    <n-icon :component="Search" />
                  </template>
                  搜索
                </n-button>
                <n-button size="small" @click="resetFilters">重置</n-button>
                <n-button size="small" type="primary" ghost @click="exportLogs">
                  <template #icon>
                    <n-icon :component="DownloadOutline" />
                  </template>
                  导出密钥
                </n-button>
              </div>
            </div>
          </div>
        </div>
      </div>
      <div class="table-main">
        <!-- 表格 -->
        <div class="table-container">
          <n-spin :show="loading">
            <n-data-table
              :columns="columns"
              :data="logs"
              :bordered="false"
              remote
              size="small"
              :scroll-x="1840"
            />
          </n-spin>
        </div>

        <!-- 分页 -->
        <div class="pagination-container">
          <div class="pagination-info">
            <span>共 {{ total }} 条记录</span>
            <n-select
              v-model:value="pageSize"
              :options="[
                { label: '15条/页', value: 15 },
                { label: '30条/页', value: 30 },
                { label: '50条/页', value: 50 },
                { label: '100条/页', value: 100 },
              ]"
              size="small"
              style="width: 100px; margin-left: 12px"
              @update:value="changePageSize"
            />
          </div>
          <div class="pagination-controls">
            <n-button
              size="small"
              :disabled="currentPage <= 1"
              @click="changePage(currentPage - 1)"
            >
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
      </div>
    </n-space>
  </div>
</template>

<style scoped>
.log-table-container {
  /* background: white; */
  /* border-radius: 8px; */
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.05);
  display: flex;
  flex-direction: column;
  /* height: 100%; */
}
.toolbar {
  background: white;
  border-radius: 8px;
  padding: 16px;
  border-bottom: 1px solid #f0f0f0;
}

.filter-section {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.filter-row {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end; /* Aligns buttons with the bottom of the filter items */
  gap: 16px;
}

.filter-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  flex: 1 1 auto; /* Let it take available space and wrap */
}

.filter-item {
  flex: 1 1 180px; /* Each item will have a base width of 180px and can grow */
  min-width: 180px; /* Prevent from becoming too narrow */
}

.filter-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

@media (max-width: 768px) {
  .pagination-container {
    flex-direction: column;
    gap: 12px;
  }
}

@media (max-width: 480px) {
  .filter-actions {
    width: 100%;
    flex-direction: column;
    align-items: stretch;
  }
  .filter-actions .n-button {
    width: 100%;
  }
}

.table-main {
  background: white;
  border-radius: 8px;
  overflow: hidden;
}
.table-container {
  /* background: white;
  border-radius: 8px; */
  flex: 1;
  overflow: auto;
  position: relative;
}
.empty-container {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
}
.pagination-container {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px;
  border-top: 1px solid #f0f0f0;
}
.pagination-info {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 13px;
  color: #666;
}
.pagination-controls {
  display: flex;
  align-items: center;
  gap: 12px;
}
.page-info {
  font-size: 13px;
  color: #666;
}
</style>
