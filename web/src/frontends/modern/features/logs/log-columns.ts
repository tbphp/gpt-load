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
// 表格列：细分缓存写入与两个完整性状态只在详情面板展示，不进表格与列选择器。
const definitions: readonly [LogColumnId, number, LogColumnSection, boolean, boolean?, number?][] =
  [
    ['completed_at_ms', 80, 'request', true],
    ['request_id', 180, 'request', false],
    ['client_model', 136, 'request', true, false, 2],
    ['protocol', 116, 'request', true],
    ['operation', 72, 'request', false],
    ['group', 120, 'routing', true, true, 1],
    ['channel', 104, 'routing', true, true],
    ['credential_name', 132, 'routing', true, true, 1],
    ['access_key', 108, 'request', true, true, 1],
    ['status', 68, 'request', true],
    ['status_code', 64, 'request', true],
    ['stream', 48, 'request', true],
    ['attempt_count', 48, 'routing', true, true],
    ['duration_ms', 64, 'performance', true],
    ['first_response_ms', 56, 'performance', true],
    ['input_tokens', 56, 'tokens', true],
    ['output_tokens', 56, 'tokens', true],
    ['estimated_cost_nano_usd', 76, 'billing', true],
    ['cost_state', 56, 'billing', false],
    ['cache_read_tokens', 60, 'tokens', false],
    ['cache_hit_rate', 56, 'tokens', false],
    ['cache_write_tokens', 60, 'tokens', false],
    ['total_tokens', 56, 'tokens', false],
    ['upstream_model', 136, 'routing', false, true],
    ['upstream_protocol', 116, 'routing', false, true],
    ['upstream_reported_model', 152, 'routing', false, true],
    ['model_consistency', 56, 'routing', false, true],
    ['route_mode', 56, 'routing', false, true],
    ['affinity_hit', 48, 'routing', false, true],
    ['reasoning_mode', 120, 'request', false],
    ['pricing_mode', 64, 'billing', false],
    ['context_threshold_tokens', 64, 'billing', false],
    ['error_code', 132, 'request', false],
    ['error_summary', 200, 'request', false, false, 2],
  ]
// 右对齐并使用等宽数字的字段，便于按位比较。
export const numericColumns: ReadonlySet<LogColumnId> = new Set([
  'status_code',
  'attempt_count',
  'duration_ms',
  'first_response_ms',
  'input_tokens',
  'output_tokens',
  'cache_read_tokens',
  'cache_hit_rate',
  'cache_write_tokens',
  'total_tokens',
  'estimated_cost_nano_usd',
  'context_threshold_tokens',
])
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
  // 每个字段都归入一组语义相近的搭档，避免非默认列各占一列。
  // 两边同等重要的配对，第二行不降级为附属信息。
  const peerPairs: ReadonlySet<string> = new Set([
    'stream-attempt_count',
    'duration_ms-first_response_ms',
    'input_tokens-output_tokens',
    'cache_read_tokens-cache_hit_rate',
    'cache_write_tokens-total_tokens',
    'route_mode-affinity_hit',
    'pricing_mode-context_threshold_tokens',
  ])
  const pairs: readonly (readonly [LogColumnId, LogColumnId, number])[] = [
    ['completed_at_ms', 'request_id', 152],
    ['client_model', 'protocol', 144],
    ['group', 'channel', 148],
    ['credential_name', 'access_key', 156],
    ['status', 'status_code', 76],
    ['stream', 'attempt_count', 64],
    ['duration_ms', 'first_response_ms', 72],
    ['input_tokens', 'output_tokens', 68],
    ['cache_read_tokens', 'cache_hit_rate', 68],
    ['cache_write_tokens', 'total_tokens', 68],
    ['estimated_cost_nano_usd', 'cost_state', 84],
    ['pricing_mode', 'context_threshold_tokens', 80],
    ['upstream_model', 'upstream_protocol', 144],
    ['upstream_reported_model', 'model_consistency', 152],
    ['route_mode', 'affinity_hit', 64],
    ['error_code', 'error_summary', 200],
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
      return [
        {
          id: fields.join('-'),
          fields,
          width: pair?.[2] ?? column.width,
          grow: column.grow,
          numeric: fields.every((field) => numericColumns.has(field)),
          peer: peerPairs.has(fields.join('-')),
        },
      ]
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
      '--modern-log-width': `calc(${cells.value.reduce((width, cell) => width + cell.width + 12, 12)}px + var(--modern-log-action-width))`,
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
