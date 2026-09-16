<script setup lang="ts">
import { Check } from '@lucide/vue'
import AppIcon from './AppIcon.vue'

defineProps<{
  label: string
  image: string
  selected?: boolean
  selectedLabel?: string
  disabled?: boolean
}>()
</script>

<template>
  <button class="modern-choice-card" type="button" :aria-pressed="selected" :disabled="disabled">
    <img :src="image" alt="" width="320" height="180" />
    <span class="modern-choice-card-label">
      <strong>{{ label }}</strong>
      <span v-if="selected" class="modern-choice-card-selected">
        <AppIcon :icon="Check" size="sm" />{{ selectedLabel }}
      </span>
    </span>
  </button>
</template>

<style scoped>
.modern-choice-card {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: var(--modern-space-3);
  border: var(--modern-line-width) solid var(--modern-control-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
  padding: var(--modern-space-2);
  color: var(--modern-text);
  text-align: left;
}
.modern-choice-card:hover:not(:disabled) {
  border-color: var(--modern-control-border-hover);
  background: var(--modern-control-hover);
}
.modern-choice-card[aria-pressed='true'] {
  border-color: var(--modern-accent);
  background: var(--modern-accent-soft);
}
.modern-choice-card:focus-visible {
  box-shadow: var(--modern-shadow-focus);
}
.modern-choice-card img {
  width: 100%;
  height: auto;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-small);
}
.modern-choice-card-label {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
  padding: var(--modern-space-1) var(--modern-space-1);
  font-size: var(--modern-font-size-secondary);
}
.modern-choice-card-label strong {
  font-weight: var(--modern-weight-medium);
}
.modern-choice-card-selected {
  display: inline-flex;
  align-items: center;
  gap: var(--modern-space-1);
  color: var(--modern-accent);
  font-size: var(--modern-font-size-caption);
}
</style>
