<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import AppTooltip from './AppTooltip.vue'

const props = defineProps<{
  label: string
  values: readonly (number | null)[]
  pointLabels?: readonly string[]
  tone?: 'accent' | 'info' | 'cost'
  size?: 'sm'
  showMarker?: boolean
  showIsolatedPoints?: boolean
  referenceValue?: number
  ranges?: readonly { from: number; to: number }[]
  cursor?: number
  cursorLabel?: string
  tooltipSide?: 'top' | 'bottom'
}>()
const emit = defineEmits<{ cursorChange: [position: number | undefined] }>()
const gradientId = useId()
const hovered = ref<number>()
const focused = ref<number>()
const interactive = computed(() => Boolean(props.pointLabels?.length))
const scalePeak = computed(
  () =>
    Math.max(
      props.referenceValue ?? 0,
      ...props.values.filter((value): value is number => value !== null),
      0,
    ) || 1,
)
const referenceY = computed(() =>
  props.referenceValue === undefined
    ? undefined
    : 96 - (props.referenceValue / scalePeak.value) * 88,
)
function seriesCoordinates(values: readonly (number | null)[]) {
  const peak = scalePeak.value
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
const isolatedPoints = computed(() =>
  props.showIsolatedPoints
    ? coordinates.value.filter(
        (point, index, points) =>
          point.y !== null && points[index - 1]?.y == null && points[index + 1]?.y == null,
      )
    : [],
)
const segments = computed(() => seriesSegments(coordinates.value))
const activePoint = computed(() => {
  const index = hovered.value ?? focused.value
  const point = index === undefined ? undefined : coordinates.value[index]
  return point?.y === null ? undefined : point
})
const tooltip = computed(
  () => props.cursorLabel ?? props.pointLabels?.[hovered.value ?? focused.value ?? 0],
)
const cursorX = computed(() =>
  props.cursor === undefined ? activePoint.value?.x : props.cursor * 100,
)
function move(event: PointerEvent): void {
  const bounds = (event.currentTarget as HTMLElement).getBoundingClientRect()
  if (!bounds.width || !coordinates.value.length) return
  const position = Math.max(0, Math.min(1, (event.clientX - bounds.left) / bounds.width))
  let low = 0,
    high = coordinates.value.length
  while (low < high) {
    const middle = Math.floor((low + high) / 2)
    const point = coordinates.value[middle]!
    if (position * 100 >= point.left + point.width) low = middle + 1
    else high = middle
  }
  hovered.value = Math.min(low, coordinates.value.length - 1)
  emit('cursorChange', position)
}
function leave(): void {
  hovered.value = undefined
  emit('cursorChange', undefined)
}
function blur(): void {
  focused.value = undefined
  emit('cursorChange', undefined)
}
function focusPoint(): void {
  const index = hovered.value ?? focused.value ?? 0
  focused.value = index
  emit('cursorChange', coordinates.value[index]!.x / 100)
}
function navigate(event: KeyboardEvent): void {
  if (event.altKey || event.ctrlKey || event.metaKey) return
  if (event.key === 'Escape') {
    hovered.value = undefined
    focused.value = undefined
    emit('cursorChange', undefined)
    return
  }
  let next = hovered.value ?? focused.value ?? 0
  if (event.key === 'ArrowLeft') next--
  else if (event.key === 'ArrowRight') next++
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = props.values.length - 1
  else return
  event.preventDefault()
  hovered.value = undefined
  next = Math.max(0, Math.min(props.values.length - 1, next))
  focused.value = next
  emit('cursorChange', coordinates.value[next]!.x / 100)
}
watch(
  () => props.values.length,
  (length) => {
    if (hovered.value !== undefined && hovered.value >= length) hovered.value = undefined
    if (focused.value !== undefined && focused.value >= length) focused.value = undefined
  },
)
</script>

<template>
  <div
    class="modern-sparkline"
    :data-tone="tone"
    :data-size="size"
    :role="interactive ? 'group' : 'img'"
    :aria-label="label"
  >
    <svg viewBox="0 0 1000 100" preserveAspectRatio="none" aria-hidden="true" focusable="false">
      <defs>
        <linearGradient :id="gradientId" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" class="modern-sparkline-fill" stop-opacity="0.2" />
          <stop offset="1" class="modern-sparkline-fill" stop-opacity="0.015" />
        </linearGradient>
      </defs>
      <line
        v-if="referenceY !== undefined"
        x1="0"
        x2="1000"
        :y1="referenceY"
        :y2="referenceY"
        class="modern-sparkline-guide"
        stroke-dasharray="4 4"
        vector-effect="non-scaling-stroke"
      />
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
    <span
      v-for="point in isolatedPoints"
      :key="point.x"
      class="modern-sparkline-marker"
      :style="{ left: `${point.x}%`, top: `${point.y}%` }"
      aria-hidden="true"
    />
    <AppTooltip v-if="interactive" :label="tooltip" :side="tooltipSide ?? 'top'">
      <span
        class="modern-sparkline-hit"
        role="img"
        :aria-label="tooltip"
        tabindex="0"
        @pointermove="move"
        @pointerleave="leave"
        @focus="focusPoint"
        @blur="blur"
        @keydown="navigate"
      />
    </AppTooltip>
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
.modern-sparkline[data-size='sm'] {
  height: var(--modern-trend-sm-height);
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
  inset: 0;
  cursor: crosshair;
  outline: none;
}
.modern-sparkline-hit:focus-visible {
  outline: var(--modern-line-width) dashed var(--modern-tooltip-border);
  outline-offset: 0;
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
