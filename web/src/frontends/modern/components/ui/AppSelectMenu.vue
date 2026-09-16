<script setup lang="ts">
import { Check, ChevronDown } from '@lucide/vue'
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
import { computed, type Component } from 'vue'
import AppButton from './AppButton.vue'
import AppIcon from './AppIcon.vue'
import AppIconButton from './AppIconButton.vue'
import AppMenuSurface from './AppMenuSurface.vue'
import { overlaySideOffset } from './overlay'
import type { SelectOption } from './types'

const props = defineProps<{
  label: string
  icon: Component
  modelValue: string
  options: readonly SelectOption[]
  showValue?: boolean
  disabled?: boolean
}>()
const selectedLabel = computed(
  () => props.options.find((option) => option.value === props.modelValue)?.label ?? props.label,
)
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

function select(value: unknown): void {
  if (!props.disabled && typeof value === 'string' && value !== props.modelValue)
    emit('update:modelValue', value)
}
</script>

<template>
  <DropdownMenuRoot>
    <DropdownMenuTrigger as-child>
      <AppButton
        v-if="showValue"
        class="modern-select-menu-value"
        variant="ghost"
        :disabled="disabled"
        :aria-label="`${label}：${selectedLabel}`"
      >
        <AppIcon :icon="icon" size="sm" />
        <span>{{ selectedLabel }}</span>
        <AppIcon :icon="ChevronDown" size="xs" class="modern-select-menu-chevron" />
      </AppButton>
      <AppIconButton v-else :icon="icon" :label="label" :disabled="disabled" />
    </DropdownMenuTrigger>
    <DropdownMenuPortal>
      <AppMenuSurface>
        <DropdownMenuContent align="end" :side-offset="overlaySideOffset">
          <DropdownMenuLabel class="modern-menu-label">{{ label }}</DropdownMenuLabel>
          <DropdownMenuRadioGroup :model-value="modelValue" @update:model-value="select">
            <DropdownMenuRadioItem
              v-for="option in options"
              :key="option.value"
              class="modern-menu-option"
              :value="option.value"
              :disabled="disabled || option.disabled"
            >
              {{ option.label }}
              <DropdownMenuItemIndicator
                ><AppIcon :icon="Check" size="sm"
              /></DropdownMenuItemIndicator>
            </DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </AppMenuSurface>
    </DropdownMenuPortal>
  </DropdownMenuRoot>
</template>

<style scoped>
.modern-button.modern-select-menu-value {
  border-color: var(--modern-tooltip-border);
  background: var(--modern-accent-soft);
  color: var(--modern-accent);
  font-weight: var(--modern-weight-regular);
  box-shadow: none;
}
.modern-button.modern-select-menu-value:hover:not(:disabled, [aria-disabled='true']) {
  border-color: var(--modern-segmented-active-border);
  background: var(--modern-segmented-active-hover);
  color: var(--modern-accent);
}
.modern-button.modern-select-menu-value:focus-visible,
.modern-button.modern-select-menu-value:active:not(:disabled, [aria-disabled='true']),
.modern-button.modern-select-menu-value[aria-expanded='true']:hover,
.modern-button.modern-select-menu-value[aria-expanded='true'] {
  border-color: var(--modern-accent);
  background: var(--modern-segmented-active-hover);
  color: var(--modern-accent);
}
.modern-button.modern-select-menu-value:focus-visible {
  box-shadow: var(--modern-shadow-focus);
}
.modern-select-menu-chevron {
  margin-left: var(--modern-space-1);
  transition: transform var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-select-menu-value[aria-expanded='true'] .modern-select-menu-chevron {
  transform: rotate(180deg);
}
</style>
