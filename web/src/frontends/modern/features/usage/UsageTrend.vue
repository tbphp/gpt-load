<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UsageAggregate, UsageReport } from '@modern/api/usage'
import { AppSvg, AppTooltip } from '@modern/components/ui'
import { dateFormatter, numberFormatter } from '@modern/components/ui/intl-formatters'
import { formatCompactNumber } from '@modern/components/ui/format'
import { chartPoints, formatUsageCost, inputTokens, percentage } from './usage-display'
import type { TrendMetric } from './usage-state'

const props = defineProps<{ report: UsageReport; metric: TrendMetric }>()
const { t, locale } = useI18n()
const host = ref<HTMLElement>()
const width = ref(720)
const hovered = ref<number>()
const focused = ref<number>()
let observer: ResizeObserver | undefined
onMounted(() => {
  observer = new ResizeObserver(([entry]) => {
    if (entry) width.value = Math.max(220, entry.contentRect.width)
  })
  if (host.value) observer.observe(host.value)
})
onBeforeUnmount(() => observer?.disconnect())
watch(
  () => [props.report, props.metric],
  () => {
    hovered.value = undefined
    focused.value = undefined
  },
)
const points = computed(() => {
  const summaries = new Map(props.report.series.map((row) => [row.bucket_start_ms, row]))
  return chartPoints(props.report, props.metric).map((point) => ({
    ...point,
    summary: summaries.get(point.from),
  }))
})
const bars = computed(() => props.metric === 'requests' || props.metric === 'tokens')
const barSeries = computed(() => {
  if (props.metric === 'requests')
    return [
      { id: 'success', value: (row: UsageAggregate) => row.success_count },
      { id: 'failure', value: (row: UsageAggregate) => row.failure_count },
    ]
  if (props.metric === 'tokens')
    return [
      { id: 'input', value: (row: UsageAggregate) => inputTokens(row) },
      { id: 'output', value: (row: UsageAggregate) => row.output_tokens },
    ]
  return []
})
const legend = computed(() =>
  barSeries.value.map((series) => {
    const value =
      props.metric === 'tokens' &&
      !props.report.summary.total_tokens &&
      (props.report.summary.usage_missing_count || props.report.summary.partial_count)
        ? null
        : series.value(props.report.summary)
    return {
      id: series.id,
      label: t('usage.trendSeries.' + series.id),
      value: value === null ? '—' : formatCompactNumber(value, locale.value),
    }
  }),
)
const ceiling = computed(() => {
  if (props.metric === 'cache') return 100
  const peak = Math.max(0, ...points.value.map((point) => point.value ?? 0))
  if (!peak) return props.metric === 'cost' ? 0.04 : 4
  const unit = 10 ** Math.floor(Math.log10(peak))
  const maximum = Math.ceil(peak / unit) * unit
  return props.metric === 'cost' ? Math.max(0.04, maximum) : Math.max(4, Math.ceil(maximum / 4) * 4)
})
const height = 240,
  bottom = 204
const top = computed(() => (bars.value ? (width.value < 340 ? 56 : 32) : 12))
const left = computed(() => {
  const longest = Math.max(
    ...Array.from({ length: 5 }, (_, index) => formatted((ceiling.value * index) / 4).length),
  )
  return Math.max(44, Math.min(108, longest * 7 + 14))
})
const right = computed(() => width.value - 8)
const position = (time: number) =>
  left.value +
  ((right.value - left.value) * (time - props.report.from_ms)) /
    (props.report.to_ms - props.report.from_ms)
