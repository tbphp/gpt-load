<script setup lang="ts">
import { computed, useId } from 'vue'

import type { UsageReportDto } from '@/api/control/usage'

type UsageSeriesPoint = Pick<
  UsageReportDto['series'][number],
  'bucket_start' | 'bucket_end' | 'request_count'
>

interface SparklinePoint {
  x: number
  y: number
  bucketStart: number
}

const props = defineProps<{
  range: UsageReportDto['range']
  series: UsageSeriesPoint[]
  title: string
  description: string
  emptyLabel: string
}>()

const viewBoxWidth = 240
const viewBoxHeight = 64
const padding = 4
const titleID = `usage-sparkline-title-${useId()}`
const descriptionID = `usage-sparkline-description-${useId()}`

const points = computed<SparklinePoint[]>(() => {
  const from = Date.parse(props.range.from)
  const to = Date.parse(props.range.to)
  const duration = to - from
  if (!Number.isFinite(from) || !Number.isFinite(to) || duration <= 0) return []

  const validSeries = props.series
    .map((point) => ({
      bucketStart: Date.parse(point.bucket_start),
      requestCount: point.request_count,
    }))
    .filter(
      (point) =>
        Number.isFinite(point.bucketStart) &&
        Number.isSafeInteger(point.requestCount) &&
        point.requestCount >= 0 &&
        point.bucketStart >= from &&
        point.bucketStart < to,
    )
  const maximum = Math.max(0, ...validSeries.map((point) => point.requestCount))
  const innerWidth = viewBoxWidth - padding * 2
  const innerHeight = viewBoxHeight - padding * 2

  return validSeries.map((point) => ({
    bucketStart: point.bucketStart,
    x: round(padding + ((point.bucketStart - from) / duration) * innerWidth),
    y: round(
      maximum === 0
        ? viewBoxHeight - padding
        : viewBoxHeight - padding - (point.requestCount / maximum) * innerHeight,
    ),
  }))
})

const path = computed(() => {
  if (points.value.length < 2) return ''

  const interval = props.range.granularity === 'hour' ? 60 * 60 * 1000 : 24 * 60 * 60 * 1000
  return points.value.reduce((result, point, index) => {
    const previous = points.value[index - 1]
    if (previous === undefined || point.bucketStart !== previous.bucketStart + interval) {
      return `${result}${result ? ' ' : ''}M ${point.x} ${point.y} L ${point.x} ${point.y}`
    }
    return `${result} L ${point.x} ${point.y}`
  }, '')
})

function round(value: number): number {
  return Math.round(value * 100) / 100
}
</script>

<template>
  <p
    v-if="points.length === 0"
    class="usage-sparkline__empty"
    data-test="usage-sparkline-empty"
    role="status"
  >
    {{ emptyLabel }}
  </p>
  <svg
    v-else
    class="usage-sparkline"
    role="img"
    :aria-labelledby="`${titleID} ${descriptionID}`"
    viewBox="0 0 240 64"
  >
    <title :id="titleID">{{ title }}</title>
    <desc :id="descriptionID">{{ description }}</desc>
    <path v-if="path" class="usage-sparkline__path" data-test="usage-sparkline-path" :d="path" />
    <circle
      v-if="points.length === 1"
      class="usage-sparkline__marker"
      data-test="usage-sparkline-marker"
      :cx="points[0]?.x"
      :cy="points[0]?.y"
      r="3"
    />
  </svg>
</template>

<style scoped>
.usage-sparkline {
  display: block;
  width: 100%;
  height: auto;
  color: var(--color-primary);
}

.usage-sparkline__path {
  fill: none;
  stroke: currentColor;
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.usage-sparkline__marker {
  fill: currentColor;
}

.usage-sparkline__empty {
  margin: 0;
  color: var(--color-text-muted);
}
</style>
