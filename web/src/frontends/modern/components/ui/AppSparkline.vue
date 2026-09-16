<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import AppTooltip from './AppTooltip.vue'

const props = defineProps<{
  label: string
  values: readonly number[]
  pointLabels?: readonly string[]
  tone?: 'accent' | 'info'
}>()
const gradientId = useId()
const hovered = ref<number>()
const focused = ref<number>()
const tabStop = ref(0)
const pointElements = new Map<number, HTMLElement>()
const interactive = computed(() => Boolean(props.pointLabels?.length))
const coordinates = computed(() => {
  const peak = Math.max(1, ...props.values)
  const step = props.values.length > 1 ? 100 / (props.values.length - 1) : 100
  return props.values.map((value, index) => {
    const x = props.values.length === 1 ? 50 : index * step
    const left = index === 0 ? 0 : x - step / 2
    const right = index === props.values.length - 1 ? 100 : x + step / 2
    return { x, y: 96 - (value / peak) * 88, left, width: right - left }
  })
})
const points = computed(() => {
  if (coordinates.value.length === 1) {
    const y = coordinates.value[0]!.y
    return `0,${y} 1000,${y}`
  }
  return coordinates.value.map((point) => `${point.x * 10},${point.y}`).join(' ')
})
const activePoint = computed(() => {
  const index = hovered.value ?? focused.value
  return index === undefined ? undefined : coordinates.value[index]
})
function pointRef(index: number, element: unknown): void {
  if (element instanceof HTMLElement) pointElements.set(index, element)
  else pointElements.delete(index)
}
function focusPoint(index: number): void {
  focused.value = index
  tabStop.value = index
}
function navigate(event: KeyboardEvent, index: number): void {
  if (event.altKey || event.ctrlKey || event.metaKey) return
  if (event.key === 'Escape') {
    hovered.value = undefined
    focused.value = undefined
    return
  }
  let next = index
  if (event.key === 'ArrowLeft') next--
  else if (event.key === 'ArrowRight') next++
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = props.values.length - 1
  else return
  event.preventDefault()
  hovered.value = undefined
  next = Math.max(0, Math.min(props.values.length - 1, next))
  pointElements.get(next)?.focus({ preventScroll: true })
}
watch(
  () => props.values.length,
  (length) => {
    tabStop.value = Math.min(tabStop.value, Math.max(0, length - 1))
    if (hovered.value !== undefined && hovered.value >= length) hovered.value = undefined
    if (focused.value !== undefined && focused.value >= length) focused.value = undefined
  },
)
</script>

<template>
  <div
    class="modern-sparkline"
    :data-tone="tone"
    :role="interactive ? 'group' : 'img'"
    :aria-label="label"
    @pointerleave="hovered = undefined"
  >
    <svg viewBox="0 0 1000 100" preserveAspectRatio="none" aria-hidden="true" focusable="false">
      <defs>
        <linearGradient :id="gradientId" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" class="modern-sparkline-fill" stop-opacity="0.2" />
          <stop offset="1" class="modern-sparkline-fill" stop-opacity="0.015" />
        </linearGradient>
      </defs>
      <polygon v-if="points" :points="`0,100 ${points} 1000,100`" :fill="`url(#${gradientId})`" />
      <polyline
        v-if="points"
        :points="points"
        class="modern-sparkline-line"
        vector-effect="non-scaling-stroke"
      />
      <line
        v-if="activePoint"
        :x1="activePoint.x * 10"
        :x2="activePoint.x * 10"
        y1="0"
        y2="100"
        class="modern-sparkline-guide"
        stroke-dasharray="2 3"
        vector-effect="non-scaling-stroke"
      />
    </svg>
    <template v-if="interactive">
      <AppTooltip
        v-for="(point, index) in coordinates"
        :key="index"
        :label="pointLabels?.[index]"
        side="top"
      >
        <span
          :ref="(element) => pointRef(index, element)"
          class="modern-sparkline-hit"
          role="img"
          :aria-label="pointLabels?.[index]"
          :tabindex="index === tabStop ? 0 : -1"
          :style="{ left: `${point.left}%`, width: `${point.width}%` }"
          @pointerenter="hovered = index"
          @focus="focusPoint(index)"
          @blur="focused = undefined"
          @keydown="navigate($event, index)"
        />
      </AppTooltip>
    </template>
    <span
      v-if="activePoint"
      class="modern-sparkline-marker"
      :style="{ left: `${activePoint.x}%`, top: `${activePoint.y}%` }"
      aria-hidden="true"
    />
  </div>
</template>

<style scoped>
.modern-sparkline {
  --modern-sparkline-color: var(--modern-accent);
  position: relative;
  width: 100%;
  height: var(--modern-trend-height);
}
.modern-sparkline[data-tone='info'] {
  --modern-sparkline-color: var(--modern-chart-input);
}
.modern-sparkline > svg {
  display: block;
  width: 100%;
  height: 100%;
  overflow: visible;
}
.modern-sparkline-line {
  fill: none;
  stroke: var(--modern-sparkline-color);
  stroke-width: var(--modern-trend-stroke);
  stroke-linecap: round;
  stroke-linejoin: round;
}
.modern-sparkline-fill {
  stop-color: var(--modern-sparkline-color);
}
.modern-sparkline-guide {
  stroke: var(--modern-tooltip-border);
  stroke-width: var(--modern-line-width);
}
.modern-sparkline-hit {
  position: absolute;
  top: 0;
  bottom: 0;
  cursor: crosshair;
  outline: none;
}
.modern-sparkline-marker {
  position: absolute;
  width: var(--modern-space-1-5);
  height: var(--modern-space-1-5);
  border: var(--modern-line-width) solid var(--modern-surface);
  border-radius: var(--modern-radius-round);
  background: var(--modern-sparkline-color);
  pointer-events: none;
  transform: translate(-50%, -50%);
}
</style>
