<script setup lang="ts">
import AppButton from '@/components/ui/AppButton.vue'

defineProps<{
  label: string
  detail: string
  sourceLabel: string
  actionLabel: string
  overridden: boolean
  disabled?: boolean
}>()

const emit = defineEmits<{ toggle: [] }>()
</script>

<template>
  <div class="runtime-override-row">
    <div class="runtime-override-row__identity">
      <strong>{{ label }}</strong>
      <small>{{ detail }}</small>
    </div>
    <span class="runtime-override-row__source" :data-overridden="overridden || undefined">
      {{ sourceLabel }}
    </span>
    <div v-if="$slots.value" class="runtime-override-row__value"><slot name="value" /></div>
    <AppButton variant="secondary" size="lg" :disabled="disabled" @click="emit('toggle')">
      {{ actionLabel }}
    </AppButton>
  </div>
</template>

<style scoped>
.runtime-override-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(0, auto) auto;
  align-items: center;
  gap: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  padding: var(--space-3);
}
.runtime-override-row__identity {
  display: grid;
  gap: var(--space-1);
}
.runtime-override-row__identity small {
  color: var(--color-text-muted);
}
.runtime-override-row__source {
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-tag);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: 4px 8px;
  font-size: var(--text-label-xs);
  white-space: nowrap;
}
.runtime-override-row__source[data-overridden='true'] {
  border-color: var(--color-action);
  background: var(--color-action-soft);
  color: var(--color-action);
}
.runtime-override-row__value {
  min-width: 112px;
}
@media (max-width: 680px) {
  .runtime-override-row {
    grid-template-columns: minmax(0, 1fr) auto;
  }
  .runtime-override-row__identity {
    grid-column: 1 / -1;
  }
  .runtime-override-row__value {
    min-width: 0;
  }
}
</style>
