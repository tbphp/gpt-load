<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type {
  QuotaHistoryPoint,
  QuotaHistoryReport,
  QuotaHistoryWindow,
} from '@modern/api/credential-quota-history'
import { AppSvg, AppTooltip } from '@modern/components/ui'
import { dateFormatter } from '@modern/components/ui/intl-formatters'

const props = defineProps<{ report: QuotaHistoryReport }>()
const { t, n, locale, te } = useI18n()
const host = ref<HTMLElement>()
const width = ref(480)
const hoveredAt = ref<number>()
const focused = ref(0)
let observer: ResizeObserver | undefined
onMounted(() => {
  observer = new ResizeObserver(([entry]) => {
    if (entry) width.value = Math.max(220, entry.contentRect.width)
  })
  if (host.value) observer.observe(host.value)
})
onBeforeUnmount(() => observer?.disconnect())
const height = 100,
  left = 8,
  top = 6,
  bottom = 94
const x = (at: number) =>
  left +
  ((at - props.report.from) / (props.report.to - props.report.from)) * (width.value - left - 6)
const y = (used: number) => top + (used / 10_000) * (bottom - top)
function windowLabel(window: QuotaHistoryWindow): string {
  const key = 'credentialCards.quotaLabels.' + window.labelKey
  return window.labelKey && te(key) ? t(key) : window.label
}
const curves = computed(() =>
  props.report.windows
    .filter((window) => window.points.length)
    .map((window, index) => {
      const paths: string[] = []
      let path = ''
      function finish(): void {
        if (path) paths.push(path)
        path = ''
      }
      window.points.forEach((point, pointIndex) => {
        const previous = window.points[pointIndex - 1]
        if (previous && point.observedAt - previous.observedAt > props.report.bucketWidth * 2)
          finish()
        const position = { x: x(point.observedAt), y: y(point.usedBasisPoints) }
        path += `${path ? ' L' : 'M'}${position.x},${position.y}`
      })
      finish()
      return { window, label: windowLabel(window), tone: index % 6, paths }
    }),
)
const times = computed(() =>
  [
    ...new Set(
      curves.value.flatMap((series) => series.window.points.map((point) => point.observedAt)),
    ),
  ].sort((a, b) => a - b),
)
function nearestIndex(values: readonly number[], at: number): number {
  let low = 0,
    high = values.length
  while (low < high) {
    const middle = Math.floor((low + high) / 2)
    if (values[middle]! < at) low = middle + 1
    else high = middle
  }
  if (!low) return 0
  if (low === values.length) return low - 1
  return at - values[low - 1]! <= values[low]! - at ? low - 1 : low
}
function nearestPoint(
  points: readonly QuotaHistoryPoint[],
  at: number,
): QuotaHistoryPoint | undefined {
  let low = 0,
    high = points.length
  while (low < high) {
    const middle = Math.floor((low + high) / 2)
    if (points[middle]!.observedAt < at) low = middle + 1
    else high = middle
  }
  const before = points[low - 1],
    after = points[low]
  const point = !before
    ? after
    : !after
      ? before
      : at - before.observedAt <= after.observedAt - at
        ? before
        : after
  return point && Math.abs(point.observedAt - at) <= props.report.bucketWidth * 2
    ? point
    : undefined
}
const selected = computed(() =>
  hoveredAt.value === undefined
    ? []
    : curves.value.map((series) => ({
        ...series,
        point: nearestPoint(series.window.points, hoveredAt.value!),
      })),
)
const timestamp = (at: number) =>
  dateFormatter(locale.value, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).format(at)
