<script setup lang="ts">
import { TabsList, TabsRoot, TabsTrigger } from 'reka-ui'

export interface SegmentedControlOption {
  value: string
  label: string
  disabled?: boolean
}

withDefaults(
  defineProps<{
    modelValue: string
    label: string
    options: SegmentedControlOption[]
    controlsId?: string
    idPrefix?: string
    scrollable?: boolean
    appearance?: 'joined' | 'pills' | 'drawer'
    size?: 'compact' | 'touch'
  }>(),
  {
    controlsId: undefined,
    idPrefix: undefined,
    appearance: 'joined',
    size: 'compact',
  },
)

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

function handleSegmentKeydown(event: KeyboardEvent): void {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()

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
    <TabsList
      class="segmented-control__list"
      :class="[
        `segmented-control__list--${appearance}`,
        `segmented-control__list--${size}`,
        { 'segmented-control__list--scrollable': scrollable },
      ]"
      :aria-label="label"
    >
      <TabsTrigger
        v-for="option in options"
        :id="idPrefix ? `${idPrefix}-${option.value}` : undefined"
        :key="option.value"
        class="segmented-control__trigger"
        :value="option.value"
        :disabled="option.disabled"
        :aria-controls="controlsId"
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
  max-width: 100%;
  display: inline-flex;
}
.segmented-control__list {
  display: inline-flex;
  overflow: hidden;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-control);
  background: var(--color-surface);
}
.segmented-control__list--scrollable {
  max-width: 100%;
  overflow-x: auto;
  overscroll-behavior-inline: contain;
}
.segmented-control__list--pills {
  gap: 6px;
  border: 0;
  border-radius: 0;
  background: transparent;
}
.segmented-control__list--compact .segmented-control__trigger {
  min-height: 36px;
  padding: 5px 12px;
}
.segmented-control__trigger {
  flex: none;
  min-height: 0;
  border: 0;
  border-right: 1px solid var(--color-border-strong);
  background: transparent;
  color: var(--color-text-muted);
  padding: 6px 12px;
  font: inherit;
  font-size: var(--text-sm);
  font-weight: 400;
  cursor: pointer;
  transition:
    color var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard),
    opacity var(--duration-fast) var(--easing-standard);
}
.segmented-control__list--touch .segmented-control__trigger {
  min-height: var(--touch-target);
}
.segmented-control__trigger:last-child {
  border-right: 0;
}
.segmented-control__list--pills .segmented-control__trigger {
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text-muted);
  padding: 6px 12px;
  font-size: 12.5px;
}
.segmented-control__list--pills
  .segmented-control__trigger:hover:not(:disabled):not([data-state='active']) {
  border-color: var(--color-text-faint);
  color: var(--color-text);
}
.segmented-control__trigger[data-state='active'] {
  background: var(--color-text);
  color: var(--color-surface);
  font-weight: 560;
}
.segmented-control__list--pills .segmented-control__trigger[data-state='active'] {
  border-color: var(--color-text);
}
.segmented-control__list--drawer {
  border-color: var(--color-border-control);
  border-radius: 5px;
}
.segmented-control__list--drawer .segmented-control__trigger {
  min-width: 52px;
  min-height: 27px;
  height: 27px;
  border: 0;
  border-radius: 5px;
  padding: 0 8px;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.segmented-control__list--drawer
  .segmented-control__trigger:hover:not(:disabled):not([data-state='active']) {
  background: var(--color-surface-sunken);
  color: var(--color-text);
}
.segmented-control__list--drawer .segmented-control__trigger[data-state='active'] {
  background: var(--color-action-soft);
  color: var(--color-action);
  font-weight: 600;
}
.segmented-control__trigger:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}
</style>
