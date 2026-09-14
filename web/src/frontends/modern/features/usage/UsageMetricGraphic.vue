<script setup lang="ts">
import { computed } from 'vue'
import type { UsageReport } from '@modern/api/usage'
import { AppSvg } from '@modern/components/ui'
import { cacheRate, chartPoints, successRate } from './usage-display'
import type { TrendMetric } from './usage-state'

const props = defineProps<{ report: UsageReport; metric: TrendMetric | 'success' }>()
const rate = computed(() => props.metric === 'success' || props.metric === 'cache')
const ratio = computed(() =>
  props.metric === 'success' ? successRate(props.report.summary) : cacheRate(props.report.summary),
)
const paths = computed(() => {
  if (props.metric === 'success' || props.metric === 'cache') return []
  const points = chartPoints(props.report, props.metric)
  const peak = Math.max(0, ...points.map((point) => point.value ?? 0)) || 1
  const result: { line: string; area: string }[] = []
  let line = '',
    first = 0,
    last = 0
  function finish(): void {
    if (line) result.push({ line, area: `${line} L${last},36 L${first},36 Z` })
    line = ''
  }
  for (const point of points) {
    if (point.value === null) {
      finish()
      continue
    }
    const x =
      4 +
      (80 * ((point.from + point.to) / 2 - props.report.from_ms)) /
        (props.report.to_ms - props.report.from_ms)
    const y = 34 - (28 * point.value) / peak
    if (!line) first = x
    last = x
    line += `${line ? ' L' : 'M'}${x},${y}`
  }
  finish()
  return result
})
</script>

<template>
  <span class="modern-usage-metric-graphic" :class="{ 'is-rate': rate }" aria-hidden="true">
    <AppSvg v-if="rate" viewBox="0 0 44 44" focusable="false">
      <circle cx="22" cy="22" r="17" class="modern-usage-metric-track" />
      <circle
        v-if="ratio !== null"
        cx="22"
        cy="22"
        r="17"
        pathLength="100"
        :stroke-dasharray="`${ratio} ${100 - ratio}`"
        transform="rotate(-90 22 22)"
        class="modern-usage-metric-rate"
      />
    </AppSvg>
    <AppSvg v-else viewBox="0 0 88 40" focusable="false">
      <template v-for="(path, index) in paths" :key="index">
        <path :d="path.area" class="modern-usage-metric-area" />
        <path :d="path.line" class="modern-usage-metric-line" />
      </template>
    </AppSvg>
  </span>
</template>

<style scoped>
.modern-usage-metric-graphic {
  display: block;
  width: 80px;
  height: 40px;
  flex: none;
  color: var(--modern-usage-stat-tone);
}
.modern-usage-metric-graphic.is-rate {
  width: var(--modern-touch-target);
  height: var(--modern-touch-target);
}
.modern-usage-metric-graphic svg {
  display: block;
  width: 100%;
  height: 100%;
}
.modern-usage-metric-track,
.modern-usage-metric-rate {
  fill: none;
  stroke-width: var(--modern-space-1);
}
.modern-usage-metric-track {
  stroke: color-mix(in srgb, currentColor 12%, var(--modern-surface));
}
.modern-usage-metric-rate {
  stroke: currentColor;
}
.modern-usage-metric-line {
  fill: none;
  stroke: currentColor;
  stroke-width: var(--modern-trend-stroke);
  stroke-linecap: round;
  stroke-linejoin: round;
}
.modern-usage-metric-area {
  fill: var(--modern-chart-area-fill);
}
</style>
