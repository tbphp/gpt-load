<script setup lang="ts">
import { useId } from 'vue'
import AppOverflowText from './AppOverflowText.vue'

defineProps<{ title: string; description?: string; compact?: boolean; flush?: boolean }>()
const titleId = useId()
</script>

<template>
  <section
    class="modern-panel"
    :class="{ 'modern-panel--compact': compact, 'modern-panel--flush': flush }"
    :aria-labelledby="titleId"
  >
    <header class="modern-panel-header">
      <div>
        <h2 :id="titleId"><AppOverflowText :text="title" /></h2>
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
  flex: 1;
}
.modern-panel-header p {
  margin-top: var(--modern-space-1);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-panel-actions {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  max-width: 100%;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-panel-body {
  padding: var(--modern-panel-inset);
}
.modern-panel--compact .modern-panel-header {
  min-height: calc(var(--modern-control-sm) + var(--modern-space-6));
  border-bottom: 0;
  padding: var(--modern-space-3) var(--modern-space-4);
}
.modern-panel--compact .modern-panel-header h2 {
  font-size: var(--modern-font-size-body);
}
.modern-panel--compact .modern-panel-body {
  padding: var(--modern-space-1) var(--modern-space-4) var(--modern-space-4);
}
.modern-panel--flush .modern-panel-body {
  padding: 0;
}
</style>