const tooltip = computed(() => {
  if (hoveredAt.value === undefined) return t('ui.date.ranges.7d')
  return [
    timestamp(hoveredAt.value),
    ...selected.value.map((series) => {
      const point = series.point
      return `${series.label}  ${point ? n((10_000 - point.usedBasisPoints) / 100, { maximumFractionDigits: 2 }) + '%' : '—'}${point && point.observedAt !== hoveredAt.value ? ' · ' + timestamp(point.observedAt) : ''}`
    }),
  ].join('\n')
})
function move(event: PointerEvent): void {
  if (!times.value.length) return
  const at =
    props.report.from +
    Math.max(0, Math.min(1, event.offsetX / (width.value - left - 6))) *
      (props.report.to - props.report.from)
  const index = nearestIndex(times.value, at)
  const nearest = times.value[index]!
  hoveredAt.value = Math.abs(nearest - at) <= props.report.bucketWidth * 2 ? nearest : undefined
}
function focus(): void {
  hoveredAt.value = times.value[Math.min(focused.value, times.value.length - 1)]
}
function navigate(event: KeyboardEvent): void {
  if (event.altKey || event.ctrlKey || event.metaKey) return
  let next = focused.value
  if (event.key === 'ArrowLeft') next--
  else if (event.key === 'ArrowRight') next++
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = times.value.length - 1
  else if (event.key === 'Escape') {
    hoveredAt.value = undefined
    return
  } else return
  event.preventDefault()
  focused.value = Math.max(0, Math.min(times.value.length - 1, next))
  focus()
}
</script>
<template>
  <div class="modern-quota-trend">
    <div class="modern-quota-trend-legend">
      <span v-for="series in curves" :key="series.window.key" :data-tone="series.tone"
        ><i aria-hidden="true" />{{ series.label }}</span
      >
    </div>
    <div ref="host" class="modern-quota-trend-plot">
      <AppSvg :viewBox="`0 0 ${width} ${height}`" aria-hidden="true" focusable="false">
        <g v-for="used in [0, 5000, 10000]" :key="used" class="modern-quota-trend-grid">
          <line :x1="left" :x2="width - 6" :y1="y(used)" :y2="y(used)" />
        </g>
        <g v-for="series in curves" :key="series.window.key" :data-tone="series.tone">
          <path
            v-for="(path, index) in series.paths"
            :key="index"
            :d="path"
            class="modern-quota-trend-line"
          />
        </g>
        <line
          v-if="hoveredAt !== undefined"
          :x1="x(hoveredAt)"
          :x2="x(hoveredAt)"
          :y1="top"
          :y2="bottom"
          class="modern-quota-trend-cursor"
        />
      </AppSvg>
      <AppTooltip :label="tooltip" side="top">
        <span
          class="modern-quota-trend-hit"
          role="img"
          :aria-label="t('credentialCards.quotaHistory') + ' · ' + tooltip"
          tabindex="0"
          @pointermove="move"
          @pointerleave="hoveredAt = undefined"
          @focus="focus"
          @blur="hoveredAt = undefined"
          @keydown="navigate"
        />
      </AppTooltip>
    </div>
  </div>
</template>
<style scoped>
.modern-quota-trend {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-quota-trend-legend {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-2) var(--modern-space-3);
  font-size: var(--modern-font-size-small);
}
.modern-quota-trend-legend span {
  display: inline-flex;
  align-items: center;
  gap: var(--modern-space-1);
}
.modern-quota-trend-legend i {
  width: var(--modern-space-1-5);
  height: var(--modern-space-1-5);
  background: currentColor;
  border-radius: var(--modern-radius-round);
}
.modern-quota-trend-plot {
  position: relative;
  min-width: 0;
}
.modern-quota-trend-plot :deep(svg) {
  display: block;
  width: 100%;
  height: 100px;
}
.modern-quota-trend-grid line {
  stroke: var(--modern-border);
  stroke-width: var(--modern-line-width);
}
.modern-quota-trend-line {
  fill: none;
  stroke: currentColor;
  stroke-width: var(--modern-line-width);
}
.modern-quota-trend-cursor {
  stroke: var(--modern-muted);
  stroke-width: var(--modern-line-width);
  stroke-dasharray: 3 3;
}
.modern-quota-trend-hit {
  position: absolute;
  left: 8px;
  right: 6px;
  top: 6px;
  bottom: 6px;
}
.modern-quota-trend-hit:focus-visible {
  outline: var(--modern-focus-width) solid var(--modern-accent);
  outline-offset: var(--modern-focus-offset);
}
[data-tone='0'] {
  color: var(--modern-accent);
}
[data-tone='1'] {
  color: var(--modern-chart-input);
}
[data-tone='2'] {
  color: var(--modern-chart-cache);
}
[data-tone='3'] {
  color: var(--modern-chart-output);
}
[data-tone='4'] {
  color: var(--modern-chart-write);
}
[data-tone='5'] {
  color: var(--modern-muted);
}
</style>
