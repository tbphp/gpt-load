<script setup lang="ts">
import {
  TooltipContent,
  TooltipPortal,
  TooltipRoot,
  TooltipTrigger,
  useForwardExpose,
} from 'reka-ui'
import { getCurrentInstance, ref, watch } from 'vue'
import { overlaySideOffset, tooltipDelay } from './overlay'

defineOptions({ inheritAttrs: false })
const props = defineProps<{
  label?: string
  side?: 'top' | 'right' | 'bottom' | 'left'
  disabled?: boolean
}>()
const open = ref(false)
const { forwardRef } = useForwardExpose()
const instance = getCurrentInstance()

// Tooltip 的浮层使根节点成为 Fragment；把单根组件链的 scoped 样式传给真实触发元素。
// 只沿根节点向上传递，避免将页面样式作用域扩散到无关的祖先或浮层。
function triggerScopeAttrs(): Record<string, string> {
  const attrs: Record<string, string> = {}
  let owner = instance
  while (owner) {
    if (owner.vnode.scopeId) attrs[owner.vnode.scopeId] = ''
    if (owner.parent?.subTree !== owner.vnode) break
    owner = owner.parent
  }
  return attrs
}
watch(
  () => props.disabled || !props.label,
  (disabled) => {
    if (disabled) open.value = false
  },
)
</script>

<template>
  <TooltipRoot
    v-model:open="open"
    :disabled="disabled || !label"
    :delay-duration="tooltipDelay"
    ignore-non-keyboard-focus
  >
    <TooltipTrigger :ref="forwardRef" v-bind="{ ...triggerScopeAttrs(), ...$attrs }" as-child
      ><slot
    /></TooltipTrigger>
    <TooltipPortal v-if="open && label && !disabled">
      <TooltipContent
        class="modern-tooltip"
        :side="side ?? 'bottom'"
        :side-offset="overlaySideOffset"
        :collision-padding="overlaySideOffset"
      >
        {{ label }}
      </TooltipContent>
    </TooltipPortal>
  </TooltipRoot>
</template>

<style>
.modern-tooltip {
  z-index: var(--modern-layer-tooltip);
  max-width: min(var(--modern-tooltip-max-width), calc(100vw - var(--modern-space-8)));
  max-height: calc(100dvh - var(--modern-space-8));
  overflow-y: auto;
  overscroll-behavior: contain;
  border: var(--modern-line-width) solid var(--modern-tooltip-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-tooltip-surface);
  padding: var(--modern-space-1-5) var(--modern-space-3);
  color: var(--modern-tooltip-text);
  font-size: var(--modern-font-size-body);
  font-weight: var(--modern-weight-regular);
  line-height: var(--modern-leading-compact);
  text-align: left;
  overflow-wrap: anywhere;
  white-space: pre-line;
  box-shadow: var(--modern-shadow-tooltip);
}
</style>
