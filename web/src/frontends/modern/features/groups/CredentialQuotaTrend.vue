<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { QuotaHistoryWindow } from '@modern/api/credential-quota-history'
import { AppSvg, AppTooltip } from '@modern/components/ui'
import { dateFormatter } from '@modern/components/ui/intl-formatters'

const props = defineProps<{
  window: QuotaHistoryWindow
  from: number
  to: number
  bucketWidth: number
  cursorAtMS?: number
}>()
const emit = defineEmits<{ cursor: [time: number | undefined] }>()
const { t, n, locale } = useI18n()
const host = ref<HTMLElement>()
const width = ref(480)
const focused = ref(0)
const nodes = new Map<number, HTMLElement>()
let observer: ResizeObserver | undefined
onMounted(() => {
  observer = new ResizeObserver(([entry]) => {
    if (entry) width.value = Math.max(220, entry.contentRect.width)
  })
  if (host.value) observer.observe(host.value)
})
onBeforeUnmount(() => observer?.disconnect())
const height = 190,
  left = 64,
  top = 12,
  bottom = 158
const x = (at: number) =>
  left + ((at - props.from) / (props.to - props.from)) * (width.value - left - 8)
const y = (used: number) => top + (used / 10_000) * (bottom - top)
const points = computed(() =>
  props.window.points.map((point) => ({
    ...point,
    x: x(point.observedAt),
    y: y(point.usedBasisPoints),
  })),
)
const active = computed(() => {
  if (props.cursorAtMS === undefined) return undefined
  const point = [...points.value].reverse().find((point) => point.observedAt <= props.cursorAtMS!)
  return point && props.cursorAtMS - point.observedAt <= Math.max(300_000, props.bucketWidth * 2)
    ? point
    : undefined
})
const segments = computed(() => {
  const result: string[] = []
  let path = ''
  points.value.forEach((point, index) => {
    const previous = points.value[index - 1]
    const separated =
      !previous ||
      point.resetAt !== previous.resetAt ||
      point.usedBasisPoints < previous.usedBasisPoints ||
      point.observedAt - previous.observedAt > Math.max(300_000, props.bucketWidth * 2)
    if (separated) {
      if (path) result.push(path)
      path = `M${point.x},${point.y}`
    } else path += ` L${point.x},${point.y}`
  })
  if (path) result.push(path)
  return result
})
const resets = computed(() =>
  points.value.filter((point, index) => {
    const previous = points.value[index - 1]
    return (
      previous &&
      (point.resetAt !== previous.resetAt || point.usedBasisPoints < previous.usedBasisPoints)
    )
  }),
)
const clock = (at: number) =>
  dateFormatter(
    locale.value,
    props.to - props.from > 2 * 86400000
      ? { month: 'short', day: 'numeric' }
      : { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' },
  ).format(at)
function label(index: number): string {
  const point = points.value[index]!
  const at = dateFormatter(locale.value, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).format(point.observedAt)
  return `${at}\n${t('credentialCards.remainingPercent')} ${n((10_000 - point.usedBasisPoints) / 100, { maximumFractionDigits: 2 })}%${point.resetAt ? '\n' + t('credentialCards.resetsAt', { time: clock(point.resetAt) }) : ''}`
}
function node(index: number, value: unknown): void {
  if (value instanceof HTMLElement) nodes.set(index, value)
  else nodes.delete(index)
}
function focus(index: number): void {
  focused.value = index
  emit('cursor', points.value[index]?.observedAt)
}
function navigate(event: KeyboardEvent, index: number): void {
  let next = index
  if (event.key === 'ArrowLeft') next--
  else if (event.key === 'ArrowRight') next++
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = points.value.length - 1
  else if (event.key === 'Escape') {
    emit('cursor', undefined)
    return
  } else return
  event.preventDefault()
  nodes.get(Math.max(0, Math.min(points.value.length - 1, next)))?.focus({ preventScroll: true })
}
</script>
<template>
  <div
    ref="host"
    class="modern-quota-trend"
    role="group"
    :aria-label="t('credentialCards.quotaHistory')"
    @pointerleave="emit('cursor', undefined)"
  >
    <AppSvg :viewBox="`0 0 ${width} ${height}`" aria-hidden="true" focusable="false">
      <g v-for="step in 5" :key="step" class="modern-quota-trend-grid">
        <line :x1="left" :x2="width - 8" :y1="y((step - 1) * 2500)" :y2="y((step - 1) * 2500)" />
        <text :x="left - 10" :y="y((step - 1) * 2500) + 4" text-anchor="end"
          >{{ n(100 - (step - 1) * 25) }}%</text
        >
      </g>
      <path
        v-for="(path, index) in segments"
        :key="index"
        :d="path"
        class="modern-quota-trend-line"
      />
      <circle
        v-for="point in points"
        :key="point.observedAt"
        :cx="point.x"
        :cy="point.y"
        r="2.5"
        class="modern-quota-trend-dot"
      />
      <circle v-if="active" :cx="active.x" :cy="active.y" r="4" class="modern-quota-trend-dot" />
      <g v-for="point in resets" :key="point.observedAt" class="modern-quota-trend-reset">
        <line :x1="point.x" :x2="point.x" :y1="top" :y2="bottom" />
        <text :x="point.x" :y="top + 10" text-anchor="middle">{{
          t('credentialCards.resetAction')
        }}</text>
      </g>
      <line
        v-if="cursorAtMS !== undefined"
        :x1="x(cursorAtMS)"
        :x2="x(cursorAtMS)"
        :y1="top"
        :y2="bottom"
        class="modern-quota-trend-cursor"
      />
      <text
        v-for="fraction in [0, 0.5, 1]"
        :key="fraction"
        :x="x(from + (to - from) * fraction)"
        :y="height - 10"
        :text-anchor="fraction === 0 ? 'start' : fraction === 1 ? 'end' : 'middle'"
        class="modern-quota-trend-axis"
        >{{ clock(from + (to - from) * fraction) }}</text
      >
    </AppSvg>
    <AppTooltip
      v-for="(point, index) in points"
      :key="point.observedAt"
      :label="label(index)"
      side="top"
    >
      <span
        :ref="(value) => node(index, value)"
        class="modern-quota-trend-hit"
        role="img"
        :aria-label="label(index)"
        :tabindex="index === Math.min(focused, points.length - 1) ? 0 : -1"
        :style="{ left: `${(point.x / width) * 100}%`, top: `${(point.y / height) * 100}%` }"
        @pointerenter="emit('cursor', point.observedAt)"
        @focus="focus(index)"
        @blur="emit('cursor', undefined)"
        @keydown="navigate($event, index)"
      />
    </AppTooltip>
  </div>
</template>
<style scoped>
.modern-quota-trend {
  position: relative;
  min-width: 0;
  color: var(--modern-accent);
}
.modern-quota-trend :deep(svg) {
  display: block;
  width: 100%;
  overflow: visible;
}
.modern-quota-trend-grid line {
  stroke: var(--modern-border);
  stroke-width: var(--modern-line-width);
}
.modern-quota-trend-grid text,
.modern-quota-trend-axis,
.modern-quota-trend-reset text {
  fill: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-quota-trend-line {
  fill: none;
  stroke: currentColor;
  stroke-width: var(--modern-trend-stroke);
}
.modern-quota-trend-dot {
  fill: currentColor;
}
.modern-quota-trend-reset line,
.modern-quota-trend-cursor {
  stroke: var(--modern-muted);
  stroke-width: var(--modern-line-width);
  stroke-dasharray: 3 3;
}
.modern-quota-trend-hit {
  position: absolute;
  width: var(--modern-space-3);
  height: var(--modern-space-3);
  transform: translate(-50%, -50%);
  border-radius: var(--modern-radius-round);
}
.modern-quota-trend-hit:focus-visible {
  outline: var(--modern-focus-width) solid var(--modern-accent);
  outline-offset: var(--modern-focus-offset);
}
</style>
