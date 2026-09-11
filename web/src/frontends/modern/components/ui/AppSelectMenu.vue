<script setup lang="ts">
import { Check } from '@lucide/vue'
import {
  DropdownMenuContent,
  DropdownMenuItemIndicator,
  DropdownMenuLabel,
  DropdownMenuPortal,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
import type { Component } from 'vue'

import AppIcon from './AppIcon.vue'
import AppIconButton from './AppIconButton.vue'
import { overlaySideOffset } from './overlay'

defineProps<{
  label: string
  icon: Component
  modelValue: string
  options: ReadonlyArray<{ value: string; label: string }>
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

function select(value: unknown): void {
  if (typeof value === 'string') emit('update:modelValue', value)
}
</script>

<template>
  <DropdownMenuRoot>
    <DropdownMenuTrigger as-child
      ><AppIconButton :icon="icon" :label="label"
    /></DropdownMenuTrigger>
    <DropdownMenuPortal>
      <DropdownMenuContent class="modern-menu" align="end" :side-offset="overlaySideOffset">
        <DropdownMenuLabel class="modern-menu-label">{{ label }}</DropdownMenuLabel>
        <DropdownMenuRadioGroup :model-value="modelValue" @update:model-value="select">
          <DropdownMenuRadioItem
            v-for="option in options"
            :key="option.value"
            class="modern-menu-item"
            :value="option.value"
          >
            {{ option.label }}
            <DropdownMenuItemIndicator
              ><AppIcon :icon="Check" size="sm"
            /></DropdownMenuItemIndicator>
          </DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenuPortal>
  </DropdownMenuRoot>
</template>

<!-- Portal 内容不带当前组件的 scoped 属性，使用唯一的 modern 类名作用于浮层节点。 -->
<style>
.modern-menu {
  z-index: var(--modern-layer-menu);
  min-width: var(--modern-menu-min-width);
  max-width: calc(100vw - var(--modern-space-8));
  max-height: var(--reka-dropdown-menu-content-available-height);
  overflow-y: auto;
  overscroll-behavior: contain;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
  padding: var(--modern-space-1);
  box-shadow: var(--modern-shadow-menu);
}
.modern-menu-label {
  padding: var(--modern-space-1) var(--modern-space-2) var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.modern-menu-item {
  display: flex;
  min-height: var(--modern-control-md);
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-5);
  border-radius: var(--modern-radius-small);
  padding: var(--modern-space-1-5) var(--modern-space-2);
  font-size: var(--modern-font-size-small);
  cursor: pointer;
}
.modern-menu-item[data-highlighted] {
  outline: none;
  background: var(--modern-subtle);
}
.modern-menu-item[data-state='checked'] {
  color: var(--modern-accent);
}
@media (max-width: 760px) {
  .modern-menu-item {
    min-height: var(--modern-touch-target);
    font-size: var(--modern-font-size-body);
  }
}
</style>
