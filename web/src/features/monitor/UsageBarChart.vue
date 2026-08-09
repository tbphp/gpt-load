<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'

import { formatISOInstant, formatInteger, formatLocalTimeRange } from '@/lib/format'

import {
  buildUsageBarGeometry,
  completeUsageBarSeries,
  isUsageBarSeriesUsable,
  type UsageBarDatum,
} from './usage-bar-chart'

const props = withDefaults(
  defineProps<{
    series: readonly UsageBarDatum[]
    title: string
    description: string
    emptyLabel: string
    primaryLabel: string
    secondaryLabel?: string
    primaryZeroDisplay?: string
    secondaryZeroDisplay?: string
    detailZeroDisplay?: string
    rangeStart: number
    rangeEnd: number
    locale?: string
    grouped?: boolean
  }>(),
  {
    locale: 'en-US',
    grouped: false,
    secondaryLabel: undefined,
    primaryZeroDisplay: undefined,
    secondaryZeroDisplay: undefined,
    detailZeroDisplay: undefined,
  },
)

const chartHeight = 158
const compactChart = ref(false)
const width = computed(() => (compactChart.value ? chartHeight * 2 : 1000))
const descriptionID = `usage-bar-chart-description-${useId()}`
const chartElement = ref<HTMLElement>()
const activePointIndex = ref<number | null>(null)
const selectionAnnouncement = ref('')
let announcementGeneration = 0
let compactMedia: MediaQueryList | undefined
const chartSeries = computed(() => {
  if (!isUsageBarSeriesUsable(props.series, props.rangeStart, props.rangeEnd)) return []
  const completed = completeUsageBarSeries(props.series, props.rangeStart, props.rangeEnd)
  const detailTemplate = props.series.find((datum) => datum.details?.length)?.details
  if (!detailTemplate) return completed
  return completed.map((datum) =>
    datum.details
      ? datum
      : {
          ...datum,
          details: detailTemplate.map((detail) => ({
            label: detail.label,
            display: props.detailZeroDisplay ?? formatInteger(0, props.locale),
          })),
        },
  )
})
const seriesKey = computed(
  () =>
    `${props.locale}:${props.primaryLabel}:${props.secondaryLabel ?? ''}:${props.grouped}:${props.primaryZeroDisplay ?? ''}:${props.secondaryZeroDisplay ?? ''}:${props.detailZeroDisplay ?? ''}:${props.rangeStart}:${props.rangeEnd}:${chartSeries.value
      .map(
        (datum) =>
          `${datum.bucket_start_ms}:${datum.bucket_end_ms}:${datum.primary_value}:${datum.secondary_value}:${datum.primary_display ?? ''}:${datum.secondary_display ?? ''}:${datum.details?.map((detail) => `${detail.label}:${detail.display}`).join(',') ?? ''}`,
      )
      .join('|')}`,
)
const geometry = computed(() =>
  buildUsageBarGeometry(
    chartSeries.value,
    width.value,
    chartHeight,
    props.rangeStart,
    props.rangeEnd,
    props.grouped,
  ),
)
const activePoint = computed(() => {
  const index = activePointIndex.value
  if (index === null) return undefined
  const datum = chartSeries.value[index]
  const point = geometry.value.points[index]
  return datum && point ? { datum, point } : undefined
})
const tooltipStyle = computed(() => {
  const point = activePoint.value?.point
  if (!point) return undefined
  const left =
    point.x < width.value * 0.16
      ? '2px'
      : point.x > width.value * 0.84
        ? 'calc(100% - 2px)'
        : `${(point.x / width.value) * 100}%`
  return {
    left,
    top: `max(82px, calc(${(point.y / chartHeight) * 100}% - 12px))`,
  }
})
const tooltipAlignment = computed(() => {
  const point = activePoint.value?.point
  if (!point) return 'center'
  if (point.x < width.value * 0.16) return 'start'
  if (point.x > width.value * 0.84) return 'end'
  return 'center'
})

watch(seriesKey, () => {
  activePointIndex.value = null
  clearAnnouncement()
})

function formatBucketTime(datum: UsageBarDatum): string {
  return formatLocalTimeRange(datum.bucket_start_ms, datum.bucket_end_ms, props.locale)
}

