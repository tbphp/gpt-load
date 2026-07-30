<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useId } from 'vue'

withDefaults(
  defineProps<{
    caption: string
    dense?: boolean
    scrollHint?: string
    appearance?: 'card' | 'editorial'
  }>(),
  {
    appearance: 'card',
    scrollHint: undefined,
  },
)

const container = ref<HTMLElement | null>(null)
const table = ref<HTMLTableElement | null>(null)
const overflowing = ref(false)
const identity = useId().replace(/[^a-zA-Z0-9_-]/g, '-')
const scrollHintId = `data-table-${identity}-scroll-hint`
let resizeObserver: ResizeObserver | undefined

function updateOverflow(): void {
  const element = container.value
  overflowing.value = Boolean(element && element.scrollWidth > element.clientWidth + 1)
}

onMounted(() => {
  updateOverflow()
  if (typeof ResizeObserver === 'function' && container.value) {
    resizeObserver = new ResizeObserver(updateOverflow)
    resizeObserver.observe(container.value)
    if (table.value) resizeObserver.observe(table.value)
  }
  window.addEventListener('resize', updateOverflow)
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  window.removeEventListener('resize', updateOverflow)
})
</script>

<template>
  <div
    ref="container"
    data-table-scroll
    class="data-table__container"
    :class="{
      'data-table__container--dense': dense,
      'data-table__container--editorial': appearance === 'editorial',
    }"
    :tabindex="overflowing ? 0 : undefined"
    :aria-label="overflowing ? caption : undefined"
    :aria-describedby="overflowing && scrollHint ? scrollHintId : undefined"
  >
    <table ref="table" class="data-table">
      <caption class="sr-only">
        {{
          caption
        }}
      </caption>
      <slot />
    </table>
    <span v-if="scrollHint" :id="scrollHintId" class="sr-only">{{ scrollHint }}</span>
  </div>
</template>

<style scoped>
.data-table__container {
  max-width: 100%;
  overflow-x: auto;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-surface);
  box-shadow: var(--shadow-card);
  overscroll-behavior-inline: contain;
}
.data-table__container--editorial {
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
}
.data-table {
  width: max-content;
  min-width: 100%;
  border-collapse: collapse;
  font-size: 0.8125rem;
}
.data-table :deep(th) {
  border-bottom: 1px solid var(--color-border-subtle);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: 9px 12px;
  font-size: 0.6875rem;
  font-weight: 700;
  letter-spacing: 0.06em;
  text-align: left;
  text-transform: uppercase;
  white-space: nowrap;
}
.data-table :deep(td) {
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 8px 12px;
  vertical-align: middle;
}
.data-table :deep(tbody tr:last-child td) {
  border-bottom: 0;
}
.data-table__container--editorial .data-table :deep(th) {
  background: transparent;
  color: var(--color-text-faint);
  padding-block: var(--space-2);
  font-size: var(--text-xs);
  font-weight: 500;
  letter-spacing: 0.04em;
  text-transform: none;
}
.data-table__container--editorial .data-table :deep(td) {
  padding-block: var(--space-3);
}
.data-table :deep(:is(th, td):first-child) {
  padding-left: var(--space-4);
}
.data-table :deep(:is(th, td):last-child) {
  padding-right: var(--space-4);
}
.data-table__container--editorial .data-table :deep(:is(th, td):first-child) {
  padding-left: 0;
}
.data-table__container--editorial .data-table :deep(:is(th, td):last-child) {
  padding-right: 0;
}

.data-table :deep([data-column-priority='high']) {
  position: relative;
}
@media (max-width: 759px) {
  .data-table :deep([data-column-priority='low']) {
    display: none;
  }
}
</style>
