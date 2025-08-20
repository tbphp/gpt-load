<template>
  <el-dialog
    v-model="visible"
    title="Gemini 详细日志"
    width="90%"
    :before-close="handleClose"
    class="logs-modal"
  >
    <!-- 搜索和过滤 -->
    <div class="search-section">
      <div class="search-row">
        <el-input
          v-model="searchParams.group_name"
          placeholder="搜索分组名称"
          clearable
          style="width: 200px"
        >
          <template #prefix>
            <i class="fas fa-search"></i>
          </template>
        </el-input>

        <el-select
          v-model="searchParams.final_success"
          placeholder="处理状态"
          clearable
          style="width: 150px"
        >
          <el-option label="全部" :value="undefined" />
          <el-option label="成功" :value="true" />
          <el-option label="失败" :value="false" />
        </el-select>

        <el-select
          v-model="searchParams.interrupt_reason"
          placeholder="中断原因"
          clearable
          style="width: 150px"
        >
          <el-option label="全部" :value="undefined" />
          <el-option label="内容被阻止" value="BLOCK" />
          <el-option label="流连接中断" value="DROP" />
          <el-option label="响应不完整" value="INCOMPLETE" />
          <el-option label="异常结束" value="FINISH_ABNORMAL" />
          <el-option label="思考过程中结束" value="FINISH_DURING_THOUGHT" />
          <el-option label="超时" value="TIMEOUT" />
        </el-select>

        <el-date-picker
          v-model="dateRange"
          type="datetimerange"
          range-separator="至"
          start-placeholder="开始时间"
          end-placeholder="结束时间"
          format="YYYY-MM-DD HH:mm"
          value-format="YYYY-MM-DDTHH:mm:ss.SSSZ"
          style="width: 350px"
        />

        <el-button type="primary" @click="handleSearch">
          <i class="fas fa-search"></i>
          搜索
        </el-button>

        <el-button @click="handleReset">
          <i class="fas fa-undo"></i>
          重置
        </el-button>
      </div>
    </div>

    <!-- 数据表格 -->
    <el-table
      :data="logs"
      v-loading="loading"
      stripe
      height="500"
      @sort-change="handleSortChange"
    >
      <el-table-column prop="id" label="ID" width="80" />
      
      <el-table-column prop="group_name" label="分组" width="120" />
      
      <el-table-column label="状态" width="80" align="center">
        <template #default="{ row }">
          <el-tag 
            :type="row.final_success ? 'success' : 'danger'"
            size="small"
          >
            {{ row.final_success ? '成功' : '失败' }}
          </el-tag>
        </template>
      </el-table-column>
      
      <el-table-column prop="retry_count" label="重试" width="80" align="center">
        <template #default="{ row }">
          <span v-if="row.retry_count > 0" class="retry-badge">
            {{ row.retry_count }}
          </span>
          <span v-else class="no-retry">-</span>
        </template>
      </el-table-column>
      
      <el-table-column prop="interrupt_reason" label="中断原因" width="120">
        <template #default="{ row }">
          <span v-if="row.interrupt_reason">
            {{ formatInterruptReason(row.interrupt_reason) }}
          </span>
          <span v-else class="no-interrupt">-</span>
        </template>
      </el-table-column>
      
      <el-table-column prop="output_chars" label="输出字符" width="100" align="right">
        <template #default="{ row }">
          {{ row.output_chars.toLocaleString() }}
        </template>
      </el-table-column>
      
      <el-table-column prop="total_duration_ms" label="响应时间" width="100" sortable="custom">
        <template #default="{ row }">
          {{ formatDuration(row.total_duration_ms) }}
        </template>
      </el-table-column>
      
      <el-table-column label="思考过滤" width="80" align="center">
        <template #default="{ row }">
          <i 
            v-if="row.thought_filtered" 
            class="fas fa-filter thought-filtered-icon"
            title="已过滤思考内容"
          ></i>
          <span v-else>-</span>
        </template>
      </el-table-column>
      
      <el-table-column prop="created_at" label="创建时间" width="160" sortable="custom">
        <template #default="{ row }">
          {{ formatDateTime(row.created_at) }}
        </template>
      </el-table-column>
      
      <el-table-column label="操作" width="100" fixed="right">
        <template #default="{ row }">
          <el-button 
            type="primary" 
            size="small" 
            @click="showLogDetail(row)"
          >
            详情
          </el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- 分页 -->
    <div class="pagination-section">
      <el-pagination
        v-model:current-page="currentPage"
        v-model:page-size="pageSize"
        :page-sizes="[10, 20, 50, 100]"
        :total="total"
        layout="total, sizes, prev, pager, next, jumper"
        @size-change="handleSizeChange"
        @current-change="handleCurrentChange"
      />
    </div>

    <!-- 日志详情对话框 -->
    <el-dialog
      v-model="detailVisible"
      title="日志详情"
      width="70%"
      append-to-body
    >
      <div v-if="selectedLog" class="log-detail">
        <div class="detail-grid">
          <div class="detail-item">
            <span class="detail-label">请求ID:</span>
            <span class="detail-value">{{ selectedLog.request_id }}</span>
          </div>
          <div class="detail-item">
            <span class="detail-label">分组:</span>
            <span class="detail-value">{{ selectedLog.group_name }}</span>
          </div>
          <div class="detail-item">
            <span class="detail-label">密钥:</span>
            <span class="detail-value">{{ selectedLog.key_value }}</span>
          </div>
          <div class="detail-item">
            <span class="detail-label">重试次数:</span>
            <span class="detail-value">{{ selectedLog.retry_count }}</span>
          </div>
          <div class="detail-item">
            <span class="detail-label">最终状态:</span>
            <el-tag :type="selectedLog.final_success ? 'success' : 'danger'">
              {{ selectedLog.final_success ? '成功' : '失败' }}
            </el-tag>
          </div>
          <div class="detail-item">
            <span class="detail-label">中断原因:</span>
            <span class="detail-value">
              {{ selectedLog.interrupt_reason ? formatInterruptReason(selectedLog.interrupt_reason) : '-' }}
            </span>
          </div>
          <div class="detail-item">
            <span class="detail-label">输出字符数:</span>
            <span class="detail-value">{{ selectedLog.output_chars.toLocaleString() }}</span>
          </div>
          <div class="detail-item">
            <span class="detail-label">总响应时间:</span>
            <span class="detail-value">{{ formatDuration(selectedLog.total_duration_ms) }}</span>
          </div>
          <div class="detail-item">
            <span class="detail-label">重试时间:</span>
            <span class="detail-value">{{ formatDuration(selectedLog.retry_duration_ms) }}</span>
          </div>
          <div class="detail-item">
            <span class="detail-label">思考过滤:</span>
            <span class="detail-value">{{ selectedLog.thought_filtered ? '是' : '否' }}</span>
          </div>
          <div class="detail-item">
            <span class="detail-label">创建时间:</span>
            <span class="detail-value">{{ formatDateTime(selectedLog.created_at) }}</span>
          </div>
          <div class="detail-item">
            <span class="detail-label">更新时间:</span>
            <span class="detail-value">{{ formatDateTime(selectedLog.updated_at) }}</span>
          </div>
        </div>

        <!-- 累积文本 -->
        <div v-if="selectedLog.accumulated_text" class="accumulated-text">
          <h4>累积文本内容:</h4>
          <div class="text-content">
            {{ selectedLog.accumulated_text }}
          </div>
        </div>

        <!-- 错误信息 -->
        <div v-if="selectedLog.error_message" class="error-message">
          <h4>错误信息:</h4>
          <div class="error-content">
            {{ selectedLog.error_message }}
          </div>
        </div>
      </div>
    </el-dialog>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, computed } from 'vue'
