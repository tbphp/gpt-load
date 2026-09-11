<script setup lang="ts">
import { LoaderCircle } from '@lucide/vue'
import { Primitive, useForwardExpose } from 'reka-ui'
import { computed, type Component } from 'vue'

import AppIcon from './AppIcon.vue'

const props = withDefaults(
  defineProps<{
    variant?: 'default' | 'primary' | 'ghost' | 'brand' | 'danger'
    size?: 'xs' | 'sm' | 'md'
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
        :size="size === 'xs' ? 'sm' : 'md'"
        :class="{ 'modern-spin': loading }"
      />
      <slot />
    </template>
  </Primitive>
</template>

<style scoped>
.modern-button {
  --modern-button-size: var(--modern-control-md);
  display: inline-flex;
  width: fit-content;
  min-height: var(--modern-button-size);
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  gap: var(--modern-space-1-5);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-surface);
  padding: var(--modern-space-1-5) var(--modern-space-3);
  color: var(--modern-text);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
  line-height: var(--modern-leading-compact);
  text-decoration: none;
}
.modern-button:hover:not(:disabled, [aria-disabled='true']) {
  border-color: var(--modern-muted);
  background: var(--modern-subtle);
}
.modern-button--xs {
  --modern-button-size: var(--modern-control-xs);
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
.modern-button--ghost {
  border-color: transparent;
  background: transparent;
  color: var(--modern-muted);
}
.modern-button--ghost:hover:not(:disabled, [aria-disabled='true']),
.modern-button--ghost[data-state='open'] {
  border-color: transparent;
  background: var(--modern-subtle);
  color: var(--modern-text);
}
/* brand 用于需要在一排中性图标里被一眼看到的高频入口。 */
.modern-button--brand {
  border-color: transparent;
  background: transparent;
  color: var(--modern-coral);
}
.modern-button--brand:hover:not(:disabled, [aria-disabled='true']) {
  border-color: transparent;
  background: var(--modern-accent-soft);
  color: var(--modern-accent);
}
.modern-button--danger {
  border-color: var(--modern-danger);
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
@media (max-width: 760px) {
  .modern-button {
    --modern-button-size: var(--modern-touch-target);
  }
}
</style>
