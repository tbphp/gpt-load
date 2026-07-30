<script setup lang="ts">
import { computed, ref, useId } from 'vue'

import { buildTrendGeometry, type TrendDatum } from './trend-chart'

const props = withDefaults(
  defineProps<{
    series: readonly TrendDatum[]
    title: string
    description: string
    emptyLabel: string
    requestLabel: string
    failureLabel: string
    rangeStart: string
    rangeEnd: string
    locale?: string
    nowLabel?: string
    failureStripLabel?: string
  }>(),
  {
    locale: 'en-US',
    nowLabel: 'Now',
    failureStripLabel: '',
  },
)

const width = 1000
const requestHeight = 220
const failureHeight = 28
const titleID = `trend-chart-title-${useId()}`
const descriptionID = `trend-chart-description-${useId()}`
const activePointIndex = ref<number | null>(null)
const chartSeries = computed<TrendDatum[]>(() => {
  if (props.series.length === 0) return []
  const rangeStart = Date.parse(props.rangeStart)
  const rangeEnd = Date.parse(props.rangeEnd)
  const firstStart = Date.parse(props.series[0]!.bucket_start)
  const firstEnd = Date.parse(props.series[0]!.bucket_end)
  const bucketDuration = firstEnd - firstStart
  if (
    !Number.isFinite(rangeStart) ||
    !Number.isFinite(rangeEnd) ||
    !Number.isFinite(bucketDuration) ||
    bucketDuration <= 0
  ) {
    return [...props.series]
  }
  const buckets = new Map(props.series.map((datum) => [Date.parse(datum.bucket_start), datum]))
  const result: TrendDatum[] = []
  for (let start = rangeStart; start < rangeEnd; start += bucketDuration) {
    result.push(
      buckets.get(start) ?? {
        bucket_start: new Date(start).toISOString(),
        bucket_end: new Date(start + bucketDuration).toISOString(),
        request_count: 0,
        failure_count: 0,
      },
    )
  }
  return result
})
const geometry = computed(() =>
  buildTrendGeometry(
    chartSeries.value,
    width,
    requestHeight,
    failureHeight,
    props.rangeStart,
    props.rangeEnd,
  ),
)
const lastPoint = computed(() => geometry.value.requestPoints.at(-1))
const timeAxis = computed(() => {
  if (props.series.length === 0) return null
  const start = Date.parse(props.rangeStart)
  const end = Date.parse(props.rangeEnd)
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return null
  const middle = start + (end - start) / 2
  const hourly = end - start <= 25 * 60 * 60 * 1000
  if (hourly) {
    return {
      start: '00:00',
      startInstant: props.rangeStart,
      middle: '12:00',
      middleInstant: new Date(middle).toISOString(),
    }
  }
  const formatter = new Intl.DateTimeFormat(props.locale, {
    month: 'short',
    day: 'numeric',
    timeZone: 'UTC',
  })
  return {
    start: formatter.format(start),
    startInstant: props.rangeStart,
    middle: formatter.format(middle),
    middleInstant: new Date(middle).toISOString(),
  }
})
const failureBars = computed(() => {
  const count = Math.max(chartSeries.value.length, 1)
  const barWidth = Math.max(3, Math.min(10, width / count / 3))
  return geometry.value.failures
    .filter((failure) => failure.value > 0)
    .map((failure) => ({
      ...failure,
      width: barWidth,
      x: Math.max(0, Math.min(width - barWidth, failure.x - barWidth / 2)),
      y: failureHeight - failure.height,
    }))
})
const activePoint = computed(() => {
  const index = activePointIndex.value
  if (index === null) return undefined
  const datum = chartSeries.value[index]
  const point = geometry.value.requestPoints[index]
  if (datum === undefined || point === undefined) return undefined
  return { datum, point, index }
})
const tooltipStyle = computed(() => {
  const active = activePoint.value
  if (active === undefined) return undefined
  return {
    left: `${(active.point.x / width) * 100}%`,
    top: `${(active.point.y / requestHeight) * 100}%`,
  }
})
const tooltipAlignment = computed(() => {
  const point = activePoint.value?.point
  if (point === undefined) return 'center'
  if (point.x < width * 0.12) return 'start'
  if (point.x > width * 0.88) return 'end'
  return 'center'
})

function formatBucketTime(datum: TrendDatum): string {
  const options: Intl.DateTimeFormatOptions =
    Date.parse(props.rangeEnd) - Date.parse(props.rangeStart) > 25 * 60 * 60 * 1000
      ? { month: 'short', day: 'numeric', timeZone: 'UTC' }
      : {
          hour: '2-digit',
          minute: '2-digit',
          hourCycle: 'h23',
          timeZone: 'UTC',
        }
  return new Intl.DateTimeFormat(props.locale, options).format(new Date(datum.bucket_start))
}

function pointLabel(datum: TrendDatum | undefined): string {
  if (datum === undefined) return ''
  const number = new Intl.NumberFormat(props.locale)
  return `${formatBucketTime(datum)} · ${props.requestLabel} ${number.format(datum.request_count)} · ${props.failureLabel} ${number.format(datum.failure_count)}`
}
</script>

