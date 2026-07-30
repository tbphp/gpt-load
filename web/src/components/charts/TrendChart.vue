<script setup lang="ts">
import { computed, useId } from 'vue'

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
    rateSuffix?: string
    failureStripLabel?: string
  }>(),
  {
    locale: 'en-US',
    nowLabel: 'Now',
    rateSuffix: '',
    failureStripLabel: '',
  },
)

const width = 1000
const requestHeight = 180
const failureHeight = 28
const titleID = `trend-chart-title-${useId()}`
const descriptionID = `trend-chart-description-${useId()}`
const geometry = computed(() =>
  buildTrendGeometry(
    props.series,
    width,
    requestHeight,
    failureHeight,
    props.rangeStart,
    props.rangeEnd,
  ),
)
const lastBucket = computed(() => props.series.at(-1))
const lastPoint = computed(() => geometry.value.requestMarkers.at(-1))
const timeAxis = computed(() => {
  if (props.series.length === 0) return null
  const start = Date.parse(props.rangeStart)
  const end = Date.parse(props.rangeEnd)
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return null
  const middle = start + (end - start) / 2
  const dateStyle = end - start >= 3 * 24 * 60 * 60 * 1000
  const formatter = new Intl.DateTimeFormat(
    props.locale,
    dateStyle
      ? { month: 'short', day: 'numeric', timeZone: 'UTC' }
      : {
          hour: '2-digit',
          minute: '2-digit',
          hourCycle: 'h23',
          timeZone: 'UTC',
        },
  )
  return {
    start: formatter.format(start),
    startInstant: props.rangeStart,
    middle: formatter.format(middle),
    middleInstant: new Date(middle).toISOString(),
  }
})
const effectiveRateSuffix = computed(() => {
  if (props.rateSuffix.length > 0) return props.rateSuffix
  const last = lastBucket.value
  if (last === undefined) return ''
  const bucketDuration = Date.parse(last.bucket_end) - Date.parse(last.bucket_start)
  return bucketDuration >= 12 * 60 * 60 * 1000 ? '/d' : '/h'
})
const lastRate = computed(() => {
  const last = lastBucket.value
  if (last === undefined) return ''
  return `${new Intl.NumberFormat(props.locale).format(last.request_count)}${effectiveRateSuffix.value}`
})
const lastValueStyle = computed(() => {
  const point = lastPoint.value
  if (point === undefined) return undefined
  return {
    left: `${Math.max(8, Math.min(98, (point.x / width) * 100))}%`,
    top: `${Math.max(12, Math.min(96, (point.y / requestHeight) * 100))}%`,
  }
})
const failureBars = computed(() => {
  const count = Math.max(props.series.length, 1)
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
</script>

<template>
  <p
    v-if="series.length === 0"
    class="trend-chart__empty"
    data-test="trend-chart-empty"
    role="status"
  >
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
        <path
          class="trend-chart__area"
          data-test="trend-request-area"
          :d="geometry.requestAreaPath"
          aria-hidden="true"
        />
        <path
          class="trend-chart__line"
          data-test="trend-request-path"
          :d="geometry.requestPath"
          aria-hidden="true"
        />
        <circle
          v-for="(marker, index) in geometry.requestMarkers"
          :key="`${marker.x}:${marker.y}:${index}`"
          class="trend-chart__marker"
          data-test="trend-request-marker"
          :cx="marker.x"
          :cy="marker.y"
          r="5"
          aria-hidden="true"
        />
      </svg>
      <span
        v-if="lastBucket"
        class="trend-chart__last-value"
        data-test="trend-last-value"
        :style="lastValueStyle"
        :aria-label="`${requestLabel} ${lastRate}`"
      >
        {{ lastRate }}
      </span>
    </div>

    <figcaption v-if="timeAxis" class="trend-chart__axis" data-test="trend-time-axis">
      <time :datetime="timeAxis.startInstant">{{ timeAxis.start }}</time>
      <time :datetime="timeAxis.middleInstant">{{ timeAxis.middle }}</time>
      <span>{{ nowLabel }}</span>
    </figcaption>

    <div class="trend-chart__failure-strip">
      <span class="trend-chart__failure-label" data-test="trend-failure-label">
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
          data-test="trend-failure-bar"
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
  height: 185px;
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
.trend-chart__last-value {
  position: absolute;
  transform: translate(-100%, calc(-100% - var(--space-2)));
  background: var(--color-canvas);
  color: var(--color-text);
  padding: 0 var(--space-1);
  font-weight: 700;
  line-height: 1.2;
  white-space: nowrap;
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
.trend-chart__last-value,
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
