// 通用 API 响应结构
export interface ApiResponse<T> {
  code: number;
  message: string;
  data: T;
}

// 密钥状态
export type KeyStatus = "active" | "invalid" | "rate_limited" | undefined;

// 数据模型定义
export interface APIKey {
  id: number;
  group_id: number;
  key_value: string;
  status: KeyStatus;
  request_count: number;
  failure_count: number;
  rate_limit_count: number;        // 429错误次数
  last_used_at?: string;
  last_429_at?: string;           // 最后一次429错误时间
  rate_limit_reset_at?: string;   // 预计配额重置时间
  created_at: string;
  updated_at: string;
}

// 类型别名，用于兼容
export type Key = APIKey;

export interface UpstreamInfo {
  url: string;
  weight: number;
}

export interface HeaderRule {
  key: string;
  value: string;
  action: "set" | "remove";
}

export interface Group {
  id?: number;
  name: string;
  display_name: string;
  description: string;
  sort: number;
  test_model: string;
  channel_type: "openai" | "gemini" | "anthropic";
  upstreams: UpstreamInfo[];
  validation_endpoint: string;
  config: Record<string, unknown>;
  api_keys?: APIKey[];
  endpoint?: string;
  param_overrides: Record<string, unknown>;
  header_rules?: HeaderRule[];
  proxy_keys: string;
  created_at?: string;
  updated_at?: string;
}

export interface GroupConfigOption {
  key: string;
  name: string;
  description: string;
  default_value: string | number;
}

// GroupStatsResponse defines the complete statistics for a group.
export interface GroupStatsResponse {
  key_stats: KeyStats;
  hourly_stats: RequestStats;
  daily_stats: RequestStats;
  weekly_stats: RequestStats;
}

// KeyStats defines the statistics for API keys in a group.
export interface KeyStats {
  total_keys: number;
  active_keys: number;
  invalid_keys: number;
  rate_limited_keys: number;  // 429限流状态的密钥数量
}

// RequestStats defines the statistics for requests over a period.
export interface RequestStats {
  total_requests: number;
  failed_requests: number;
  failure_rate: number;
}

export type TaskType = "KEY_VALIDATION" | "KEY_IMPORT";

export interface KeyValidationResult {
  invalid_keys: number;
  total_keys: number;
  valid_keys: number;
}

export interface KeyImportResult {
  added_count: number;
  ignored_count: number;
}

export interface TaskInfo {
  task_type: TaskType;
  is_running: boolean;
  group_name?: string;
  processed?: number;
  total?: number;
  started_at?: string;
  finished_at?: string;
  result?: KeyValidationResult | KeyImportResult;
  error?: string;
}

// Based on backend response
export interface RequestLog {
  id: string;
  timestamp: string;
  group_id: number;
  key_id: number;
  is_success: boolean;
  source_ip: string;
  status_code: number;
  request_path: string;
  duration_ms: number;
  error_message: string;
  user_agent: string;
  retries: number;
  group_name?: string;
  key_value?: string;
  model: string;
  upstream_addr: string;
  is_stream: boolean;
}

export interface Pagination {
  page: number;
  page_size: number;
  total_items: number;
  total_pages: number;
}

export interface LogsResponse {
  items: RequestLog[];
  pagination: Pagination;
}

export interface LogFilter {
  page?: number;
  page_size?: number;
  group_name?: string;
  key_value?: string;
  model?: string;
  is_success?: boolean | null;
  status_code?: number | null;
  source_ip?: string;
  error_contains?: string;
  start_time?: string | null;
  end_time?: string | null;
}

export interface DashboardStats {
  total_requests: number;
  success_requests: number;
  success_rate: number;
  group_stats: GroupRequestStat[];
}

export interface GroupRequestStat {
  display_name: string;
  request_count: number;
}

// 仪表盘统计卡片数据
export interface StatCard {
  value: number;
  sub_value?: number;
  sub_value_tip?: string;
  trend: number;
  trend_is_growth: boolean;
}

// 仪表盘基础统计响应
export interface DashboardStatsResponse {
  key_count: StatCard;
  rpm: StatCard;
  request_count: StatCard;
  error_rate: StatCard;
}

// 图表数据集
export interface ChartDataset {
  label: string;
  data: number[];
  color: string;
}

// 图表数据
export interface ChartData {
  labels: string[];
  datasets: ChartDataset[];
}

// Token池相关类型定义
export interface PoolStats {
  validation_pool: number;
  ready_pool: number;
  active_pool: number;
  cooling_pool: number;
  total_keys: number;
  last_updated: string;
}

export interface PoolStatsResponse {
  group_id: number;
  group_name: string;
  pool_stats: PoolStats;
  performance_metrics: {
    throughput: number;
    avg_latency: number;
    error_rate: number;
    cache_hit_rate: number;
  };
  pool_health: {
    status: "healthy" | "warning" | "critical";
    issues: string[];
  };
  last_updated: string;
}

export interface RecoveryMetrics {
  total_recovery_attempts: number;
  successful_recoveries: number;
  failed_recoveries: number;
  overall_success_rate: number;
  recent_success_rate: number;
  avg_recovery_latency: number;
  recoveries_per_hour: number;
  last_recovery_at?: string;
  error_stats: {
    [key: string]: number;
  };
  hourly_stats: Array<{
    hour: string;
    attempts: number;
    successes: number;
    success_rate: number;
  }>;
}

export interface RecoveryPlan {
  id: string;
  key_id: number;
  group_id: number;
  priority: "low" | "normal" | "high" | "critical";
  scheduled_at: string;
  estimated_delay: number;
  strategy: string;
  status: "pending" | "processing" | "completed" | "failed";
  created_at: string;
}

export interface BatchRecoveryRequest {
  priority?: "low" | "normal" | "high" | "critical";
  max_concurrent?: number;
  delay_between_batches?: number;
  filter?: {
    min_failure_count?: number;
    max_failure_count?: number;
    last_429_before?: string;
    last_429_after?: string;
  };
}

export interface PoolConfiguration {
  pool_type: "redis" | "memory";
  redis_config?: {
    address: string;
    password?: string;
    db: number;
    pool_size: number;
  };
  memory_config?: {
    shard_count: number;
    cache_size: number;
    gc_interval: number;
  };
  recovery_config: {
    enable_auto_recovery: boolean;
    check_interval: number;
    batch_size: number;
    max_concurrent_recoveries: number;
    recovery_timeout: number;
  };
  performance_config: {
    enable_metrics: boolean;
    metrics_interval: number;
    enable_caching: boolean;
    cache_ttl: number;
  };
}
