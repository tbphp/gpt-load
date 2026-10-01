<script setup lang="ts">
import { Upload } from '@lucide/vue'
import { ref } from 'vue'
import AppButton from './AppButton.vue'
import type { ControlSize } from './types'

const props = defineProps<{
  label: string
  accept?: string
  multiple?: boolean
  disabled?: boolean
  loading?: boolean
  size?: ControlSize
}>()
const emit = defineEmits<{ select: [files: File[]] }>()
const input = ref<HTMLInputElement>()
function open(): void {
  if (!props.disabled && !props.loading) input.value?.click()
}
function selected(event: Event): void {
  const element = event.target as HTMLInputElement
  const files = Array.from(element.files ?? [])
  element.value = ''
  if (files.length) emit('select', files)
}
</script>

<template>
  <slot :open="open">
    <AppButton :icon="Upload" :disabled="disabled" :loading="loading" :size="size" @click="open">{{
      label
    }}</AppButton>
  </slot>
  <input
    ref="input"
    type="file"
    hidden
    :accept="accept"
    :multiple="multiple"
    :disabled="disabled || loading"
    tabindex="-1"
    @change="selected"
  />
</template>