import { ElMessage } from 'element-plus'
import { 
  getGeminiLogs,
  formatDuration,
  formatInterruptReason,
  type GeminiLog,
  type GeminiLogQueryParams
} from '@/api/gemini'

// Props
const emit = defineEmits<{
  close: []
}>()

// 响应式数据
const visible = ref(true)
const loading = ref(false)
const detailVisible = ref(false)

const logs = ref<GeminiLog[]>([])
const total = ref(0)
const currentPage = ref(1)
const pageSize = ref(20)

const selectedLog = ref<GeminiLog | null>(null)
const dateRange = ref<[string, string] | null>(null)

const searchParams = reactive<GeminiLogQueryParams>({
  group_name: '',
  final_success: undefined,
  interrupt_reason: '',
})

// 计算属性
const queryParams = computed(() => {
  const params: GeminiLogQueryParams = {
    page: currentPage.value,
    page_size: pageSize.value,
    ...searchParams
  }

  if (dateRange.value) {
    params.start_time = dateRange.value[0]
    params.end_time = dateRange.value[1]
  }

  // 清理空值
  Object.keys(params).forEach(key => {
    const value = (params as any)[key]
    if (value === '' || value === undefined || value === null) {
      delete (params as any)[key]
    }
  })

  return params
})

// 页面挂载
onMounted(() => {
  loadLogs()
})

