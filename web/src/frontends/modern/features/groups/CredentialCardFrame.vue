<script setup lang="ts">
import { AppLoadingIndicator } from '@modern/components/ui'
defineProps<{ selected: boolean; pending?: boolean; compact?: boolean }>()
</script>
<template>
  <article
    class="modern-credential-card"
    :class="{ 'is-selected': selected, 'is-compact': compact }"
    :aria-busy="pending || undefined"
  >
    <AppLoadingIndicator :loading="pending" />
    <header class="modern-credential-card-heading"><slot name="heading" /></header>
    <div class="modern-credential-card-body"><slot /></div>
    <footer class="modern-credential-card-footer"><slot name="footer" /></footer>
  </article>
</template>
<style scoped>
.modern-credential-card {
  container: modern-credential-card / inline-size;
  position: relative;
  display: flex;
  flex-direction: column;
  min-width: 0;
  align-self: start;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
  overflow: hidden;
  transition: border-color var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-credential-card:hover {
  border-color: var(--modern-control-border-hover);
}
.modern-credential-card.is-selected {
  border-color: var(--modern-accent);
}
.modern-credential-card-heading {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  padding: var(--modern-credential-card-inset) var(--modern-credential-card-inset) 0;
  min-width: 0;
}
.modern-credential-card-body {
  display: grid;
  gap: var(--modern-credential-card-gap);
  flex: 1;
  min-width: 0;
  padding: var(--modern-credential-card-gap) var(--modern-credential-card-inset);
}
.modern-credential-card-footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
  padding: var(--modern-space-1-5) var(--modern-credential-card-inset);
  border-top: var(--modern-line-width) solid var(--modern-border);
  background: color-mix(in srgb, var(--modern-subtle) 55%, var(--modern-surface));
  font-size: var(--modern-font-size-small);
  color: var(--modern-muted);
}
.modern-credential-card.is-compact {
  height: var(--modern-key-card-height);
  display: grid;
  grid-template-rows: calc(var(--modern-control-sm) + var(--modern-space-4)) minmax(0, 1fr) calc(
      var(--modern-control-nav) + var(--modern-space-3)
    );
}
.is-compact .modern-credential-card-heading {
  padding-top: var(--modern-space-2);
}
.is-compact .modern-credential-card-body {
  min-height: 0;
  align-content: space-between;
  gap: var(--modern-space-1);
  padding-block: var(--modern-space-2);
}
.is-compact .modern-credential-card-footer {
  flex-wrap: nowrap;
}
@media (max-width: 760px) {
  .modern-credential-card.is-compact {
    height: calc(
      var(--modern-key-card-height) + var(--modern-touch-target) + var(--modern-space-3)
    );
    grid-template-rows:
      calc(var(--modern-touch-target) + var(--modern-space-4)) minmax(0, 1fr)
      calc(var(--modern-touch-target) + var(--modern-space-3));
  }
}
</style>
