<script setup lang="ts">
import { Check, ChevronDown } from 'lucide-vue-next'
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

defineProps<{
  modelValue?: string
  label: string
  options: SelectOption[]
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const attrs = useAttrs()
</script>

<template>
  <SelectRoot
    :model-value="modelValue"
    @update:model-value="(value) => typeof value === 'string' && emit('update:modelValue', value)"
  >
    <SelectTrigger v-bind="attrs" class="app-select__trigger" :aria-label="label">
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
  min-height: 44px;
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
}
.app-select__value {
  min-width: 0;
  overflow-wrap: anywhere;
  white-space: normal;
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
  border: 1px solid var(--color-border);
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
  background: var(--color-surface-secondary);
}
.app-select__indicator {
  position: absolute;
  left: 10px;
  display: inline-flex;
  color: var(--color-primary);
}
</style>
