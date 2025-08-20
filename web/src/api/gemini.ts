import { http } from '@/utils/http'

// Gemini 配置接口
export interface GeminiConfig {
  max_consecutive_retries: number
  retry_delay_ms: number
  swallow_thoughts_after_retry: boolean
  enable_punctuation_heuristic: boolean
  enable_detailed_logging: boolean
  save_retry_requests: boolean
  max_output_chars: number
  stream_timeout: number
}

// Gemini 配置更新接口
export interface GeminiConfigUpdate {
  max_consecutive_retries?: number
  retry_delay_ms?: number
  swallow_thoughts_after_retry?: boolean
  enable_punctuation_heuristic?: boolean
  enable_detailed_logging?: boolean
  save_retry_requests?: boolean
  max_output_chars?: number
  stream_timeout?: number
}

// Gemini 统计接口
export interface GeminiStats {
  total_logs: number
  successful_logs: number
  failed_logs: number
  success_rate: number
  total_retries: number
  average_retries: number
  max_retries: number
  thoughts_filtered: number
  filter_rate: number
  average_duration_ms: number
  max_duration_ms: number
  min_duration_ms: number
  interruption_stats: Record<string, number>
  start_time: string
  end_time: string
}

// Gemini 日志接口
export interface GeminiLog {
  id: number
  request_id: string
  group_id: number
  group_name: string
  key_value: string
  retry_count: number
  interrupt_reason: string
  final_success: boolean
  accumulated_text: string
  thought_filtered: boolean
  output_chars: number
  total_duration_ms: number
  retry_duration_ms: number
  original_request?: string
  retry_requests?: string
  error_message?: string
  created_at: string
  updated_at: string
}

// Gemini 日志摘要接口
export interface GeminiLogSummary {
  id: number
  request_id: string
  group_name: string
  retry_count: number
  interrupt_reason: string
  final_success: boolean
  thought_filtered: boolean
  output_chars: number
  total_duration_ms: number
  created_at: string
}

// Gemini 日志查询参数
export interface GeminiLogQueryParams {
  page?: number
  page_size?: number
  group_id?: number
  group_name?: string
  key_value?: string
  interrupt_reason?: string
  final_success?: boolean
  thought_filtered?: boolean
  min_retry_count?: number
  max_retry_count?: number
  start_time?: string
  end_time?: string
  order_by?: string
  order_desc?: boolean
}

// Gemini 日志响应接口
export interface GeminiLogResponse {
  logs: GeminiLog[]
  total: number
  page: number
  page_size: number
  total_pages: number
}

// Gemini 健康状态接口
export interface GeminiHealth {
  status: string
  success_rate: number
  total_logs: number
  average_retries: number
  thoughts_filtered: number
  last_24h_stats: GeminiStats
  timestamp: string
}

// API 函数

/**
 * 获取 Gemini 配置
 */
export const getGeminiSettings = async (): Promise<GeminiConfig> => {
  const response = await http.get('/api/gemini/settings')
  return response.data
}

/**
 * 更新 Gemini 配置
 */
export const updateGeminiSettings = async (config: GeminiConfigUpdate): Promise<void> => {
  await http.put('/api/gemini/settings', config)
}

/**
 * 获取 Gemini 统计数据
 */
export const getGeminiStats = async (days: number = 7): Promise<GeminiStats> => {
  const response = await http.get('/api/gemini/stats', {
    params: { days }
  })
  return response.data
}

/**
 * 获取 Gemini 日志
 */
export const getGeminiLogs = async (params: GeminiLogQueryParams = {}): Promise<GeminiLogResponse> => {
  const response = await http.get('/api/gemini/logs', {
    params: {
      page: 1,
      page_size: 20,
      order_by: 'created_at',
      order_desc: true,
      ...params
    }
  })
  return response.data
}

/**
 * 获取最近的 Gemini 日志
 */
export const getRecentGeminiLogs = async (limit: number = 10): Promise<GeminiLogSummary[]> => {
  const response = await http.get('/api/gemini/recent-logs', {
    params: { limit }
  })
  return response.data
}

/**
 * 重置 Gemini 统计数据
 */
export const resetGeminiStats = async (days: number = 30): Promise<void> => {
  await http.post('/api/gemini/reset-stats', null, {
    params: { days }
  })
}

/**
 * 获取 Gemini 健康状态
 */
export const getGeminiHealth = async (): Promise<GeminiHealth> => {
  const response = await http.get('/api/gemini/health')
  return response.data
}

// 工具函数

/**
 * 格式化中断原因
 */
export const formatInterruptReason = (reason: string): string => {
  const reasonMap: Record<string, string> = {
    'BLOCK': '内容被阻止',
    'DROP': '流连接中断',
    'INCOMPLETE': '响应不完整',
    'FINISH_ABNORMAL': '异常结束',
    'FINISH_DURING_THOUGHT': '思考过程中结束',
    'TIMEOUT': '超时'
  }
  return reasonMap[reason] || reason
}

/**
 * 格式化持续时间
 */
export const formatDuration = (ms: number): string => {
  if (ms < 1000) {
    return `${ms}ms`
  } else if (ms < 60000) {
    return `${(ms / 1000).toFixed(1)}s`
  } else {
    return `${(ms / 60000).toFixed(1)}min`
  }
}

/**
 * 格式化成功率
 */
export const formatSuccessRate = (rate: number): string => {
  return `${(rate * 100).toFixed(1)}%`
}

/**
 * 获取状态颜色
 */
export const getStatusColor = (status: string): string => {
  switch (status) {
    case 'healthy':
      return 'success'
    case 'degraded':
      return 'warning'
    case 'unhealthy':
      return 'danger'
    default:
      return 'info'
  }
}

/**
 * 获取成功状态颜色
 */
export const getSuccessColor = (success: boolean): string => {
  return success ? 'success' : 'danger'
}

/**
 * 格式化字节数
 */
export const formatBytes = (bytes: number): string => {
  if (bytes === 0) return '0 B'
  
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
}

/**
 * 验证配置值
 */
export const validateGeminiConfig = (config: GeminiConfigUpdate): string[] => {
  const errors: string[] = []
  
  if (config.max_consecutive_retries !== undefined) {
    if (config.max_consecutive_retries < 1 || config.max_consecutive_retries > 200) {
      errors.push('最大重试次数必须在 1-200 之间')
    }
  }
  
  if (config.retry_delay_ms !== undefined) {
    if (config.retry_delay_ms < 100 || config.retry_delay_ms > 10000) {
      errors.push('重试延迟必须在 100-10000 毫秒之间')
    }
  }
  
  if (config.max_output_chars !== undefined) {
    if (config.max_output_chars < 0) {
      errors.push('最大输出字符数不能为负数')
    }
  }
  
  if (config.stream_timeout !== undefined) {
    if (config.stream_timeout < 30 || config.stream_timeout > 3600) {
      errors.push('流超时时间必须在 30-3600 秒之间')
    }
  }
  
  return errors
}
