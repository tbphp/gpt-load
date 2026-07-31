<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'

import { formatISOInstant, formatInteger, formatLocalTimeRange } from '@/lib/format'

import { buildTrendGeometry, isTrendSeriesUsable, type TrendDatum } from './trend-chart'

const props = withDefaults(
  defineProps<{
    series: readonly TrendDatum[]
    title: string
    description: string
    emptyLabel: string
    requestLabel: string
    failureLabel: string
    rangeStart: number
    rangeEnd: number
    locale?: string
  }>(),
  { locale: 'en-US' },
)

const width = 1000
const chartHeight = 158
const maximumFailureHeight = 46
const descriptionID = `trend-chart-description-${useId()}`
const chartElement = ref<HTMLElement>()
const activePointIndex = ref<number | null>(null)
const selectionAnnouncement = ref('')
let announcementGeneration = 0
const chartSeries = computed(() =>
  isTrendSeriesUsable(props.series, props.rangeStart, props.rangeEnd) ? props.series : [],
)
const seriesKey = computed(
  () =>
    `${props.rangeStart}:${props.rangeEnd}:${chartSeries.value
      .map(
        (datum) =>
          `${datum.bucket_start_ms}:${datum.bucket_end_ms}:${datum.request_count}:${datum.failure_count}`,
      )
      .join('|')}`,
)
const geometry = computed(() =>
  buildTrendGeometry(
    chartSeries.value,
    width,
    chartHeight,
    maximumFailureHeight,
    props.rangeStart,
    props.rangeEnd,
  ),
)
const activePoint = computed(() => {
  const index = activePointIndex.value
  if (index === null) return undefined
  const datum = chartSeries.value[index]
  const point = geometry.value.requestPoints[index]
  return datum && point ? { datum, point } : undefined
})
const tooltipStyle = computed(() => {
  const point = activePoint.value?.point
  if (!point) return undefined
  return {
    left: `${(point.x / width) * 100}%`,
    top: `${(point.y / chartHeight) * 100}%`,
  }
})
const tooltipAlignment = computed(() => {
  const point = activePoint.value?.point
  if (!point) return 'center'
  if (point.x < width * 0.16) return 'start'
  if (point.x > width * 0.84) return 'end'
  return 'center'
})

watch(seriesKey, () => {
  activePointIndex.value = null
  clearAnnouncement()
})

function formatBucketRange(datum: TrendDatum): string {
  return formatLocalTimeRange(datum.bucket_start_ms, datum.bucket_end_ms, props.locale)
}

function tooltipValue(value: number): string {
  return formatInteger(value, props.locale)
}

function pointAnnouncement(index: number): string {
  const datum = chartSeries.value[index]
  if (!datum) return ''
  return `${formatBucketRange(datum)} · ${props.requestLabel} ${tooltipValue(datum.request_count)} · ${props.failureLabel} ${tooltipValue(datum.failure_count)}`
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
  const points = geometry.value.requestPoints
  if (!element || points.length === 0) return null
  const bounds = element.getBoundingClientRect()
  if (bounds.width <= 0) return null
  const x = Math.max(0, Math.min(width, ((event.clientX - bounds.left) / bounds.width) * width))
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

onMounted(() => document.addEventListener('pointerdown', closeOnExternalPointer, true))
onBeforeUnmount(() => document.removeEventListener('pointerdown', closeOnExternalPointer, true))
</script>

<template>
  <p v-if="chartSeries.length === 0" class="trend-chart__empty" role="status">
    {{ emptyLabel }}
  </p>
  <figure
    v-else
    ref="chartElement"
    class="trend-chart"
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
    <span :id="descriptionID" class="trend-chart__visually-hidden">{{ description }}</span>
    <span class="trend-chart__visually-hidden" aria-live="polite" aria-atomic="true">
      {{ selectionAnnouncement }}
    </span>
    <div class="trend-chart__plot-stack">
      <Transition name="trend-chart__data" appear>
        <div :key="seriesKey" class="trend-chart__data-layer">
          <div class="trend-chart__request-frame">
            <svg
              class="trend-chart__graphic"
              :viewBox="`0 0 ${width} ${chartHeight}`"
              preserveAspectRatio="none"
              aria-hidden="true"
            >
              <g class="trend-chart__grid">
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
              <path class="trend-chart__area" :d="geometry.requestAreaPath" />
              <g class="trend-chart__failure-bars">
                <rect
                  v-for="(bar, index) in geometry.failureBars.filter((bar) => bar.value > 0)"
                  :key="`${bar.x}:${index}`"
                  :x="bar.x"
                  :y="bar.y"
                  :width="bar.width"
                  :height="bar.height"
                  rx="1.5"
                />
              </g>
              <path class="trend-chart__line" :d="geometry.requestPath" />
              <Transition name="trend-chart__active">
                <g v-if="activePoint" class="trend-chart__active-markers">
                  <line
                    class="trend-chart__guide"
                    :x1="activePoint.point.x"
                    :y1="geometry.plotTop"
                    :x2="activePoint.point.x"
                    :y2="geometry.baseline"
                  />
                  <circle
                    class="trend-chart__dot"
                    :cx="activePoint.point.x"
                    :cy="activePoint.point.y"
                    r="4.5"
                  />
                </g>
              </Transition>
            </svg>
            <Transition name="trend-chart__active">
              <div
                v-if="activePoint"
                class="trend-chart__tooltip"
                :class="`trend-chart__tooltip--${tooltipAlignment}`"
                :style="tooltipStyle"
                role="tooltip"
              >
                <time :datetime="formatISOInstant(activePoint.datum.bucket_start_ms)">
                  {{ formatBucketRange(activePoint.datum) }}
                </time>
                <span>{{ requestLabel }} {{ tooltipValue(activePoint.datum.request_count) }}</span>
                <span>{{ failureLabel }} {{ tooltipValue(activePoint.datum.failure_count) }}</span>
              </div>
            </Transition>
          </div>
        </div>
      </Transition>
    </div>
  </figure>
</template>

<style scoped>
.trend-chart {
  position: relative;
  display: grid;
  min-width: 0;
  margin: 0;
  cursor: crosshair;
  outline: none;
}

.trend-chart:focus-visible {
  box-shadow: var(--focus-ring);
}

.trend-chart__visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  clip-path: inset(50%);
  white-space: nowrap;
}

