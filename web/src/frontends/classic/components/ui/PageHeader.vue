<script setup lang="ts">
withDefaults(
  defineProps<{
    id?: string
    title: string
    appearance?: 'default' | 'ledger'
  }>(),
  { id: undefined, appearance: 'default' },
)
</script>

<template>
  <header class="page-header" :class="`page-header--${appearance}`">
    <h1 :id="id">{{ title }}</h1>
    <div v-if="$slots.actions" class="page-header__actions"><slot name="actions" /></div>
  </header>
</template>

<style scoped>
.page-header {
  display: flex;
  min-height: 64px;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-6);
  border-bottom: 1px solid var(--color-border-control);
  padding-bottom: var(--space-5);
}

.page-header h1 {
  max-width: none;
  font-family: var(--font-sans);
  font-size: 24px;
  font-weight: 700;
  letter-spacing: -0.02em;
  line-height: var(--line-compact);
}

.page-header__actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.page-header--ledger {
  display: grid;
  min-height: 0;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-6);
  padding-bottom: var(--space-5);
}

.page-header--ledger h1 {
  font-family: var(--font-serif);
  font-size: var(--title-lede);
  font-weight: 500;
  line-height: var(--line-compact);
}

@media (max-width: 560px) {
  .page-header h1 {
    font-size: 22px;
  }
}

@media (max-width: 680px) {
  .page-header--ledger {
    grid-template-columns: minmax(0, 1fr);
    gap: 15px;
  }

  .page-header--ledger h1 {
    font-size: var(--title-lede);
  }
}
</style>