const y = (value: number) => bottom - ((bottom - top.value) * value) / ceiling.value
const coordinates = computed(() =>
  points.value.map((point) => {
    const start = position(point.from),
      end = position(point.to)
    let cumulative = 0
    const stack =
      point.value === null
        ? []
        : barSeries.value.map((series) => {
            const value = point.summary ? series.value(point.summary) : 0
            const lower = cumulative
            cumulative += value
            return { id: series.id, value, y: y(cumulative), height: y(lower) - y(cumulative) }
          })
    return {
      ...point,
      stack,
      start,
      end,
      x: (start + end) / 2,
      y: y(point.value ?? 0),
      barWidth: Math.max(1, Math.min(44, (end - start) * 0.72)),
    }
  }),
)
const segments = computed(() => {
  const result: { line: string; area: string }[] = []
  let line = '',
    first = 0,
    last = 0
  function finish(): void {
    if (line) result.push({ line, area: `${line} L${last},${bottom} L${first},${bottom} Z` })
    line = ''
  }
  coordinates.value.forEach((point) => {
    if (point.value === null) {
      finish()
      return
    }
    if (!line) first = point.x
    last = point.x
    line += `${line ? ' L' : 'M'}${point.x},${point.y}`
  })
  finish()
  return result
})
const selected = computed(() => hovered.value ?? focused.value)
const active = computed(() =>
  selected.value === undefined ? undefined : coordinates.value[selected.value],
)
const ticks = computed(() => {
  const count = width.value < 450 ? 3 : 5
  return Array.from({ length: count }, (_, index) => {
    const fraction = index / (count - 1)
    return {
      x: left.value + fraction * (right.value - left.value),
      time: props.report.from_ms + fraction * (props.report.to_ms - props.report.from_ms),
    }
  })
})
function clock(value: number): string {
  return dateFormatter(
    locale.value,
    props.report.to_ms - props.report.from_ms > 2 * 86400000
      ? { month: 'short', day: 'numeric' }
      : { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' },
  ).format(value)
}
function formatted(value: number | null): string {
  if (value === null) return '—'
  if (props.metric === 'cache') return percentage(value, locale.value)
  if (props.metric === 'cost')
    return numberFormatter(locale.value, {
      style: 'currency',
      currency: 'USD',
      currencyDisplay: 'narrowSymbol',
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    }).format(value)
  return formatCompactNumber(value, locale.value)
}
const pointLabels = computed(() => {
  const day = dateFormatter(locale.value, { month: 'short', day: 'numeric' })
  const time = dateFormatter(locale.value, { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
  return points.value.map((point) => {
    const endDay = day.format(point.from) === day.format(point.to) ? '' : day.format(point.to) + ' '
    const metricValue =
      props.metric === 'cost' && point.summary && point.value !== null
        ? formatUsageCost(point.summary.estimated_cost_nano_usd, locale.value)
        : formatted(point.value)
    const lines = [
      `${day.format(point.from)} ${time.format(point.from)} – ${endDay}${time.format(point.to)}`,
      `${t('usage.' + props.metric)} ${metricValue}`,
    ]
    for (const series of barSeries.value) {
      const value = point.value === null ? null : point.summary ? series.value(point.summary) : 0
      lines.push(
        `${t('usage.trendSeries.' + series.id)} ${value === null ? '—' : formatCompactNumber(value, locale.value)}`,
      )
    }
    if (props.metric === 'cache') {
      const row = point.summary
      const missing = row && !row.total_tokens && (row.usage_missing_count || row.partial_count)
      const count = (value: number) => (missing ? '—' : formatCompactNumber(value, locale.value))
      lines.push(
        `${t('usage.parts.cache')} ${count(row?.cache_read_tokens ?? 0)} Token`,
        `${t('usage.parts.write')} ${count(row ? row.cache_write_5m_tokens + row.cache_write_1h_tokens + row.cache_write_unknown_tokens : 0)} Token`,
        `${t('usage.trendSeries.totalInput')} ${count(row ? inputTokens(row) : 0)} Token`,
      )
    }
    if (
      props.metric === 'cost' &&
      point.summary &&
      (point.summary.unpriced_request_count || point.summary.pricing_partial_count)
    ) {
      lines.push(
        t('usage.unpriced', {
          count: formatCompactNumber(point.summary.unpriced_request_count, locale.value),
          partial: formatCompactNumber(point.summary.pricing_partial_count, locale.value),
        }),
      )
    }
    if (
      (props.metric === 'tokens' || props.metric === 'cache') &&
      (point.summary?.usage_missing_count || point.summary?.partial_count)
    )
      lines.push(t('usage.incomplete'))
    return lines.join('\n')
  })
})
const tooltip = computed(() => pointLabels.value[selected.value ?? 0])
function move(event: PointerEvent): void {
  const bounds = (event.currentTarget as HTMLElement).getBoundingClientRect()
  if (!bounds.width || !points.value.length) return
  const fraction = Math.max(0, Math.min(1, (event.clientX - bounds.left) / bounds.width))
  const at = props.report.from_ms + fraction * (props.report.to_ms - props.report.from_ms)
  let low = 0,
    high = points.value.length
  while (low < high) {
    const middle = Math.floor((low + high) / 2)
    if (points.value[middle]!.to <= at) low = middle + 1
    else high = middle
  }
  hovered.value = Math.min(low, points.value.length - 1)
}
function focus(): void {
  focused.value = selected.value ?? 0
}
function navigate(event: KeyboardEvent): void {
  if (event.altKey || event.ctrlKey || event.metaKey) return
  if (event.key === 'Escape') {
    hovered.value = undefined
    focused.value = undefined
    return
  }
  let next = selected.value ?? 0
  if (event.key === 'ArrowLeft') next--
  else if (event.key === 'ArrowRight') next++
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = points.value.length - 1
  else return
  event.preventDefault()
  hovered.value = undefined
  focused.value = Math.max(0, Math.min(points.value.length - 1, next))
}
</script>

<template>
  <div
    ref="host"
    class="modern-usage-chart"
    :data-metric="metric"
    role="group"
    :aria-label="t('usage.chartKeyboard', { metric: t('usage.' + metric) })"
  >
    <div v-if="legend.length" class="modern-usage-chart-legend">
      <span v-for="item in legend" :key="item.id" class="modern-usage-chart-legend-item">
        <i :data-series="item.id" aria-hidden="true" />
        <span>{{ item.label }}</span>
        <strong>{{ item.value }}</strong>
      </span>
    </div>
    <AppSvg :viewBox="`0 0 ${width} ${height}`" aria-hidden="true" focusable="false">
      <g v-for="step in 5" :key="step" class="modern-usage-gridline">
        <line
          :x1="left"
          :x2="right"
          :y1="y((ceiling * (step - 1)) / 4)"
          :y2="y((ceiling * (step - 1)) / 4)"
        />
        <text :x="left - 10" :y="y((ceiling * (step - 1)) / 4) + 4" text-anchor="end">{{
          formatted((ceiling * (step - 1)) / 4)
        }}</text>
      </g>
      <g v-if="bars">
        <g v-for="point in coordinates" :key="point.from">
          <rect
            v-for="part in point.stack.filter((part) => part.value > 0)"
            :key="part.id"
            :x="point.x - point.barWidth / 2"
            :y="part.y"
            :width="point.barWidth"
            :height="part.height"
            :data-series="part.id"
            class="modern-usage-column"
            :class="{ 'is-active': active?.from === point.from }"
          />
        </g>
      </g>
      <g v-else>
        <template v-for="(segment, index) in segments" :key="index">
          <path :d="segment.area" class="modern-usage-area" />
          <path :d="segment.line" class="modern-usage-line" />
        </template>
        <circle
          v-for="point in coordinates.filter((point) => point.value !== null)"
          :key="point.from"
          :cx="point.x"
          :cy="point.y"
          r="2"
          class="modern-usage-dot"
        />
      </g>
      <g v-if="active">
        <line :x1="active.x" :x2="active.x" :y1="top" :y2="bottom" class="modern-usage-crosshair" />
        <circle
          v-if="!bars && active.value !== null"
          :cx="active.x"
          :cy="active.y"
          r="4"
          class="modern-usage-selected-dot"
        />
      </g>
      <text
        v-for="(tick, index) in ticks"
        :key="index"
        :x="tick.x"
        :y="height - 10"
        :text-anchor="index === 0 ? 'start' : index === ticks.length - 1 ? 'end' : 'middle'"
        class="modern-usage-axis"
        >{{ clock(tick.time) }}</text
      >
    </AppSvg>
    <span v-if="!report.summary.request_count || !segments.length" class="modern-usage-chart-empty">
      {{ t(!report.summary.request_count ? 'usage.empty' : 'usage.noMeasuredData') }}
    </span>
    <AppTooltip v-if="coordinates.length" :label="tooltip" side="top">
      <span
        class="modern-usage-chart-hit"
        role="img"
        :aria-label="tooltip"
        tabindex="0"
        :style="{
          left: (left / width) * 100 + '%',
          width: ((right - left) / width) * 100 + '%',
          top: (top / height) * 100 + '%',
          height: ((bottom - top) / height) * 100 + '%',
        }"
        @pointermove="move"
        @pointerleave="hovered = undefined"
        @focus="focus"
        @blur="focused = undefined"
        @keydown="navigate"
      />
    </AppTooltip>
  </div>
</template>

<style scoped>
.modern-usage-chart {
  position: relative;
  min-width: 0;
  color: var(--modern-accent);
}
.modern-usage-chart[data-metric='tokens'] {
  color: var(--modern-chart-input);
}
.modern-usage-chart[data-metric='cache'] {
  color: var(--modern-chart-cache);
}
.modern-usage-chart[data-metric='cost'] {
  color: var(--modern-chart-output);
}
.modern-usage-chart [data-series='success'] {
  color: var(--modern-chart-cache);
}
.modern-usage-chart [data-series='failure'] {
  color: var(--modern-danger);
}
.modern-usage-chart [data-series='input'] {
  color: var(--modern-chart-input);
}
.modern-usage-chart [data-series='output'] {
  color: var(--modern-chart-output);
}
.modern-usage-chart-legend {
  position: absolute;
  top: 0;
  left: 0;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-1) var(--modern-space-5);
  max-width: 100%;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-usage-chart-legend-item {
  display: inline-flex;
  align-items: center;
  gap: var(--modern-space-1-5);
  white-space: nowrap;
}
.modern-usage-chart-legend-item i {
  width: var(--modern-space-2);
  height: var(--modern-space-2);
  border-radius: var(--modern-space-0-5);
  background: currentColor;
}
.modern-usage-chart-legend-item strong {
  color: var(--modern-text);
  font-weight: var(--modern-weight-medium);
  font-variant-numeric: tabular-nums;
}
.modern-usage-chart svg {
  display: block;
  width: 100%;
  height: 240px;
  overflow: visible;
}
.modern-usage-gridline line {
  stroke: color-mix(in srgb, var(--modern-border) 75%, var(--modern-surface));
  stroke-width: var(--modern-line-width);
  stroke-dasharray: 2 5;
}
.modern-usage-gridline text,
.modern-usage-axis {
  fill: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
}
.modern-usage-column {
  fill: color-mix(in srgb, currentColor 65%, var(--modern-surface));
}
.modern-usage-column.is-active {
  fill: currentColor;
}
.modern-usage-line {
  fill: none;
  stroke: currentColor;
  stroke-width: var(--modern-trend-stroke);
  stroke-linejoin: round;
  stroke-linecap: round;
}
.modern-usage-area {
  fill: var(--modern-chart-area-soft-fill);
}
.modern-usage-dot {
  fill: currentColor;
}
.modern-usage-selected-dot {
  fill: currentColor;
  stroke: var(--modern-surface);
  stroke-width: var(--modern-focus-width);
}
.modern-usage-crosshair {
  stroke: var(--modern-tooltip-border);
  stroke-width: var(--modern-line-width);
  stroke-dasharray: 3 4;
}
.modern-usage-chart-hit {
  position: absolute;
  cursor: crosshair;
  outline: none;
}
.modern-usage-chart-hit:focus-visible {
  outline: var(--modern-line-width) dashed var(--modern-tooltip-border);
  outline-offset: 0;
}
.modern-usage-chart-empty {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  pointer-events: none;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
</style>
