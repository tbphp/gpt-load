<script setup lang="ts">
withDefaults(
  defineProps<{
    as?: 'div' | 'main' | 'section' | 'article'
    padded?: boolean
  }>(),
  {
    as: 'div',
    padded: true,
  },
)
</script>

<template>
  <component :is="as" class="ledger-sheet" :class="{ 'ledger-sheet--padded': padded }">
    <slot />
  </component>
</template>

<style scoped>
.ledger-sheet {
  position: relative;
  min-width: 0;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-sheet);
  background: var(--color-surface);
  box-shadow: var(--shadow-sheet);
}

.ledger-sheet--padded {
  padding: var(--sheet-padding-top) var(--sheet-padding-inline) var(--sheet-padding-bottom);
}

@media (max-width: 860px) {
  .ledger-sheet--padded {
    padding: var(--sheet-padding-top-compact) var(--sheet-padding-inline-compact)
      var(--sheet-padding-bottom-compact);
  }
}
</style>
