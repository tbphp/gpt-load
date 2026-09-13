<script setup lang="ts">
import { LoaderCircle } from '@lucide/vue'
import { Primitive, useForwardExpose } from 'reka-ui'
import { computed, type Component } from 'vue'

import AppIcon from './AppIcon.vue'
import type { ButtonSize, ButtonVariant } from './types'

const props = withDefaults(
  defineProps<{
    variant?: ButtonVariant
    size?: ButtonSize
    type?: 'button' | 'submit' | 'reset'
    icon?: Component
    iconOnly?: boolean
    loading?: boolean
    disabled?: boolean
    asChild?: boolean
  }>(),
  { variant: 'default', size: 'md', type: 'button', icon: undefined },
)
const inactive = computed(() => props.disabled || props.loading)
const displayIcon = computed(() => (props.loading ? LoaderCircle : props.icon))
const iconSize = computed(() => (props.size === 'xxs' ? 'xs' : props.size === 'xs' ? 'sm' : 'md'))
const { forwardRef } = useForwardExpose()

function preventInactiveClick(event: MouseEvent): void {
  if (!inactive.value) return
  event.preventDefault()
  event.stopImmediatePropagation()
}
</script>

<template>
  <Primitive
    :ref="forwardRef"
    as="button"
    :as-child="asChild"
    :type="asChild ? undefined : type"
    class="modern-button"
    :class="[
      `modern-button--${variant}`,
      `modern-button--${size}`,
      { 'modern-button--icon': iconOnly },
    ]"
    :disabled="!asChild && inactive ? true : undefined"
    :aria-disabled="inactive || undefined"
    :aria-busy="loading || undefined"
    :tabindex="asChild && inactive ? -1 : undefined"
    @click.capture="preventInactiveClick"
  >
    <slot v-if="asChild" />
    <template v-else>
      <AppIcon
        v-if="displayIcon"
        :icon="displayIcon"
        :size="iconSize"
        :class="{ 'modern-spin': loading }"
      />
      <slot />
    </template>
  </Primitive>
</template>

<style scoped>
.modern-button {
  --modern-button-size: var(--modern-control-md);
  --modern-button-font-size: var(--modern-font-size-secondary);
  display: inline-flex;
  width: fit-content;
  min-height: var(--modern-button-size);
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  gap: var(--modern-space-1-5);
  border: var(--modern-line-width) solid var(--modern-control-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-surface);
  padding: var(--modern-space-1-5) var(--modern-space-3);
  color: var(--modern-text);
  font-size: var(--modern-button-font-size);
  font-weight: var(--modern-weight-medium);
  line-height: var(--modern-leading-compact);
  text-decoration: none;
  box-shadow: var(--modern-shadow-control);
  user-select: none;
}
.modern-button:hover:not(:disabled, [aria-disabled='true']) {
  border-color: var(--modern-control-border-hover);
  background: var(--modern-control-hover);
}
.modern-button:active:not(:disabled, [aria-disabled='true']) {
  background: var(--modern-control-pressed);
  box-shadow: none;
}
.modern-button:focus-visible {
  outline: var(--modern-focus-width) solid var(--modern-accent);
  outline-offset: var(--modern-focus-offset);
  box-shadow: var(--modern-shadow-focus);
}
.modern-button--xxs {
  --modern-button-size: var(--modern-control-xxs);
  --modern-button-font-size: var(--modern-font-size-caption);
  gap: var(--modern-space-1);
  padding: var(--modern-space-0-5) var(--modern-space-1-5);
}
.modern-button--xs {
  --modern-button-size: var(--modern-control-xs);
  --modern-button-font-size: var(--modern-font-size-small);
}
.modern-button--sm {
  --modern-button-size: var(--modern-control-sm);
}
.modern-button--primary {
  border-color: var(--modern-action);
  background: var(--modern-action);
  color: var(--modern-on-action);
}
.modern-button--primary:hover:not(:disabled, [aria-disabled='true']) {
  border-color: var(--modern-action-hover);
  background: var(--modern-action-hover);
}
.modern-button--primary:active:not(:disabled, [aria-disabled='true']) {
  border-color: var(--modern-action-hover);
  background: var(--modern-action-hover);
}
.modern-button--outline,
.modern-button--outline:hover:not(:disabled, [aria-disabled='true']),
.modern-button--outline:active:not(:disabled, [aria-disabled='true']) {
  border-color: var(--modern-accent);
  background: transparent;
  color: var(--modern-accent);
  box-shadow: none;
}
.modern-button--ghost {
  border-color: transparent;
  background: transparent;
  color: var(--modern-muted);
  box-shadow: none;
}
.modern-button--ghost:hover:not(:disabled, [aria-disabled='true']),
.modern-button--ghost[aria-expanded='true'] {
  border-color: transparent;
  background: var(--modern-control-hover);
  color: var(--modern-text);
}
/* brand 用于需要在一排中性图标里被一眼看到的高频入口。 */
.modern-button--brand {
  border-color: transparent;
  background: transparent;
  color: var(--modern-coral);
  box-shadow: none;
}
.modern-button--brand:hover:not(:disabled, [aria-disabled='true']) {
  border-color: transparent;
  background: var(--modern-accent-soft);
  color: var(--modern-accent);
}
.modern-button--brand[aria-current='page'] {
  background: var(--modern-accent-soft);
  color: var(--modern-accent);
}
.modern-button--danger {
  border-color: var(--modern-danger-soft);
  background: var(--modern-danger-soft);
  color: var(--modern-danger);
}
.modern-button--danger:hover:not(:disabled, [aria-disabled='true']) {
  border-color: var(--modern-danger);
  background: var(--modern-danger-soft);
}
.modern-button--icon {
  width: var(--modern-button-size);
  height: var(--modern-button-size);
  padding: 0;
}
.modern-button--text {
  min-height: var(--modern-control-xs);
  flex-shrink: 1;
  justify-content: flex-start;
  border: 0;
  background: transparent;
  padding: 0;
  color: inherit;
  font: inherit;
  text-align: inherit;
  box-shadow: none;
}
.modern-button--text:hover:not(:disabled, [aria-disabled='true']) {
  background: transparent;
  color: var(--modern-accent);
  text-decoration: underline;
  text-underline-offset: var(--modern-space-1);
}
@media (max-width: 760px) {
  .modern-button {
    --modern-button-size: var(--modern-touch-target);
  }
}
</style>
