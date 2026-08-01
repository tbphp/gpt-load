<script setup lang="ts">
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

.collection-filter-search-control {
  position: relative;
  display: block;
  min-width: 0;
}

.collection-filter-search-control > svg {
  position: absolute;
  top: 50%;
  left: 11px;
  transform: translateY(-50%);
  color: var(--color-text-faint);
  pointer-events: none;
}

.collection-filter-control {
  width: 100%;
  min-width: 0;
  height: 32px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  appearance: none;
  background: var(--color-surface);
  color: var(--color-text);
  padding: 0 10px;
  font: inherit;
  font-size: var(--text-meta);
}

.collection-filter-control:hover {
  border-color: var(--color-text-faint);
}

.collection-filter-control::placeholder {
  color: var(--color-text-faint);
}

.collection-filter-search-control .collection-filter-control {
  padding-right: 38px;
  padding-left: 34px;
}

.collection-filter-search-clear {
  position: absolute;
  top: 2px;
  right: 3px;
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

  .collection-filter-control,
  .collection-filter-field .app-select__trigger {
    height: var(--touch-target);
  }

  .collection-filter-search-clear {
    top: 0;
    right: 0;
    width: var(--touch-target);
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

  .collection-filter-control,
  .collection-filter-field .app-select__trigger,
  .collection-filter-search-clear {
    font-size: 16px;
  }

  .collection-filter-bar__result {
    align-items: flex-start;
    flex-direction: column;
    gap: var(--space-1);
  }
}
</style>
