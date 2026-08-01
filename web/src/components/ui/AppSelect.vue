<script setup lang="ts">
import { Check, ChevronDown } from '@lucide/vue'
import { useAttrs } from 'vue'
import {
  SelectContent,
  SelectItem,
  SelectItemIndicator,
  SelectItemText,
  SelectPortal,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from 'reka-ui'

interface SelectOption {
  value: string
  label: string
}

defineOptions({ inheritAttrs: false })

const props = withDefaults(
  defineProps<{
    modelValue?: string
    label: string
    options: SelectOption[]
    disabled?: boolean
    variant?: 'default' | 'embedded'
    size?: 'md' | 'compact'
  }>(),
  {
    modelValue: undefined,
    disabled: false,
    variant: 'default',
    size: 'md',
  },
)
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const attrs = useAttrs()
</script>

<template>
  <SelectRoot
    :model-value="modelValue"
    :disabled="props.disabled"
    @update:model-value="(value) => typeof value === 'string' && emit('update:modelValue', value)"
  >
    <SelectTrigger
      v-bind="attrs"
      class="app-select__trigger"
      :class="[`app-select__trigger--${props.variant}`, `app-select__trigger--${props.size}`]"
      :aria-label="label"
      :disabled="props.disabled"
    >
      <SelectValue class="app-select__value" />
      <ChevronDown class="app-select__chevron" :size="16" aria-hidden="true" />
    </SelectTrigger>
    <SelectPortal>
      <SelectContent class="app-select__content" position="popper" :side-offset="6">
        <SelectItem
          v-for="option in options"
          :key="option.value"
          class="app-select__item"
          :value="option.value"
          :data-value="option.value"
        >
          <SelectItemIndicator class="app-select__indicator">
            <Check :size="15" aria-hidden="true" />
          </SelectItemIndicator>
          <SelectItemText>{{ option.label }}</SelectItemText>
        </SelectItem>
      </SelectContent>
    </SelectPortal>
  </SelectRoot>
</template>

<style>
.app-select__trigger {
  display: inline-flex;
  min-width: 126px;
  min-height: var(--control-md);
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 8px 10px;
  font: inherit;
  cursor: pointer;
  transition:
    color var(--duration-fast) var(--easing-standard),
    border-color var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard),
    opacity var(--duration-fast) var(--easing-standard);
}
.app-select__trigger:hover:not([data-disabled]) {
  border-color: var(--color-text-faint);
}
.app-select__trigger[data-disabled] {
  cursor: not-allowed;
  opacity: 0.55;
}
.app-select__trigger--embedded {
  width: 100%;
  min-width: 0;
  min-height: 0;
  flex: 1;
  border: 0;
  border-radius: 0;
  padding: 7px 10px;
  font-size: var(--text-sm);
}
.app-select__trigger--compact {
  width: auto;
  min-width: 0;
  min-height: var(--control-compact);
  height: var(--control-compact);
  padding: 0 var(--space-2);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}
.app-select__trigger--compact .app-select__chevron,
.app-select__trigger--embedded .app-select__chevron {
  width: 14px;
  height: 14px;
}
.app-select__value {
  min-width: 0;
  overflow-wrap: anywhere;
  white-space: normal;
}
.app-select__trigger--embedded .app-select__value {
  overflow: hidden;
  text-overflow: ellipsis;
  overflow-wrap: normal;
  white-space: nowrap;
}
.app-select__chevron {
  flex-shrink: 0;
}
.app-select__content {
  z-index: var(--z-popover);
  min-width: var(--reka-select-trigger-width);
  max-width: var(--reka-select-content-available-width);
  max-height: min(320px, var(--reka-select-content-available-height));
  overflow-y: auto;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding: var(--space-1);
  box-shadow: var(--shadow-overlay);
}
.app-select__item {
  position: relative;
  display: flex;
  min-width: 0;
  max-width: 100%;
  min-height: 44px;
  align-items: center;
  border-radius: var(--radius-tag);
  padding: 7px 10px 7px 32px;
  color: var(--color-text);
  cursor: pointer;
  outline: none;
  overflow-wrap: anywhere;
  white-space: normal;
}
.app-select__item[data-highlighted] {
  background: var(--color-surface-sunken);
}
.app-select__item[data-disabled] {
  cursor: not-allowed;
  opacity: 0.55;
}
.app-select__indicator {
  position: absolute;
  left: 10px;
  display: inline-flex;
  color: var(--color-action);
}
</style>