function primaryTooltipValue(datum: UsageBarDatum): string {
  return (
    datum.primary_display ??
    (datum.primary_value === 0 ? props.primaryZeroDisplay : undefined) ??
    formatInteger(datum.primary_value, props.locale)
  )
}

function secondaryTooltipValue(datum: UsageBarDatum): string {
  return (
    datum.secondary_display ??
    (datum.secondary_value === 0 ? props.secondaryZeroDisplay : undefined) ??
    formatInteger(datum.secondary_value, props.locale)
  )
}

function pointAnnouncement(index: number): string {
  const datum = chartSeries.value[index]
  if (!datum) return ''
  const primary = `${formatBucketTime(datum)} · ${props.primaryLabel} ${primaryTooltipValue(datum)}`
  const values =
    props.grouped && props.secondaryLabel
      ? `${primary} · ${props.secondaryLabel} ${secondaryTooltipValue(datum)}`
      : primary
  return (datum.details ?? []).reduce(
    (announcement, detail) => `${announcement} · ${detail.label} ${detail.display}`,
    values,
  )
}

function clearAnnouncement(): void {
  announcementGeneration += 1
  selectionAnnouncement.value = ''
}

function announcePoint(index: number): void {
  const message = pointAnnouncement(index)
  const generation = ++announcementGeneration
  selectionAnnouncement.value = ''
  void nextTick(() => {
    if (generation === announcementGeneration) selectionAnnouncement.value = message
  })
}

function selectPoint(index: number | null, announce = false): void {
  activePointIndex.value = index
  if (index === null) clearAnnouncement()
  else if (announce) announcePoint(index)
}

function nearestPointIndex(event: PointerEvent | MouseEvent): number | null {
  const element = chartElement.value
  const points = geometry.value.points
  if (!element || points.length === 0) return null
  const bounds = element.getBoundingClientRect()
  if (bounds.width <= 0) return null
  const x = Math.max(
    0,
    Math.min(width.value, ((event.clientX - bounds.left) / bounds.width) * width.value),
  )
  return points.reduce(
    (nearest, point, index) =>
      Math.abs(point.x - x) < Math.abs(points[nearest]!.x - x) ? index : nearest,
    0,
  )
}

function selectNearest(event: PointerEvent | MouseEvent, announce = false): void {
  const index = nearestPointIndex(event)
  selectPoint(index, announce && index !== null)
}

function onPointerMove(event: PointerEvent): void {
  if (event.pointerType !== 'touch') selectNearest(event)
}

function onCommitSelection(event: MouseEvent): void {
  selectNearest(event, true)
}

function onPointerLeave(event: PointerEvent): void {
  if (event.pointerType !== 'touch') selectPoint(null)
}

function onKeydown(event: KeyboardEvent): void {
  const lastIndex = chartSeries.value.length - 1
  if (lastIndex < 0) return
  if (event.key === 'Escape') {
    selectPoint(null)
    return
  }
  let next: number | null = null
  if (event.key === 'ArrowLeft') next = Math.max(0, (activePointIndex.value ?? 0) - 1)
  if (event.key === 'ArrowRight') next = Math.min(lastIndex, (activePointIndex.value ?? -1) + 1)
  if (event.key === 'Home') next = 0
  if (event.key === 'End') next = lastIndex
  if (next !== null) {
    event.preventDefault()
    selectPoint(next, true)
  }
}

function closeOnExternalPointer(event: PointerEvent): void {
  const target = event.target
  if (target instanceof Node && !chartElement.value?.contains(target)) selectPoint(null)
}

function closeOnFocusLeave(event: FocusEvent): void {
  const next = event.relatedTarget
  if (next instanceof Node && chartElement.value?.contains(next)) return
  selectPoint(null)
}

function syncCompactChart(event: MediaQueryListEvent | MediaQueryList): void {
  compactChart.value = event.matches
}

onMounted(() => {
  compactMedia = window.matchMedia('(max-width: 620px)')
  syncCompactChart(compactMedia)
  compactMedia.addEventListener('change', syncCompactChart)
  document.addEventListener('pointerdown', closeOnExternalPointer, true)
})
onBeforeUnmount(() => {
  compactMedia?.removeEventListener('change', syncCompactChart)
  document.removeEventListener('pointerdown', closeOnExternalPointer, true)
})
</script>

