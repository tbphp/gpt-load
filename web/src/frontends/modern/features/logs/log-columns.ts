import { computed, ref, watch } from 'vue'

export const logColumnIds = [
  'completed_at_ms',
  'request_id',
  'client_model',
  'protocol',
  'operation',
  'group',
  'channel',
  'credential_name',
  'access_key',
  'status',
  'status_code',
  'stream',
  'attempt_count',
  'first_response_ms',
  'duration_ms',
  'input_tokens',
  'output_tokens',
  'cache_read_tokens',
  'estimated_cost_nano_usd',
  'upstream_model',
  'upstream_reported_model',
  'model_consistency',
  'upstream_protocol',
  'route_mode',
  'affinity_hit',
  'reasoning_mode',
  'reasoning_effort',
  'reasoning_budget',
  'cache_hit_rate',
  'cache_write_tokens',
  'cache_write_5m_tokens',
  'cache_write_1h_tokens',
  'cache_write_unknown_tokens',
  'total_tokens',
  'usage_state',
  'cost_state',
  'pricing_completeness',
  'pricing_mode',
  'context_threshold_tokens',
  'error_code',
  'error_summary',
] as const
export type LogColumnId = (typeof logColumnIds)[number]
export type LogColumnSection = 'request' | 'routing' | 'performance' | 'tokens' | 'billing'
export interface LogColumn {
  id: LogColumnId
  width: number
  section: LogColumnSection
  defaultVisible: boolean
  admin: boolean
  grow: number
}
const definitions: readonly [LogColumnId, number, LogColumnSection, boolean, boolean?, number?][] =
  [
    ['completed_at_ms', 112, 'request', true],
    ['request_id', 260, 'request', false],
    ['client_model', 180, 'request', true, false, 2],
    ['protocol', 176, 'request', true],
    ['operation', 132, 'request', false],
    ['group', 120, 'routing', true, true, 1],
    ['channel', 150, 'routing', true, true],
    ['credential_name', 146, 'routing', true, true, 1],
    ['access_key', 124, 'request', true, true, 1],
    ['status', 82, 'request', true],
    ['status_code', 64, 'request', true],
    ['stream', 60, 'request', true],
    ['attempt_count', 62, 'routing', true, true],
    ['first_response_ms', 76, 'performance', true],
    ['duration_ms', 76, 'performance', true],
    ['input_tokens', 86, 'tokens', true],
    ['output_tokens', 86, 'tokens', true],
    ['cache_read_tokens', 96, 'tokens', true],
    ['estimated_cost_nano_usd', 100, 'billing', true],
    ['upstream_model', 180, 'routing', false, true],
    ['upstream_reported_model', 180, 'routing', false, true],
    ['model_consistency', 108, 'routing', false, true],
    ['upstream_protocol', 176, 'routing', false, true],
    ['route_mode', 92, 'routing', false, true],
    ['affinity_hit', 76, 'routing', false, true],
    ['reasoning_mode', 110, 'request', false],
    ['reasoning_effort', 98, 'request', false],
    ['reasoning_budget', 100, 'request', false],
    ['cache_hit_rate', 94, 'tokens', false],
    ['cache_write_tokens', 92, 'tokens', false],
    ['cache_write_5m_tokens', 112, 'tokens', false],
    ['cache_write_1h_tokens', 112, 'tokens', false],
    ['cache_write_unknown_tokens', 112, 'tokens', false],
    ['total_tokens', 94, 'tokens', false],
    ['usage_state', 100, 'tokens', false],
    ['cost_state', 100, 'billing', false],
    ['pricing_completeness', 110, 'billing', false],
    ['pricing_mode', 140, 'billing', false],
    ['context_threshold_tokens', 112, 'billing', false],
    ['error_code', 168, 'request', false],
    ['error_summary', 280, 'request', false, false, 2],
  ]
export const logColumns: readonly LogColumn[] = definitions.map(
  ([id, width, section, defaultVisible, admin, grow]) => ({
    id,
    width,
    section,
    defaultVisible,
    admin: Boolean(admin),
    grow: grow ?? 0,
  }),
)
export function useLogColumns(admin: boolean) {
  const available = logColumns.filter((column) => admin || !column.admin)
  const defaults = available.filter((column) => column.defaultVisible).map((column) => column.id)
  const storageKey = 'gpt-load.modern.logs.columns.' + (admin ? 'admin' : 'access-key')
  const failed = ref(false)
  let initial = defaults
  try {
    const raw: unknown = JSON.parse(window.localStorage.getItem(storageKey) ?? 'null')
    if (Array.isArray(raw)) {
      const saved = available.filter((column) => raw.includes(column.id)).map((column) => column.id)
      if (saved.length) initial = saved
    }
  } catch {
    /* 存储不可用时继续使用默认列。 */
  }
  const selected = ref<LogColumnId[]>(initial)
  watch(
    selected,
    (value) => {
      try {
        const raw = JSON.stringify(value)
        window.localStorage.setItem(storageKey, raw)
        failed.value = window.localStorage.getItem(storageKey) !== raw
      } catch {
        failed.value = true
      }
    },
    { deep: true },
  )
  const visible = computed(() => available.filter((column) => selected.value.includes(column.id)))
  // 选择仍按字段保存，只有同时可见的相关字段才合并为双行。
  const pairs: readonly (readonly [LogColumnId, LogColumnId, number])[] = [
    ['client_model', 'protocol', 200],
    ['group', 'channel', 160],
    ['credential_name', 'access_key', 184],
    ['status', 'status_code', 96],
    ['stream', 'attempt_count', 88],
    ['duration_ms', 'first_response_ms', 112],
    ['input_tokens', 'output_tokens', 112],
  ]
  const cells = computed(() => {
    const consumed = new Set<LogColumnId>()
    return visible.value.flatMap((column) => {
      if (consumed.has(column.id)) return []
      const pair = pairs.find(
        ([first, second]) =>
          (first === column.id || second === column.id) &&
          selected.value.includes(first) &&
          selected.value.includes(second),
      )
      const fields = pair ? [pair[0], pair[1]] : [column.id]
      fields.forEach((id) => consumed.add(id))
      return [{ id: fields.join('-'), fields, width: pair?.[2] ?? column.width, grow: column.grow }]
    })
  })
  const style = computed(() => {
    const fallback = cells.value.some((cell) => cell.grow) ? undefined : cells.value[0]?.id
    return {
      '--modern-log-columns': [
        ...cells.value.map((cell) =>
          cell.grow || cell.id === fallback
            ? `minmax(${cell.width}px, ${cell.grow || 1}fr)`
            : `${cell.width}px`,
        ),
        'var(--modern-log-action-width)',
      ].join(' '),
      '--modern-log-width': `calc(${cells.value.reduce((width, cell) => width + cell.width + 16, 24)}px + var(--modern-log-action-width))`,
    }
  })
  function toggle(id: LogColumnId, checked: boolean): void {
    if (!checked && selected.value.length <= 1) return
    selected.value = available
      .filter((column) => (column.id === id ? checked : selected.value.includes(column.id)))
      .map((column) => column.id)
  }
  return {
    available,
    visible,
    cells,
    selected,
    style,
    failed,
    toggle,
    reset: () => {
      selected.value = [...defaults]
    },
    showAll: () => {
      selected.value = available.map((column) => column.id)
    },
  }
}
