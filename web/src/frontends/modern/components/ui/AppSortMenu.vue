<script setup lang="ts">
import { ArrowDownWideNarrow } from '@lucide/vue'
import AppSelectMenu from './AppSelectMenu.vue'
import type { SelectOption } from './types'

defineProps<{
  label: string
  modelValue: string
  options: readonly SelectOption[]
  disabled?: boolean
}>()
defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <AppSelectMenu
    :label="label"
    :icon="ArrowDownWideNarrow"
    :model-value="modelValue"
    :options="options"
    :disabled="disabled"
    show-value
    @update:model-value="$emit('update:modelValue', $event)"
  />
</template>
