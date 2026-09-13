<script setup lang="ts">
import AppTooltip from './AppTooltip.vue'
import { LoaderCircle } from '@lucide/vue'
import { useForwardExpose } from 'reka-ui'
import AppIcon from './AppIcon.vue'

defineOptions({ inheritAttrs: false })
const { forwardRef } = useForwardExpose()
withDefaults(
  defineProps<{
    modelValue: boolean
    label: string
    disabled?: boolean
    loading?: boolean
    size?: 'xxs' | 'sm' | 'md'
  }>(),
  { size: 'md' },
)
defineEmits<{ 'update:modelValue': [value: boolean] }>()
</script>

<template>
  <AppTooltip :label="label">
    <button
      :ref="forwardRef"
      v-bind="$attrs"
      class="modern-switch"
      :class="`modern-switch--${size}`"
      type="button"
      role="switch"
      :aria-label="label"
      :aria-checked="modelValue"
      :aria-busy="loading || undefined"
      :disabled="disabled || loading"
      @click="$emit('update:modelValue', !modelValue)"
    >
      <span class="modern-switch-track" :class="{ 'is-on': modelValue }" aria-hidden="true"
        ><span><AppIcon v-if="loading" :icon="LoaderCircle" size="xs" class="modern-spin" /></span
      ></span>
    </button>
  </AppTooltip>
</template>

<style scoped>
.modern-switch {
  display: inline-flex;
  min-height: var(--modern-control-md);
  align-items: center;
  justify-content: flex-start;
  flex-shrink: 0;
  border: 0;
  border-radius: var(--modern-radius-control);
  padding: var(--modern-space-1) 0;
  background: transparent;
}
.modern-switch-track {
  display: block;
  position: relative;
  width: var(--modern-switch-width);
  height: var(--modern-switch-height);
  border-radius: var(--modern-switch-height);
  background: var(--modern-switch-off);
  transition: background-color var(--modern-motion-fast) var(--modern-motion-ease);
}
/* 轨道尺寸与 sm 一致：滑块要容纳 12px 加载图标，再缩会溢出。
   xxs 只降容器高度，用于和同排的 xxs 按钮对齐。 */
.modern-switch--xxs,
.modern-switch--sm {
  --modern-switch-width: var(--modern-switch-sm-width);
  --modern-switch-height: var(--modern-switch-sm-height);
  --modern-switch-thumb: var(--modern-switch-sm-thumb);
  min-height: var(--modern-control-xs);
}
.modern-switch--xxs {
  min-height: var(--modern-control-xxs);
}
.modern-switch-track > span {
  position: absolute;
  display: flex;
  top: var(--modern-space-0-5);
  left: var(--modern-space-0-5);
  width: var(--modern-switch-thumb);
  height: var(--modern-switch-thumb);
  border-radius: var(--modern-radius-round);
  align-items: center;
  justify-content: center;
  background: var(--modern-on-action);
  color: var(--modern-accent);
  box-shadow: var(--modern-shadow-thumb);
  transition: transform var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-switch:hover:not(:disabled) .modern-switch-track.is-on {
  background: var(--modern-action-hover);
}
.modern-switch:focus-visible {
  outline: var(--modern-focus-width) solid var(--modern-accent);
  outline-offset: var(--modern-focus-offset);
}
.modern-switch-track.is-on {
  background: var(--modern-action);
}
.modern-switch-track.is-on > span {
  background: var(--modern-on-action);
  transform: translateX(calc(var(--modern-switch-width) - var(--modern-switch-height)));
}
@media (max-width: 760px) {
  .modern-switch {
    min-width: var(--modern-touch-target);
    min-height: var(--modern-touch-target);
  }
}
</style>