<template>
  <figure
    ref="chartElement"
    class="usage-bar-chart"
    tabindex="0"
    role="group"
    :aria-label="title"
    :aria-describedby="descriptionID"
    @click="onCommitSelection"
    @focusout="closeOnFocusLeave"
    @keydown="onKeydown"
    @pointerleave="onPointerLeave"
    @pointermove="onPointerMove"
  >
    <span :id="descriptionID" class="usage-bar-chart__visually-hidden">
      {{ chartSeries.length === 0 ? emptyLabel : description }}
    </span>
    <span class="usage-bar-chart__visually-hidden" aria-live="polite" aria-atomic="true">
      {{ selectionAnnouncement }}
    </span>
    <div class="usage-bar-chart__plot-stack">
      <Transition name="usage-bar-chart__data" appear>
        <div :key="seriesKey" class="usage-bar-chart__data-layer">
          <div class="usage-bar-chart__frame">
            <svg
              class="usage-bar-chart__graphic"
              :viewBox="`0 0 ${width} ${chartHeight}`"
              aria-hidden="true"
            >
              <g class="usage-bar-chart__grid">
                <line
                  v-for="lineY in [
                    geometry.plotTop,
                    (geometry.plotTop + geometry.baseline) / 2,
                    geometry.baseline,
                  ]"
                  :key="lineY"
                  x1="0"
                  :y1="lineY"
                  :x2="width"
                  :y2="lineY"
                />
              </g>
              <g class="usage-bar-chart__primary-bars">
                <rect
                  v-for="(bar, index) in geometry.primaryBars.filter((bar) => bar.value > 0)"
                  :key="`${bar.x}:${index}`"
                  :x="bar.x"
                  :y="bar.y"
                  :width="bar.width"
                  :height="bar.height"
                  rx="1.5"
                />
              </g>
              <g v-if="grouped" class="usage-bar-chart__secondary-bars">
                <rect
                  v-for="(bar, index) in geometry.secondaryBars.filter((bar) => bar.value > 0)"
                  :key="`${bar.x}:${index}`"
                  :x="bar.x"
                  :y="bar.y"
                  :width="bar.width"
                  :height="bar.height"
                  rx="1.5"
                />
              </g>
              <Transition name="usage-bar-chart__active">
                <g v-if="activePoint" class="usage-bar-chart__active-markers">
                  <line
                    class="usage-bar-chart__guide"
                    :x1="activePoint.point.x"
                    :y1="geometry.plotTop"
                    :x2="activePoint.point.x"
                    :y2="geometry.baseline"
                  />
                </g>
              </Transition>
            </svg>
            <Transition name="usage-bar-chart__active">
              <div
                v-if="activePoint"
                class="usage-bar-chart__tooltip"
                :class="`usage-bar-chart__tooltip--${tooltipAlignment}`"
                :style="tooltipStyle"
                role="tooltip"
              >
                <time
                  class="usage-bar-chart__tooltip-time"
                  :datetime="formatISOInstant(activePoint.datum.bucket_start_ms)"
                >
                  {{ formatBucketTime(activePoint.datum) }}
                </time>
                <span class="usage-bar-chart__tooltip-row">
                  <span class="usage-bar-chart__tooltip-key">
                    <i
                      class="usage-bar-chart__tooltip-swatch usage-bar-chart__tooltip-swatch--primary"
                    ></i>
                    {{ primaryLabel }}
                  </span>
                  <strong class="usage-bar-chart__tooltip-value">
                    {{ primaryTooltipValue(activePoint.datum) }}
                  </strong>
                </span>
                <span v-if="grouped && secondaryLabel" class="usage-bar-chart__tooltip-row">
                  <span class="usage-bar-chart__tooltip-key">
                    <i
                      class="usage-bar-chart__tooltip-swatch usage-bar-chart__tooltip-swatch--secondary"
                    ></i>
                    {{ secondaryLabel }}
                  </span>
                  <strong class="usage-bar-chart__tooltip-value">
                    {{ secondaryTooltipValue(activePoint.datum) }}
                  </strong>
                </span>
                <span
                  v-for="detail in activePoint.datum.details ?? []"
                  :key="detail.label"
                  class="usage-bar-chart__tooltip-row"
                >
                  <span class="usage-bar-chart__tooltip-key usage-bar-chart__tooltip-key--detail">
                    {{ detail.label }}
                  </span>
                  <strong class="usage-bar-chart__tooltip-value">
                    {{ detail.display }}
                  </strong>
                </span>
              </div>
            </Transition>
          </div>
        </div>
      </Transition>
    </div>
  </figure>
