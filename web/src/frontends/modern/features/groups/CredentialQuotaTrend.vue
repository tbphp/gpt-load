<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { QuotaHistoryReport } from '@modern/api/credential-quota-history'
import { AppSvg, AppTooltip } from '@modern/components/ui'

const props = defineProps<{ report: QuotaHistoryReport; cursor?: number; tooltip: string }>()
const emit = defineEmits<{ cursorChange: [position: number | undefined] }>()
const { t } = useI18n()
const gradientId = useId()
const focused = ref(0)
const height = 100,
  top = 8,
  bottom = 96
const x = (at: number) => ((at - props.report.from) / (props.report.to - props.report.from)) * 1000
const y = (used: number) => top + (used / 10_000) * (bottom - top)
const windows = computed(() => props.report.windows.filter((window) => window.points.length))
const curves = computed(() =>
  windows.value.map((window, index) => {
    let leadingPoint = window.points[0]!
    for (const point of window.points) {
      if (point.observedAt > props.report.from) break
      leadingPoint = point
    }
    const last = window.points[window.points.length - 1]!
    const points = [
      { observedAt: props.report.from, usedBasisPoints: leadingPoint.usedBasisPoints },
      ...window.points.filter(
        (point) => point.observedAt > props.report.from && point.observedAt < props.report.to,
      ),
      { observedAt: props.report.to, usedBasisPoints: last.usedBasisPoints },
    ]
    const path = points
      .map(
        (point, pointIndex) =>
          `${pointIndex ? 'L' : 'M'}${x(point.observedAt)},${y(point.usedBasisPoints)}`,
      )
      .join(' ')
    const startX = x(points[0]!.observedAt)
    const end = x(points[points.length - 1]!.observedAt)
    return {
      key: window.key,
      gradientId: `${gradientId}-${index}`,
      tone: index % 6,
      path,
      area: `${path} L${end},100 L${startX},100 Z`,
    }
  }),
)
const times = computed(() =>
  [
    ...new Set(
      windows.value.flatMap((window) =>
        window.points
          .filter(
            (point) => point.observedAt >= props.report.from && point.observedAt < props.report.to,
          )
          .map((point) => point.observedAt),
      ),
    ),
  ].sort((a, b) => a - b),
)
watch(
  () => times.value.length,
  (length) => {
    focused.value = length ? Math.min(focused.value, length - 1) : 0
  },
)
function move(event: PointerEvent): void {
  const bounds = (event.currentTarget as HTMLElement).getBoundingClientRect()
  if (bounds.width)
    emit('cursorChange', Math.max(0, Math.min(1, (event.clientX - bounds.left) / bounds.width)))
}
function focus(): void {
  const at = times.value[Math.min(focused.value, times.value.length - 1)]
  if (at !== undefined) emit('cursorChange', x(at) / 1000)
}
function navigate(event: KeyboardEvent): void {
  if (event.altKey || event.ctrlKey || event.metaKey) return
  let next = focused.value
  if (event.key === 'ArrowLeft') next--
  else if (event.key === 'ArrowRight') next++
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = times.value.length - 1
  else if (event.key === 'Escape') {
    emit('cursorChange', undefined)
    return
  } else return
  event.preventDefault()
  focused.value = Math.max(0, Math.min(times.value.length - 1, next))
  focus()
}
</script>
<template>
  <div class="modern-quota-trend">
    <div class="modern-quota-trend-plot">
      <AppSvg
        :viewBox="`0 0 1000 ${height}`"
        preserveAspectRatio="none"
        aria-hidden="true"
        focusable="false"
      >
        <defs>
          <linearGradient
            v-for="series in curves"
            :id="series.gradientId"
            :key="series.key"
            :data-tone="series.tone"
            x1="0"
            y1="0"
            x2="0"
            y2="1"
          >
            <stop offset="0" class="modern-quota-trend-fill" stop-opacity="0.2" />
            <stop offset="1" class="modern-quota-trend-fill" stop-opacity="0.015" />
          </linearGradient>
        </defs>
        <g v-for="series in curves" :key="series.key" :data-tone="series.tone">
          <path :d="series.area" :fill="`url(#${series.gradientId})`" />
          <path
            :d="series.path"
            class="modern-quota-trend-line"
            vector-effect="non-scaling-stroke"
          />
        </g>
        <line
          v-if="cursor !== undefined"
          :x1="cursor * 1000"
          :x2="cursor * 1000"
          y1="0"
          y2="100"
          class="modern-quota-trend-cursor"
          stroke-dasharray="2 3"
          vector-effect="non-scaling-stroke"
        />
      </AppSvg>
      <AppTooltip :label="tooltip" side="top">
        <span
          class="modern-quota-trend-hit"
          role="img"
          :aria-label="t('credentialCards.quotaHistory') + ' · ' + tooltip"
          tabindex="0"
          @pointermove="move"
          @pointerleave="emit('cursorChange', undefined)"
          @focus="focus"
          @blur="emit('cursorChange', undefined)"
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
.modern-quota-trend-plot {
  position: relative;
  min-width: 0;
}
.modern-quota-trend-plot :deep(svg) {
  display: block;
  width: 100%;
  height: var(--modern-trend-sm-height);
  overflow: visible;
}
.modern-quota-trend-fill {
  stop-color: currentColor;
}
.modern-quota-trend-line {
  fill: none;
  stroke: currentColor;
  stroke-width: var(--modern-trend-stroke);
  stroke-linecap: round;
  stroke-linejoin: round;
}
.modern-quota-trend-cursor {
  stroke: var(--modern-tooltip-border);
  stroke-width: var(--modern-line-width);
}
.modern-quota-trend-hit {
  position: absolute;
  inset: 0;
  cursor: crosshair;
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
