<script setup lang="ts">
import { useId } from 'vue'

defineProps<{ title: string; description?: string }>()
const titleId = useId()
</script>

<template>
  <section class="modern-panel" :aria-labelledby="titleId">
    <header class="modern-panel-header">
      <div>
        <h2 :id="titleId">{{ title }}</h2>
        <p v-if="description">{{ description }}</p>
      </div>
      <div v-if="$slots.actions" class="modern-panel-actions"><slot name="actions" /></div>
    </header>
    <div class="modern-panel-body"><slot /></div>
  </section>
</template>

<style scoped>
.modern-panel {
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
}
.modern-panel-header {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  padding: var(--modern-space-4) var(--modern-panel-inset);
  border-radius: var(--modern-radius-panel) var(--modern-radius-panel) 0 0;
}
.modern-panel-header h2 {
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
.modern-panel-header > div:first-child {
  min-width: 0;
}
.modern-panel-header p {
  margin-top: var(--modern-space-1);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-panel-actions {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-panel-body {
  padding: var(--modern-panel-inset);
}
</style>
