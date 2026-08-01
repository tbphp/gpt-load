<script setup lang="ts">
import { Info } from '@lucide/vue'

defineProps<{ message: string }>()
</script>

<template>
  <div class="model-discovery-notice">
    <Info :size="16" aria-hidden="true" />
    <span>{{ message }}</span>
  </div>
</template>

<style scoped>
.model-discovery-notice {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin-top: 18px;
  border: 1px solid var(--color-border-subtle);
  border-radius: 8px;
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: 10px 12px;
  font-size: var(--text-sm);
}

.model-discovery-notice > svg {
  flex: none;
}
</style>
