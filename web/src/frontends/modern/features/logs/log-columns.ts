import { computed, ref, watch } from 'vue'
import type { LogEntry } from '@modern/api/logs'
import { logCanMergeError } from './log-display'

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
export type LogColumnSection =
  'request' | 'models' | 'routing' | 'result' | 'performance' | 'tokens' | 'billing'
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
    ['group', 120, 'routing', true, true, 1],
    ['channel', 104, 'routing', true, true],
    ['credential_name', 132, 'routing', true, true, 1],
    ['access_key', 108, 'routing', true, true, 1],
    ['client_model', 152, 'models', true, false, 2],
    ['protocol', 152, 'request', true],
    ['reasoning_mode', 120, 'models', true],
    ['stream', 48, 'request', true],
    ['route_mode', 64, 'routing', true, true],
    ['affinity_hit', 56, 'routing', true, true],
    ['duration_ms', 72, 'performance', true],
    ['first_response_ms', 64, 'performance', true],
    ['status', 68, 'result', true],
    ['status_code', 64, 'result', true],
    ['attempt_count', 56, 'result', true, true],
    ['input_tokens', 56, 'tokens', true],
    ['output_tokens', 56, 'tokens', true],
    ['cache_read_tokens', 60, 'tokens', true],
    ['cache_hit_rate', 56, 'tokens', true],
    ['estimated_cost_nano_usd', 76, 'billing', true],
    // 用户额外选择的字段统一追加，不打断默认列和错误摘要区域。
    ['request_id', 180, 'request', false],
    ['operation', 88, 'request', false],
    ['upstream_reported_model', 152, 'models', false, true],
    ['error_code', 132, 'result', false],
    ['error_summary', 200, 'result', false, false, 2],
    ['cache_write_tokens', 60, 'tokens', false],
    ['total_tokens', 56, 'tokens', false],
    ['pricing_mode', 64, 'billing', false],
    ['context_threshold_tokens', 72, 'billing', false],
    ['cost_state', 64, 'billing', false],
  ]

const previousDefaultColumns: readonly LogColumnId[] = [
  'completed_at_ms',
  'client_model',
  'protocol',
  'group',
  'channel',
  'credential_name',
  'access_key',
  'status',
  'status_code',
  'stream',
  'attempt_count',
  'duration_ms',
  'first_response_ms',
  'input_tokens',
  'output_tokens',
  'estimated_cost_nano_usd',
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
  const defaultIDs = new Set(defaults)
  const storageKey = 'gpt-load.modern.logs.columns.' + (admin ? 'admin' : 'access-key')
  const failed = ref(false)
  let initial = defaults
  try {
    const raw: unknown = JSON.parse(window.localStorage.getItem(storageKey) ?? 'null')
    if (Array.isArray(raw)) {
      const availableIDs = new Set(available.map((column) => column.id))
      const saved = raw
        .map((id) =>
          id === 'upstream_model' || id === 'model_consistency' || id === 'features'
            ? 'client_model'
            : id,
        )
        .filter(
          (id): id is LogColumnId => typeof id === 'string' && availableIDs.has(id as LogColumnId),
        )
      const previousDefaults = previousDefaultColumns.filter((id) => availableIDs.has(id))
      const savedPreviousDefaults =
        new Set(saved).size === previousDefaults.length &&
        previousDefaults.every((id) => saved.includes(id))
      if (saved.length && !savedPreviousDefaults)
        initial = available.filter((column) => saved.includes(column.id)).map((column) => column.id)
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
    'reasoning_mode-stream',
    'duration_ms-first_response_ms',
    'input_tokens-output_tokens',
    'cache_read_tokens-cache_hit_rate',
    'cache_write_tokens-total_tokens',
    'route_mode-affinity_hit',
    'pricing_mode-context_threshold_tokens',
  ])
  const pairs: readonly (readonly [LogColumnId, LogColumnId, number])[] = [
    ['reasoning_mode', 'stream', 88],
    ['group', 'channel', 148],
    ['access_key', 'credential_name', 156],
    ['route_mode', 'affinity_hit', 62],
    ['status', 'status_code', 76],
    ['error_code', 'error_summary', 200],
    ['duration_ms', 'first_response_ms', 132],
    ['input_tokens', 'output_tokens', 68],
    ['cache_read_tokens', 'cache_hit_rate', 68],
    ['cache_write_tokens', 'total_tokens', 68],
    ['pricing_mode', 'context_threshold_tokens', 80],
  ]
  const cells = computed(() => {
    const consumed = new Set<LogColumnId>()
    return visible.value.flatMap((column) => {
      if (consumed.has(column.id)) return []
      const pair = pairs.find(
        ([first, second]) =>
          (first === column.id || second === column.id) &&
          defaultIDs.has(first) === defaultIDs.has(second) &&
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
          numeric: numericColumns.has(fields[0]),
          peer: peerPairs.has(fields.join('-')),
        },
      ]
    })
  })
  const errorStart = computed(() => {
    // 已单独展示错误时不再重复；隐藏列不参与占位和合并。
    if (selected.value.includes('error_code') || selected.value.includes('error_summary')) return -1
    const groups: readonly (readonly LogColumnId[])[] = [
      ['input_tokens', 'output_tokens'],
      ['cache_read_tokens', 'cache_hit_rate'],
      ['estimated_cost_nano_usd'],
    ]
    return cells.value.findIndex((_, start) =>
      groups.every((fields, offset) => {
        const cell = cells.value[start + offset]
        return cell !== undefined && cell.fields.every((field) => fields.includes(field))
      }),
    )
  })
  function forRow(row: LogEntry) {
    const start = errorStart.value >= 0 && logCanMergeError(row) ? errorStart.value : -1
    return cells.value.flatMap((cell, index) => {
      if (start >= 0 && index > start && index < start + 3) return []
      const error = index === start
      return [
        {
          ...cell,
          id: error ? 'error-summary' : cell.id,
          numeric: error ? false : cell.numeric,
          index,
          span: error ? 3 : 1,
          error,
        },
      ]
    })
  }
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
    forRow,
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
