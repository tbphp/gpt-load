<script setup lang="ts">
import { computed, useId } from 'vue'

import { buildTrendGeometry, type TrendDatum } from './trend-chart'

const props = defineProps<{
  series: readonly TrendDatum[]
  title: string
  description: string
  emptyLabel: string
  requestLabel: string
  failureLabel: string
}>()

const width = 1000
const requestHeight = 220
const chartGap = 28
const failureHeight = 40
const viewHeight = requestHeight + chartGap + failureHeight
const titleID = `trend-chart-title-${useId()}`
const descriptionID = `trend-chart-description-${useId()}`
const geometry = computed(() =>
  buildTrendGeometry(props.series, width, requestHeight, failureHeight),
)
const lastBucket = computed(() => props.series.at(-1))
const failureBars = computed(() => {
  const count = Math.max(props.series.length, 1)
  const barWidth = Math.max(3, Math.min(10, width / count / 3))
  return geometry.value.failures
    .filter((failure) => failure.value > 0)
    .map((failure) => ({
      ...failure,
      width: barWidth,
      x: Math.max(0, Math.min(width - barWidth, failure.x - barWidth / 2)),
      y: requestHeight + chartGap + failureHeight - failure.height,
    }))
})
const singleMarker = computed(() => {
  if (props.series.length !== 1) return null
  const match = /^M ([\d.]+) ([\d.]+)$/.exec(geometry.value.requestPath)
  if (match === null) return null
  return { x: Number(match[1]), y: Number(match[2]) }
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
    <svg
      class="trend-chart__graphic"
      role="img"
      :aria-labelledby="`${titleID} ${descriptionID}`"
      :viewBox="`0 0 ${width} ${viewHeight}`"
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
        v-if="singleMarker"
        class="trend-chart__marker"
        data-test="trend-request-marker"
        :cx="singleMarker.x"
        :cy="singleMarker.y"
        r="5"
        aria-hidden="true"
      />
      <g class="trend-chart__failures" aria-hidden="true">
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
      </g>
    </svg>
    <figcaption v-if="lastBucket" class="trend-chart__caption" data-test="trend-last-bucket">
      <time :datetime="lastBucket.bucket_end">{{ lastBucket.bucket_end }}</time>
      <span
        >{{ requestLabel }} <strong>{{ lastBucket.request_count }}</strong></span
      >
      <span class="trend-chart__failure-caption">
        {{ failureLabel }} <strong>{{ lastBucket.failure_count }}</strong>
      </span>
    </figcaption>
  </figure>
</template>

<style scoped>
.trend-chart {
  display: grid;
  min-width: 0;
  gap: var(--space-3);
  margin: 0;
}
.trend-chart__graphic {
  display: block;
  width: 100%;
  min-height: 220px;
  max-height: 320px;
}
.trend-chart__grid line {
  stroke: var(--color-border-subtle);
  stroke-width: 1;
  vector-effect: non-scaling-stroke;
}
.trend-chart__area {
  fill: var(--color-action-soft);
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
.trend-chart__failures rect {
  fill: var(--color-danger);
}
.trend-chart__caption {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--space-3);
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
.trend-chart__caption time {
  margin-right: auto;
  overflow-wrap: anywhere;
}
.trend-chart__caption strong {
  color: var(--color-text);
}
.trend-chart__failure-caption {
  color: var(--color-danger);
}
.trend-chart__empty {
  margin: 0;
  color: var(--color-text-muted);
}
@media (max-width: 640px) {
  .trend-chart__graphic {
    min-height: 180px;
  }
  .trend-chart__caption {
    align-items: flex-start;
    flex-direction: column;
  }
}
</style>