</template>

<style scoped>
.usage-bar-chart {
  position: relative;
  display: grid;
  min-width: 0;
  margin: 0;
  cursor: crosshair;
}

.usage-bar-chart__visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  clip-path: inset(50%);
  white-space: nowrap;
}

.usage-bar-chart__frame {
  position: relative;
  height: 100%;
}

.usage-bar-chart__plot-stack {
  position: relative;
  aspect-ratio: var(--chart-aspect-ratio);
}

.usage-bar-chart__data-layer {
  height: 100%;
}

.usage-bar-chart__graphic {
  display: block;
  width: 100%;
  height: 100%;
}

.usage-bar-chart__grid line {
  stroke: var(--color-border-subtle);
  stroke-width: 1;
  stroke-dasharray: var(--chart-grid-dash);
}

.usage-bar-chart__primary-bars rect {
  fill: var(--color-action);
  opacity: 0.78;
}

.usage-bar-chart__secondary-bars rect {
  fill: var(--color-success);
  opacity: 0.78;
}

.usage-bar-chart__guide {
  stroke: var(--color-border-strong);
}

.usage-bar-chart__tooltip {
  position: absolute;
  z-index: 2;
  min-width: 134px;
  transform: translate(-50%, -100%);
  border: 1px solid var(--color-border-strong);
  border-radius: 8px;
  background: var(--color-surface-raised);
  box-shadow: var(--shadow-chart-tooltip);
  padding: 8px 11px;
  pointer-events: none;
  white-space: nowrap;
}

.usage-bar-chart__tooltip-time {
  display: block;
  margin-bottom: 5px;
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: 11px;
}

.usage-bar-chart__tooltip-row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 14px;
  font-size: 12px;
}

.usage-bar-chart__tooltip-row + .usage-bar-chart__tooltip-row {
  margin-top: 2px;
}

.usage-bar-chart__tooltip-key {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--color-text-muted);
}

.usage-bar-chart__tooltip-key--detail {
  padding-left: 13px;
}

.usage-bar-chart__tooltip-swatch {
  display: block;
  width: 7px;
  height: 7px;
  flex: none;
  border-radius: 2px;
}

.usage-bar-chart__tooltip-swatch--primary {
  background: var(--color-action);
}

.usage-bar-chart__tooltip-swatch--secondary {
  background: var(--color-success);
}

.usage-bar-chart__tooltip-value {
  color: var(--color-text);
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}

.usage-bar-chart__tooltip--start {
  transform: translate(0, -100%);
}

.usage-bar-chart__tooltip--end {
  transform: translate(-100%, -100%);
}

@media (max-width: 620px) {
  .usage-bar-chart__plot-stack {
    min-height: 120px;
    aspect-ratio: 2 / 1;
  }
}

@media (prefers-reduced-motion: no-preference) {
  .usage-bar-chart__data-enter-active,
  .usage-bar-chart__data-leave-active {
    transition: opacity var(--duration-data) var(--easing-data);
  }

  .usage-bar-chart__data-leave-active {
    position: absolute;
    inset: 0;
  }

  .usage-bar-chart__data-enter-from,
  .usage-bar-chart__data-leave-to,
  .usage-bar-chart__active-enter-from,
  .usage-bar-chart__active-leave-to {
    opacity: 0;
  }

  .usage-bar-chart__active-enter-active,
  .usage-bar-chart__active-leave-active {
    transition: opacity var(--duration-fast) var(--easing-standard);
  }

  .usage-bar-chart__primary-bars rect,
  .usage-bar-chart__secondary-bars rect {
    transition: fill var(--duration-data) var(--easing-data);
  }
}
</style>
