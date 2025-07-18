// Generic API response structure
export interface ApiResponse<T> {
  code: number;
  message: string;
  data: T;
}

// Key status
export type KeyStatus = "active" | "invalid" | undefined;

// Data model definitions
export interface APIKey {
  id: number;
  group_id: number;
  key_value: string;
  status: KeyStatus;
  request_count: number;
  failure_count: number;
  last_used_at?: string;
  created_at: string;
  updated_at: string;
}

// Type alias for compatibility
export type Key = APIKey;

export interface UpstreamInfo {
  url: string;
  weight: number;
}

export interface Group {
  id?: number;
  name: string;
  display_name: string;
  description: string;
  sort: number;
  test_model: string;
  channel_type: "openai" | "gemini";
  upstreams: UpstreamInfo[];
  config: Record<string, unknown>;
  api_keys?: APIKey[];
  endpoint?: string;
  param_overrides: Record<string, unknown>;
  created_at?: string;
  updated_at?: string;
}

export interface GroupConfigOption {
  key: string;
  name: string;
  description: string;
  default_value: number;
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
}

// RequestStats defines the statistics for requests over a period.
export interface RequestStats {
  total_requests: number;
  failed_requests: number;
  failure_rate: number;
}

export interface TaskInfo {
  is_running: boolean;
  group_name?: string;
  processed?: number;
  total?: number;
  started_at?: string;
  finished_at?: string;
  result?: {
    invalid_keys: number;
    total_keys: number;
    valid_keys: number;
  };
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

// Dashboard statistics card data
export interface StatCard {
  value: number;
  sub_value?: number;
  sub_value_tip?: string;
  trend: number;
  trend_is_growth: boolean;
}

// Dashboard basic statistics response
export interface DashboardStatsResponse {
  key_count: StatCard;
  group_count: StatCard;
  request_count: StatCard;
  error_rate: StatCard;
}

// Chart dataset
export interface ChartDataset {
  label: string;
  data: number[];
  color: string;
}

// Chart data
export interface ChartData {
  labels: string[];
  datasets: ChartDataset[];
}
