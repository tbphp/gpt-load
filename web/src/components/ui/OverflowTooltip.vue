<script setup lang="ts">
import {
  computed,
  getCurrentInstance,
  nextTick,
  onBeforeUnmount,
  onMounted,
  onUpdated,
  ref,
  useAttrs,
  watch,
  type Component,
  type ComponentPublicInstance,
} from 'vue'

import AppTooltip from './AppTooltip.vue'

defineOptions({ inheritAttrs: false })

const props = withDefaults(
  defineProps<{
    content?: string
    as?: string | Component
    side?: 'top' | 'right' | 'bottom' | 'left'
    align?: 'start' | 'center' | 'end'
    measureSelector?: string
    focusable?: boolean
    tooltipDisabled?: boolean
  }>(),
  {
    content: '',
    as: 'span',
    side: 'top',
    align: 'center',
    measureSelector: '',
    focusable: true,
    tooltipDisabled: false,
  },
)

const attrs = useAttrs()
const instance = getCurrentInstance()
// 动态根节点由本组件创建，Vue 无法沿无 DOM 的 Tooltip 组件链自动继承调用方的
// scoped CSS 标记。显式转发这些标记，保证接入提示前后的原有样式选择器保持有效。
const inheritedScopeAttrs = Object.fromEntries(
  [instance?.vnode.scopeId]
    .filter((scopeId): scopeId is string => Boolean(scopeId))
    .map((scopeId) => [scopeId, '']),
)
function forwardedAttrs(): Record<string, unknown> {
  return { ...inheritedScopeAttrs, ...attrs }
}
const trigger = ref<HTMLElement | ComponentPublicInstance | null>(null)
const overflowing = ref(false)
const measuredContent = ref('')
let resizeObserver: ResizeObserver | undefined
let observedTarget: HTMLElement | null = null

const tooltipContent = computed(() => props.content || measuredContent.value)
const tooltipIsDisabled = computed(
  () => props.tooltipDisabled || !overflowing.value || tooltipContent.value.length === 0,
)
const resolvedTabindex = computed(() => {
  if (attrs.tabindex !== undefined) return attrs.tabindex as string | number
  return props.focusable && overflowing.value ? 0 : undefined
})

function triggerElement(): HTMLElement | null {
  const value = trigger.value
  if (value instanceof HTMLElement) return value
  const element = value?.$el
  return element instanceof HTMLElement ? element : null
}

function measurementElement(): HTMLElement | null {
  const element = triggerElement()
  if (!element || !props.measureSelector) return element
  return element.querySelector<HTMLElement>(props.measureSelector)
}

function updateOverflow(target: HTMLElement | null): void {
  if (!target || props.tooltipDisabled) {
    measuredContent.value = ''
    overflowing.value = false
    return
  }

  measuredContent.value = target.textContent?.trim() ?? ''
  overflowing.value =
    tooltipContent.value.length > 0 &&
    (target.scrollWidth > target.clientWidth + 1 || target.scrollHeight > target.clientHeight + 1)
}

function observeTarget(): void {
  const target = measurementElement()
  if (target === observedTarget) {
    updateOverflow(target)
    return
  }

  resizeObserver?.disconnect()
  resizeObserver = undefined
  observedTarget = target

  if (target && typeof ResizeObserver === 'function') {
    resizeObserver = new ResizeObserver(() => updateOverflow(target))
    resizeObserver.observe(target)
  }
  updateOverflow(target)
}

function queueObservation(): void {
  void nextTick(observeTarget)
}

watch(() => [props.content, props.measureSelector, props.tooltipDisabled], queueObservation)
onMounted(queueObservation)
onUpdated(observeTarget)
onBeforeUnmount(() => resizeObserver?.disconnect())
</script>

<template>
  <AppTooltip :content="tooltipContent" :side="side" :align="align" :disabled="tooltipIsDisabled">
    <component :is="as" ref="trigger" v-bind="forwardedAttrs()" :tabindex="resolvedTabindex">
      <slot />
    </component>
  </AppTooltip>
</template>
