<template>
  <div class="config-card">
    <div class="card-header">
      <h3 class="card-title">
        <i class="fas fa-cog"></i>
        配置管理
      </h3>
      <el-button 
        type="primary" 
        size="small" 
        :loading="loading"
        @click="$emit('refresh')"
      >
        <i class="fas fa-sync-alt"></i>
        刷新
      </el-button>
    </div>

    <div class="card-content" v-if="config">
      <el-form 
        :model="formData" 
        label-width="140px" 
        size="default"
        @submit.prevent="handleSubmit"
      >
        <!-- 重试配置 -->
        <div class="config-section">
          <h4 class="section-title">重试配置</h4>
          
          <el-form-item label="最大重试次数">
            <el-input-number
              v-model="formData.max_consecutive_retries"
              :min="1"
              :max="200"
              :step="1"
              style="width: 200px"
            />
            <span class="field-hint">流中断时的最大重试次数 (1-200)</span>
          </el-form-item>

          <el-form-item label="重试延迟">
            <el-input-number
              v-model="formData.retry_delay_ms"
              :min="100"
              :max="10000"
              :step="50"
              style="width: 200px"
            />
            <span class="field-hint">重试间隔时间，毫秒 (100-10000)</span>
          </el-form-item>
        </div>

        <!-- 内容过滤配置 -->
        <div class="config-section">
          <h4 class="section-title">内容过滤</h4>
          
          <el-form-item label="过滤思考内容">
            <el-switch
              v-model="formData.swallow_thoughts_after_retry"
              active-text="启用"
              inactive-text="禁用"
            />
            <span class="field-hint">重试后自动过滤 Gemini 的思考过程</span>
          </el-form-item>

          <el-form-item label="标点启发式">
            <el-switch
              v-model="formData.enable_punctuation_heuristic"
              active-text="启用"
              inactive-text="禁用"
            />
            <span class="field-hint">使用标点符号判断内容完整性</span>
          </el-form-item>
        </div>

        <!-- 输出限制配置 -->
        <div class="config-section">
          <h4 class="section-title">输出限制</h4>
          
          <el-form-item label="最大输出字符">
            <el-input-number
              v-model="formData.max_output_chars"
              :min="0"
              :max="1000000"
              :step="1000"
              style="width: 200px"
            />
            <span class="field-hint">限制输出字符数，0 表示无限制</span>
          </el-form-item>

          <el-form-item label="流超时时间">
            <el-input-number
              v-model="formData.stream_timeout"
              :min="30"
              :max="3600"
              :step="30"
              style="width: 200px"
            />
            <span class="field-hint">流式响应超时时间，秒 (30-3600)</span>
          </el-form-item>
        </div>

        <!-- 调试配置 -->
        <div class="config-section">
          <h4 class="section-title">调试选项</h4>
          
          <el-form-item label="详细日志">
            <el-switch
              v-model="formData.enable_detailed_logging"
              active-text="启用"
              inactive-text="禁用"
            />
            <span class="field-hint">启用详细的调试日志记录</span>
          </el-form-item>

          <el-form-item label="保存重试请求">
            <el-switch
              v-model="formData.save_retry_requests"
              active-text="启用"
              inactive-text="禁用"
            />
            <span class="field-hint">保存重试请求内容用于调试</span>
          </el-form-item>
        </div>

        <!-- 操作按钮 -->
        <div class="form-actions">
          <el-button 
            type="primary" 
            :loading="saving"
            @click="handleSubmit"
          >
            <i class="fas fa-save"></i>
            保存配置
          </el-button>
          
          <el-button 
            type="default"
            @click="resetForm"
          >
            <i class="fas fa-undo"></i>
            重置
          </el-button>
        </div>
      </el-form>
    </div>

    <div class="card-content" v-else-if="loading">
      <div class="loading-state">
        <i class="fas fa-spinner fa-spin"></i>
        <p>加载配置中...</p>
      </div>
    </div>

    <div class="card-content" v-else>
      <div class="error-state">
        <i class="fas fa-exclamation-triangle"></i>
        <p>配置加载失败</p>
        <el-button type="primary" @click="$emit('refresh')">重试</el-button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { useMessage } from 'naive-ui'