.trend-chart__request-frame {
  position: relative;
}

.trend-chart__plot-stack {
  position: relative;
  height: 158px;
}

.trend-chart__data-layer {
  height: 158px;
}

.trend-chart__graphic {
  display: block;
  width: 100%;
  height: 158px;
}

.trend-chart__grid line {
  stroke: var(--color-border-subtle);
  stroke-width: 1;
  stroke-dasharray: 3 4;
  vector-effect: non-scaling-stroke;
}

.trend-chart__area {
  fill: var(--color-action);
  opacity: 0.1;
}

.trend-chart__line {
  fill: none;
  stroke: var(--color-action);
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 2;
  vector-effect: non-scaling-stroke;
}

.trend-chart__guide {
  stroke: var(--color-border-strong);
  stroke-dasharray: 3 3;
  vector-effect: non-scaling-stroke;
}

.trend-chart__dot {
  fill: var(--color-action);
  stroke: var(--color-surface-raised);
  stroke-width: 2;
  vector-effect: non-scaling-stroke;
}

.trend-chart__failure-bars rect {
  fill: var(--color-danger);
  opacity: 0.78;
}

.trend-chart__tooltip {
  position: absolute;
  z-index: 2;
  display: grid;
  min-width: 148px;
  gap: 2px;
  transform: translate(-50%, calc(-100% - 12px));
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-control);
  background: var(--color-surface-raised);
  box-shadow: var(--shadow-card);
  color: var(--color-text-muted);
  padding: var(--space-2) var(--space-3);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  line-height: 1.45;
  pointer-events: none;
  white-space: nowrap;
}

.trend-chart__tooltip time {
  color: var(--color-text);
}

.trend-chart__tooltip--start {
  transform: translate(0, calc(-100% - 12px));
}

.trend-chart__tooltip--end {
  transform: translate(-100%, calc(-100% - 12px));
}

.trend-chart__empty {
  margin: 0;
  color: var(--color-text-muted);
}

@media (prefers-reduced-motion: no-preference) {
  .trend-chart__data-enter-active,
  .trend-chart__data-leave-active {
    transition: opacity var(--duration-data) var(--easing-data);
  }

  .trend-chart__data-leave-active {
    position: absolute;
    inset: 0;
  }

  .trend-chart__data-enter-from,
  .trend-chart__data-leave-to,
  .trend-chart__active-enter-from,
  .trend-chart__active-leave-to {
    opacity: 0;
  }

  .trend-chart__active-enter-active,
  .trend-chart__active-leave-active {
    transition: opacity var(--duration-fast) var(--easing-standard);
  }

  .trend-chart__area,
  .trend-chart__line,
  .trend-chart__failure-bars rect {
    transition: fill var(--duration-data) var(--easing-data);
  }
}
</style>