<template>
  <p v-if="series.length === 0" class="trend-chart__empty" role="status">
    {{ emptyLabel }}
  </p>
  <figure v-else class="trend-chart">
    <div class="trend-chart__request-frame">
      <svg
        class="trend-chart__request-graphic"
        role="img"
        :aria-labelledby="`${titleID} ${descriptionID}`"
        :viewBox="`0 0 ${width} ${requestHeight}`"
        preserveAspectRatio="none"
      >
        <title :id="titleID">{{ title }}</title>
        <desc :id="descriptionID">{{ description }}</desc>
        <g class="trend-chart__grid" aria-hidden="true">
          <line
            v-for="lineY in [0, requestHeight / 2, requestHeight]"
            :key="lineY"
            x1="0"
            :y1="lineY"
            :x2="width"
            :y2="lineY"
          />
        </g>
        <path class="trend-chart__area" :d="geometry.requestAreaPath" aria-hidden="true" />
        <path class="trend-chart__line" :d="geometry.requestPath" aria-hidden="true" />
        <circle
          v-if="lastPoint"
          class="trend-chart__marker"
          :cx="lastPoint.x"
          :cy="lastPoint.y"
          r="4"
          aria-hidden="true"
        />
      </svg>
      <button
        v-for="(point, index) in geometry.requestPoints"
        :key="`${point.x}:${point.y}:${index}`"
        class="trend-chart__hit-target"
        type="button"
        :style="{
          left: `${(point.x / width) * 100}%`,
          top: `${(point.y / requestHeight) * 100}%`,
        }"
        :aria-label="pointLabel(chartSeries[index])"
        @pointerenter="activePointIndex = index"
        @pointerleave="activePointIndex = null"
        @focus="activePointIndex = index"
        @blur="activePointIndex = null"
      ></button>
      <div
        v-if="activePoint"
        class="trend-chart__tooltip"
        :class="`trend-chart__tooltip--${tooltipAlignment}`"
        :style="tooltipStyle"
        role="tooltip"
      >
        <time :datetime="activePoint.datum.bucket_start">{{
          formatBucketTime(activePoint.datum)
        }}</time>
        <span>{{ requestLabel }} {{ activePoint.datum.request_count }}</span>
        <span>{{ failureLabel }} {{ activePoint.datum.failure_count }}</span>
      </div>
    </div>

    <figcaption v-if="timeAxis" class="trend-chart__axis">
      <time :datetime="timeAxis.startInstant">{{ timeAxis.start }}</time>
      <time :datetime="timeAxis.middleInstant">{{ timeAxis.middle }}</time>
      <span>{{ nowLabel }}</span>
    </figcaption>

    <div class="trend-chart__failure-strip">
      <span class="trend-chart__failure-label">
        {{ failureStripLabel || failureLabel }}
      </span>
      <svg
        class="trend-chart__failure-graphic"
        :viewBox="`0 0 ${width} ${failureHeight}`"
        preserveAspectRatio="none"
        aria-hidden="true"
      >
        <rect
          v-for="(bar, index) in failureBars"
          :key="`${bar.x}:${index}`"
          :x="bar.x"
          :y="bar.y"
          :width="bar.width"
          :height="bar.height"
          rx="1"
        />
      </svg>
    </div>
  </figure>
</template>

<style scoped>
.trend-chart {
  display: grid;
  min-width: 0;
  margin: 0;
  padding-bottom: var(--space-2);
}
.trend-chart__request-frame {
  position: relative;
}
.trend-chart__request-graphic,
.trend-chart__failure-graphic {
  display: block;
  width: 100%;
}
.trend-chart__request-graphic {
  height: 220px;
}
.trend-chart__grid line {
  stroke: var(--color-border-subtle);
  stroke-width: 1;
  vector-effect: non-scaling-stroke;
}
.trend-chart__area {
  fill: color-mix(in srgb, var(--color-action) 12%, var(--color-canvas));
}
.trend-chart__line {
  fill: none;
  stroke: var(--color-action);
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 2.5;
  vector-effect: non-scaling-stroke;
}
.trend-chart__marker {
  fill: var(--color-action);
  stroke: var(--color-surface);
  stroke-width: 2;
  vector-effect: non-scaling-stroke;
}
.trend-chart__hit-target {
  position: absolute;
  width: 24px;
  height: 24px;
  transform: translate(-50%, -50%);
  border: 0;
  border-radius: 50%;
  background: transparent;
  padding: 0;
  cursor: crosshair;
}
.trend-chart__hit-target::after {
  position: absolute;
  top: 50%;
  left: 50%;
  width: 8px;
  height: 8px;
  transform: translate(-50%, -50%);
  border: 2px solid var(--color-surface);
  border-radius: 50%;
  background: var(--color-action);
  content: '';
  opacity: 0;
}
.trend-chart__hit-target:hover::after,
.trend-chart__hit-target:focus-visible::after {
  opacity: 1;
}
.trend-chart__tooltip {
  position: absolute;
  z-index: 2;
  display: grid;
  min-width: 132px;
  gap: 2px;
  transform: translate(-50%, calc(-100% - 12px));
  border: 1px solid var(--color-border-subtle);
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
.trend-chart__axis {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  margin-top: var(--space-4);
}
.trend-chart__axis > :nth-child(2) {
  text-align: center;
}
.trend-chart__axis > :last-child {
  text-align: right;
}
.trend-chart__failure-strip {
  display: grid;
  gap: calc(var(--space-1) / 2);
  margin-top: var(--space-1);
}
.trend-chart__failure-label {
  justify-self: start;
}
.trend-chart__axis,
.trend-chart__failure-label {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
.trend-chart__axis,
.trend-chart__failure-label {
  color: var(--color-text-faint);
}
.trend-chart__failure-graphic {
  height: 28px;
}
.trend-chart__failure-graphic rect {
  fill: var(--color-danger);
}
.trend-chart__empty {
  margin: 0;
  color: var(--color-text-muted);
}
</style>
