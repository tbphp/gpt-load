<script setup lang="ts">
const props = withDefaults(
  defineProps<{
    summary: string
    open?: boolean
    indicatorPosition?: 'end' | 'inline'
  }>(),
  { indicatorPosition: 'end' },
)
const emit = defineEmits<{ 'update:open': [open: boolean] }>()

function handleToggle(event: Event): void {
  emit('update:open', (event.currentTarget as HTMLDetailsElement).open)
}
</script>

<template>
  <details class="disclosure-panel" :open="open" @toggle="handleToggle">
    <summary>
      <span class="disclosure-panel__summary-content">
        <slot name="summary">{{ summary }}</slot>
        <span
          v-if="props.indicatorPosition === 'inline'"
          class="disclosure-panel__summary-indicator"
          aria-hidden="true"
        ></span>
      </span>
      <span
        v-if="props.indicatorPosition === 'end'"
        class="disclosure-panel__summary-indicator"
        aria-hidden="true"
      ></span>
    </summary>
    <div class="disclosure-panel__body">
      <slot />
    </div>
  </details>
</template>

<style scoped>
.disclosure-panel {
  border-top: 1px solid var(--color-border-subtle);
  padding-top: 14px;
}

.disclosure-panel summary {
  display: flex;
  min-height: var(--control-sm);
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border-radius: var(--radius-control);
  color: var(--color-text-muted);
  cursor: pointer;
  font-size: var(--text-sm);
  font-weight: 600;
  list-style: none;
  padding: 5px 8px;
  transition:
    background-color var(--duration-fast) var(--easing-standard),
    color var(--duration-fast) var(--easing-standard);
}

.disclosure-panel summary::-webkit-details-marker {
  display: none;
}

.disclosure-panel summary:hover {
  background: var(--color-surface-sunken);
  color: var(--color-text);
}

.disclosure-panel summary:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}

.disclosure-panel__summary-content {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
}

.disclosure-panel__summary-indicator {
  position: relative;
  display: grid;
  width: 20px;
  height: 20px;
  flex: none;
  place-items: center;
  color: var(--color-text-faint);
}

.disclosure-panel__summary-indicator::before,
.disclosure-panel__summary-indicator::after {
  position: absolute;
  width: 10px;
  height: 1.5px;
  border-radius: 2px;
  background: currentColor;
  content: '';
  transition: transform var(--duration-fast) var(--easing-standard);
}

.disclosure-panel__summary-indicator::after {
  transform: rotate(90deg);
}

.disclosure-panel[open] .disclosure-panel__summary-indicator::after {
  transform: rotate(0deg);
}

.disclosure-panel__body {
  margin-top: 11px;
}

@media (prefers-reduced-motion: reduce) {
  .disclosure-panel summary,
  .disclosure-panel__summary-indicator::before,
  .disclosure-panel__summary-indicator::after {
    transition: none;
  }
}
</style>
