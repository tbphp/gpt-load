<script setup lang="ts">
import { TabsList, TabsRoot, TabsTrigger } from 'reka-ui'

export interface SegmentedControlOption {
  value: string
  label: string
  disabled?: boolean
}

defineProps<{
  modelValue: string
  label: string
  options: SegmentedControlOption[]
}>()

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

function handleSegmentKeydown(event: KeyboardEvent): void {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return

  const current = event.currentTarget
  if (!(current instanceof HTMLButtonElement)) return
  const triggers = [
    ...(current.parentElement?.querySelectorAll<HTMLButtonElement>(
      '[data-segment-value]:not(:disabled)',
    ) ?? []),
  ]
  if (triggers.length === 0) return

  const currentIndex = triggers.indexOf(current)
  if (currentIndex < 0) return
  let nextIndex = currentIndex
  if (event.key === 'Home') nextIndex = 0
  if (event.key === 'End') nextIndex = triggers.length - 1
  if (event.key === 'ArrowLeft') nextIndex = (currentIndex - 1 + triggers.length) % triggers.length
  if (event.key === 'ArrowRight') nextIndex = (currentIndex + 1) % triggers.length

  const next = triggers[nextIndex]
  const value = next?.dataset.segmentValue
  if (!next || !value || next === current) return
  event.preventDefault()
  next.focus()
  emit('update:modelValue', value)
}
</script>

<template>
  <TabsRoot
    class="segmented-control"
    :model-value="modelValue"
    orientation="horizontal"
    activation-mode="manual"
    @update:model-value="(value) => typeof value === 'string' && emit('update:modelValue', value)"
  >
    <TabsList class="segmented-control__list" :aria-label="label">
      <TabsTrigger
        v-for="option in options"
        :key="option.value"
        class="segmented-control__trigger"
        :value="option.value"
        :disabled="option.disabled"
        :data-segment-value="option.value"
        @keydown="handleSegmentKeydown"
      >
        {{ option.label }}
      </TabsTrigger>
    </TabsList>
  </TabsRoot>
</template>

<style scoped>
.segmented-control {
  display: inline-flex;
}
.segmented-control__list {
  display: inline-grid;
  grid-auto-columns: minmax(56px, 1fr);
  grid-auto-flow: column;
  overflow: hidden;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-control);
  background: var(--color-surface);
}
.segmented-control__trigger {
  min-height: var(--control-sm);
  border: 0;
  border-right: 1px solid var(--color-border-strong);
  background: transparent;
  color: var(--color-text-muted);
  padding: var(--space-2) var(--space-3);
  font: inherit;
  font-weight: 650;
  cursor: pointer;
}
.segmented-control__trigger:last-child {
  border-right: 0;
}
.segmented-control__trigger[data-state='active'] {
  background: var(--color-text);
  color: var(--color-text-inverse);
}
.segmented-control__trigger:disabled {
  color: var(--color-disabled);
  cursor: not-allowed;
}
</style>
