<script setup lang="ts">
import { useLoadingActivity } from '@modern/components/ui/loading'
import './credential-card.css'
const props = defineProps<{ selected: boolean; pending?: boolean; compact?: boolean }>()
useLoadingActivity(() => Boolean(props.pending))
</script>
<template>
  <article
    class="modern-credential-card modern-credential-surface"
    :class="{ 'is-selected': selected, 'is-compact': compact }"
    :aria-busy="pending || undefined"
  >
    <header class="modern-credential-card-heading"><slot name="heading" /></header>
    <div class="modern-credential-card-body"><slot /></div>
    <footer class="modern-credential-card-footer modern-credential-surface-footer">
      <slot name="footer" />
    </footer>
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
  padding: var(--modern-space-1) var(--modern-credential-card-inset);
  border-top: var(--modern-line-width) solid var(--modern-border);
  font-size: var(--modern-font-size-small);
  color: var(--modern-muted);
}
.modern-credential-card.is-compact {
  --modern-credential-card-inset: var(--modern-space-3);
  height: var(--modern-key-card-height);
  display: grid;
  grid-template-rows: calc(var(--modern-control-xs) + var(--modern-space-2)) minmax(0, 1fr) var(
      --modern-control-sm
    );
}
.is-compact .modern-credential-card-heading {
  padding-top: var(--modern-space-1);
}
.is-compact .modern-credential-card-body {
  min-height: 0;
  align-content: center;
  gap: var(--modern-space-1);
  padding-block: var(--modern-space-1);
}
.is-compact .modern-credential-card-footer {
  flex-wrap: nowrap;
  gap: var(--modern-space-1);
  padding-block: var(--modern-space-0-5);
}
@media (max-width: 760px) {
  .modern-credential-card.is-compact {
    height: calc(var(--modern-key-card-height) + var(--modern-space-10));
    grid-template-rows:
      calc(var(--modern-touch-target) + var(--modern-space-2)) minmax(0, 1fr)
      calc(var(--modern-touch-target) + var(--modern-space-2));
  }
}
</style>
