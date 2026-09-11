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
import AppMenuSurface from './AppMenuSurface.vue'
import { overlaySideOffset } from './overlay'
import type { SelectOption } from './types'

defineProps<{
  label: string
  icon: Component
  modelValue: string
  options: readonly SelectOption[]
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

function select(value: unknown): void {
  if (typeof value === 'string') emit('update:modelValue', value)
}
</script>

<template>
  <DropdownMenuRoot>
    <DropdownMenuTrigger as-child>
      <AppIconButton :icon="icon" :label="label" />
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
              :disabled="option.disabled"
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
