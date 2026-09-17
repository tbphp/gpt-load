<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import AppTooltip from './AppTooltip.vue'

const props = defineProps<{
  label: string
  values: readonly (number | null)[]
  pointLabels?: readonly string[]
  tone?: 'accent' | 'info' | 'cost'
  showMarker?: boolean
  ranges?: readonly { from: number; to: number }[]
  cursor?: number
  cursorLabel?: string
}>()
const emit = defineEmits<{ cursorChange: [position: number | undefined] }>()
const gradientId = useId()
const hovered = ref<number>()
const focused = ref<number>()
const tabStop = ref(0)
const pointElements = new Map<number, HTMLElement>()
const interactive = computed(() => Boolean(props.pointLabels?.length))
function seriesCoordinates(values: readonly (number | null)[]) {
  const peak = Math.max(0, ...values.filter((value): value is number => value !== null)) || 1
  const step = values.length > 1 ? 100 / (values.length - 1) : 100
  return values.map((value, index) => {
    const range = props.ranges?.[index]
    const x = range ? ((range.from + range.to) / 2) * 100 : values.length === 1 ? 50 : index * step
    const left = range ? range.from * 100 : index === 0 ? 0 : x - step / 2
    const right = range ? range.to * 100 : index === values.length - 1 ? 100 : x + step / 2
    return { x, y: value === null ? null : 96 - (value / peak) * 88, left, width: right - left }
  })
}
function seriesSegments(coordinates: ReturnType<typeof seriesCoordinates>) {
  if (coordinates.length === 1 && coordinates[0]!.y !== null) {
    const y = coordinates[0]!.y
    return [{ points: `0,${y} 1000,${y}`, start: 0, end: 1000 }]
  }
  const result: { points: string; start: number; end: number }[] = []
  let points = '',
    start = 0,
    end = 0
  function finish(): void {
    if (points) result.push({ points, start, end })
    points = ''
  }
  for (const point of coordinates) {
    if (point.y === null) {
      finish()
      continue
    }
    if (!points) start = point.x * 10
    end = point.x * 10
    points += `${points ? ' ' : ''}${end},${point.y}`
  }
  finish()
  return result
}
const coordinates = computed(() => seriesCoordinates(props.values))
const segments = computed(() => seriesSegments(coordinates.value))
const activePoint = computed(() => {
  const index = hovered.value ?? focused.value
  const point = index === undefined ? undefined : coordinates.value[index]
  return point?.y === null ? undefined : point
})
const cursorX = computed(() =>
  props.cursor === undefined ? activePoint.value?.x : props.cursor * 100,
)
function move(event: PointerEvent): void {
  const bounds = (event.currentTarget as HTMLElement).getBoundingClientRect()
  if (bounds.width)
    emit('cursorChange', Math.max(0, Math.min(1, (event.clientX - bounds.left) / bounds.width)))
}
function leave(): void {
  hovered.value = undefined
  emit('cursorChange', undefined)
}
function blur(): void {
  focused.value = undefined
  emit('cursorChange', undefined)
}
function pointRef(index: number, element: unknown): void {
  if (element instanceof HTMLElement) pointElements.set(index, element)
  else pointElements.delete(index)
}
function focusPoint(index: number): void {
  focused.value = index
  tabStop.value = index
  emit('cursorChange', coordinates.value[index]!.x / 100)
}
function navigate(event: KeyboardEvent, index: number): void {
  if (event.altKey || event.ctrlKey || event.metaKey) return
  if (event.key === 'Escape') {
    hovered.value = undefined
    focused.value = undefined
    emit('cursorChange', undefined)
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
    @pointermove="move"
    @pointerleave="leave"
  >
    <svg viewBox="0 0 1000 100" preserveAspectRatio="none" aria-hidden="true" focusable="false">
      <defs>
        <linearGradient :id="gradientId" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" class="modern-sparkline-fill" stop-opacity="0.2" />
          <stop offset="1" class="modern-sparkline-fill" stop-opacity="0.015" />
        </linearGradient>
      </defs>
      <g v-for="(segment, index) in segments" :key="index">
        <polygon
          :points="`${segment.start},100 ${segment.points} ${segment.end},100`"
          :fill="`url(#${gradientId})`"
        />
        <polyline
          :points="segment.points"
          class="modern-sparkline-line"
          vector-effect="non-scaling-stroke"
        />
      </g>
      <line
        v-if="cursorX !== undefined"
        :x1="cursorX * 10"
        :x2="cursorX * 10"
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
        :label="cursorLabel ?? pointLabels?.[index]"
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
          @blur="blur"
          @keydown="navigate($event, index)"
        />
      </AppTooltip>
    </template>
    <span
      v-if="activePoint && showMarker !== false"
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
.modern-sparkline[data-tone='cost'] {
  --modern-sparkline-color: var(--modern-chart-cost);
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
