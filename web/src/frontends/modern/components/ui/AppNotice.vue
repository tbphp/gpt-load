<script setup lang="ts">
withDefaults(
  defineProps<{ tone?: 'info' | 'success' | 'warning' | 'danger'; bordered?: boolean }>(),
  { tone: 'info' },
)
</script>

<template>
  <div
    class="modern-notice"
    :class="[`modern-notice--${tone}`, { 'modern-notice--bordered': bordered }]"
    :role="tone === 'danger' ? 'alert' : 'status'"
  >
    <div class="modern-notice-message"><slot /></div>
    <div v-if="$slots.actions" class="modern-notice-actions"><slot name="actions" /></div>
  </div>
</template>

<style scoped>
.modern-notice {
  --modern-notice-color: var(--modern-info);
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-3);
  color: var(--modern-notice-color);
  font-size: var(--modern-font-size-body);
}
.modern-notice--success {
  --modern-notice-color: var(--modern-success);
}
.modern-notice--warning {
  --modern-notice-color: var(--modern-warning);
}
.modern-notice--danger {
  --modern-notice-color: var(--modern-danger);
}
.modern-notice--bordered {
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  padding: var(--modern-space-3) var(--modern-space-4);
}
.modern-notice-message {
  min-width: 0;
  flex: 1;
  overflow-wrap: anywhere;
}
.modern-notice-actions {
  flex-shrink: 0;
}
</style>
