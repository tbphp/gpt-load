<script setup lang="ts">
import { X } from '@lucide/vue'
import AppIconButton from './AppIconButton.vue'
import AppOverflowText from './AppOverflowText.vue'

defineProps<{
  title: string
  description?: string
  closeLabel: string
  closeDisabled?: boolean
}>()
defineEmits<{ close: [] }>()
</script>

<template>
  <header class="modern-dialog-header">
    <div>
      <h2>{{ title }}</h2>
      <p v-if="description"><AppOverflowText :text="description" /></p>
    </div>
    <AppIconButton
      :icon="X"
      :label="closeLabel"
      :disabled="closeDisabled"
      size="sm"
      @click="$emit('close')"
    />
  </header>
</template>

<style scoped>
.modern-dialog-header {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-4);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  padding: var(--modern-space-4) var(--modern-space-5);
}
.modern-dialog-header > div {
  min-width: 0;
}
.modern-dialog-header h2 {
  color: var(--modern-text);
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
.modern-dialog-header p {
  margin-top: var(--modern-space-1);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  overflow-wrap: anywhere;
}
</style>
