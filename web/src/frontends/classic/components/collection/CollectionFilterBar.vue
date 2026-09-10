<script setup lang="ts">
defineOptions({ inheritAttrs: false })

withDefaults(
  defineProps<{
    label: string
    showResult?: boolean
    singleColumn?: boolean
    appearance?: 'default' | 'detail'
  }>(),
  {
    showResult: false,
    singleColumn: false,
    appearance: 'default',
  },
)
</script>

<template>
  <form
    v-bind="$attrs"
    class="collection-filter-bar"
    :class="[
      `collection-filter-bar--${appearance}`,
      { 'collection-filter-bar--single': singleColumn },
    ]"
    role="search"
    :aria-label="label"
    autocomplete="off"
    @submit.prevent
  >
    <slot />
  </form>

  <div v-if="showResult" class="collection-filter-bar__result">
    <slot name="result" />
  </div>
</template>

<style>
.collection-filter-bar {
  display: grid;
  grid-template-columns: minmax(260px, 1fr) 204px 148px;
  align-items: end;
  gap: 10px;
  padding: 22px 0 13px;
}

.collection-filter-bar.collection-filter-bar--single {
  grid-template-columns: minmax(0, 1fr);
}

.collection-filter-bar.collection-filter-bar--detail {
  padding-top: 13px;
}

.collection-filter-field {
  display: grid;
  min-width: 0;
  gap: 5px;
}

.collection-filter-label {
  color: var(--color-text-faint);
  font-size: var(--text-meta);
}

.collection-filter-field .app-select__trigger {
  width: 100%;
  height: 32px;
}

.collection-filter-field--monospace .app-select__trigger {
  font-family: var(--font-mono);
}

.collection-filter-bar__result {
  display: flex;
  min-height: 32px;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  border-bottom: 1px solid var(--color-border-control);
  color: var(--color-text-faint);
  padding: 0 0 9px;
  font-size: var(--text-sm);
}

@media (max-width: 860px) {
  .collection-filter-bar {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    padding-top: 17px;
  }

  .collection-filter-field--search {
    grid-column: 1 / -1;
  }

  .collection-filter-field .app-select__trigger {
    height: var(--touch-target);
  }

  .collection-filter-bar__result .app-button {
    min-height: var(--touch-target);
  }
}

@media (max-width: 560px) {
  .collection-filter-bar {
    grid-template-columns: 1fr;
  }

  .collection-filter-field--search {
    grid-column: auto;
  }

  .collection-filter-field .app-select__trigger {
    font-size: 16px;
  }

  .collection-filter-bar__result {
    align-items: flex-start;
    flex-direction: column;
    gap: var(--space-1);
  }
}
</style>