// 方法
const loadLogs = async () => {
  try {
    loading.value = true
    const response = await getGeminiLogs(queryParams.value)
    logs.value = response.logs
    total.value = response.total
  } catch (error) {
    console.error('Failed to load logs:', error)
    ElMessage.error('加载日志失败')
  } finally {
    loading.value = false
  }
}

const handleSearch = () => {
  currentPage.value = 1
  loadLogs()
}

const handleReset = () => {
  Object.assign(searchParams, {
    group_name: '',
    final_success: undefined,
    interrupt_reason: '',
  })
  dateRange.value = null
  currentPage.value = 1
  loadLogs()
}

const handleSortChange = ({ prop, order }: { prop: string; order: string }) => {
  // 实现排序逻辑
  loadLogs()
}

const handleSizeChange = (size: number) => {
  pageSize.value = size
  currentPage.value = 1
  loadLogs()
}

const handleCurrentChange = (page: number) => {
  currentPage.value = page
  loadLogs()
}

const showLogDetail = (log: GeminiLog) => {
  selectedLog.value = log
  detailVisible.value = true
}

const handleClose = () => {
  visible.value = false
  emit('close')
}

const formatDateTime = (timestamp: string): string => {
  return new Date(timestamp).toLocaleString('zh-CN')
}
</script>

<style scoped>
.logs-modal :deep(.el-dialog__body) {
  padding: 20px;
}

.search-section {
  margin-bottom: 20px;
}

.search-row {
  display: flex;
  gap: 12px;
  align-items: center;
  flex-wrap: wrap;
}

.pagination-section {
  margin-top: 20px;
  display: flex;
  justify-content: center;
}

.retry-badge {
  background: #f59e0b;
  color: white;
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 12px;
  font-weight: 600;
}

.no-retry,
.no-interrupt {
  color: #9ca3af;
}

.thought-filtered-icon {
  color: #3b82f6;
}

.log-detail {
  max-height: 600px;
  overflow-y: auto;
}

.detail-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
  gap: 16px;
  margin-bottom: 24px;
}

.detail-item {
  display: flex;
  align-items: center;
  gap: 8px;
}

.detail-label {
  font-weight: 600;
  color: #374151;
  min-width: 100px;
}

.detail-value {
  color: #6b7280;
}

.accumulated-text,
.error-message {
  margin-top: 24px;
}

.accumulated-text h4,
.error-message h4 {
  margin: 0 0 12px 0;
  color: #374151;
}

.text-content,
.error-content {
  background: #f9fafb;
  border: 1px solid #e5e7eb;
  border-radius: 6px;
  padding: 16px;
  font-family: 'Courier New', monospace;
  font-size: 14px;
  line-height: 1.5;
  max-height: 300px;
  overflow-y: auto;
  white-space: pre-wrap;
  word-break: break-word;
}

.error-content {
  background: #fef2f2;
  border-color: #fecaca;
  color: #dc2626;
}

@media (max-width: 768px) {
  .search-row {
    flex-direction: column;
    align-items: stretch;
  }
  
  .search-row > * {
    width: 100% !important;
  }
  
  .detail-grid {
    grid-template-columns: 1fr;
  }
}
</style>