import { 
  validateGeminiConfig,
  type GeminiConfig, 
  type GeminiConfigUpdate 
} from '@/api/gemini'

// Props
interface Props {
  config: GeminiConfig | null
  loading: boolean
}

const props = defineProps<Props>()

// Emits
const emit = defineEmits<{
  update: [config: GeminiConfigUpdate]
  refresh: []
}>()

// 响应式数据
const saving = ref(false)
const message = useMessage()
const formData = ref<GeminiConfig>({
  max_consecutive_retries: 100,
  retry_delay_ms: 750,
  swallow_thoughts_after_retry: true,
  enable_punctuation_heuristic: true,
  enable_detailed_logging: false,
  save_retry_requests: false,
  max_output_chars: 0,
  stream_timeout: 300
})

// 监听配置变化
watch(() => props.config, (newConfig) => {
  if (newConfig) {
    formData.value = { ...newConfig }
  }
}, { immediate: true })

// 检查是否有变化
const hasChanges = computed(() => {
  if (!props.config) return false
  
  return Object.keys(formData.value).some(key => {
    const formKey = key as keyof GeminiConfig
    return formData.value[formKey] !== props.config![formKey]
  })
})

// 提交表单
const handleSubmit = async () => {
  if (!props.config || !hasChanges.value) {
    message.info('没有配置变更')
    return
  }

  // 验证配置
  const errors = validateGeminiConfig(formData.value)
  if (errors.length > 0) {
    message.error(errors[0])
    return
  }

  try {
    saving.value = true
    
    // 计算变更的字段
    const changes: GeminiConfigUpdate = {}
    Object.keys(formData.value).forEach(key => {
      const formKey = key as keyof GeminiConfig
      if (formData.value[formKey] !== props.config![formKey]) {
        ;(changes as any)[formKey] = formData.value[formKey]
      }
    })

    emit('update', changes)
  } catch (error) {
    console.error('Failed to save config:', error)
  } finally {
    saving.value = false
  }
}

// 重置表单
const resetForm = () => {
  if (props.config) {
    formData.value = { ...props.config }
    message.success('表单已重置')
  }
}
</script>

<style scoped>
.config-card {
  background: white;
  border-radius: 8px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
  overflow: hidden;
}

.card-header {
  padding: 20px;
  border-bottom: 1px solid #e5e7eb;
  display: flex;
  justify-content: space-between;
  align-items: center;
  background: #f9fafb;
}

.card-title {
  font-size: 18px;
  font-weight: 600;
  color: #1f2937;
  margin: 0;
  display: flex;
  align-items: center;
  gap: 8px;
}

.card-title i {
  color: #6366f1;
}

.card-content {
  padding: 24px;
}

.config-section {
  margin-bottom: 32px;
}

.config-section:last-child {
  margin-bottom: 0;
}

.section-title {
  font-size: 16px;
  font-weight: 600;
  color: #374151;
  margin: 0 0 16px 0;
  padding-bottom: 8px;
  border-bottom: 2px solid #e5e7eb;
}

.field-hint {
  display: block;
  font-size: 12px;
  color: #6b7280;
  margin-top: 4px;
  margin-left: 8px;
}

.form-actions {
  margin-top: 32px;
  padding-top: 24px;
  border-top: 1px solid #e5e7eb;
  display: flex;
  gap: 12px;
}

.loading-state,
.error-state {
  text-align: center;
  padding: 40px 20px;
}

.loading-state i {
  font-size: 32px;
  color: #6366f1;
  margin-bottom: 16px;
}

.error-state i {
  font-size: 32px;
  color: #ef4444;
  margin-bottom: 16px;
}

.loading-state p,
.error-state p {
  font-size: 16px;
  color: #6b7280;
  margin: 0 0 16px 0;
}

:deep(.el-form-item__label) {
  font-weight: 500;
  color: #374151;
}

:deep(.el-input-number) {
  width: 100%;
}

:deep(.el-switch) {
  margin-right: 8px;
}
</style>
