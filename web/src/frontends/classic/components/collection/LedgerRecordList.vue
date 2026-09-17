<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, onUpdated, ref } from 'vue'

interface LedgerRecordListProps {
  label: string
  rowCount?: number
  scrollHint?: string
  /**
   * Page-owned class applied to the list root. Define `--ledger-record-list-grid`
   * on it for the desktop columns and, when needed,
   * `--ledger-record-list-card-grid` for the mobile card layout.
   */
  gridClass?: string
}

const props = defineProps<LedgerRecordListProps>()

const container = ref<HTMLElement | null>(null)
const overflowing = ref(false)
const accessibleLabel = computed(() =>
  overflowing.value && props.scrollHint ? `${props.label} · ${props.scrollHint}` : props.label,
)
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
  }
  window.addEventListener('resize', updateOverflow)
})

onUpdated(updateOverflow)

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  window.removeEventListener('resize', updateOverflow)
})
</script>

<template>
  <div
    ref="container"
    class="ledger-record-list"
    :class="gridClass"
    role="table"
    :aria-label="accessibleLabel"
    :aria-rowcount="rowCount"
    :tabindex="overflowing ? 0 : undefined"
  >
    <div class="ledger-record-list__header" role="row" aria-rowindex="1">
      <slot name="header" />
    </div>
    <slot />
  </div>
</template>

<style>
.ledger-record-list {
  display: grid;
  grid-template-columns: var(--ledger-record-list-grid, minmax(0, 1fr));
  column-gap: var(--ledger-record-list-column-gap, 16px);
  overflow-x: auto;
  overflow-y: hidden;
  border-bottom: 1px solid var(--color-border-control);
}

.ledger-record-list__header,
.ledger-record-list__record {
  display: grid;
  grid-column: 1 / -1;
  grid-template-columns: subgrid;
  align-items: center;
}

.ledger-record-list__header {
  min-height: 38px;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
  font-weight: 500;
  letter-spacing: 0.04em;
}

.ledger-record-list__header > span,
.ledger-record-list__record > .ledger-record-list__cell {
  justify-self: stretch;
  text-align: left;
}

.ledger-record-list__record {
  position: relative;
  min-height: var(--ledger-record-list-record-min-height, 96px);
  border-top: 1px solid var(--color-border-subtle);
  padding: var(--ledger-record-list-record-padding, 14px 0);
  transition: background-color var(--duration-fast) var(--easing-standard);
}

.ledger-record-list__record:first-of-type {
  border-top-color: var(--color-border-control);
}

.ledger-record-list__record:hover {
  background: var(--color-surface-sunken);
}

.ledger-record-list__cell {
  min-width: 0;
}

@media (max-width: 860px) {
  .ledger-record-list {
    grid-template-columns: minmax(0, 1fr);
    gap: 10px;
    overflow: visible;
    border: 0;
    padding-top: 10px;
  }

  .ledger-record-list__header {
    display: none;
  }

  .ledger-record-list__record {
    display: grid;
    grid-column: 1;
    min-height: 0;
    grid-template-columns: var(--ledger-record-list-card-grid, minmax(0, 0.48fr) minmax(0, 1.52fr));
    align-items: start;
    gap: 14px 16px;
    border: 1px solid var(--color-border-subtle);
    border-radius: var(--radius-control);
    background: var(--color-surface);
    padding: 16px;
  }

  .ledger-record-list__record:first-of-type {
    border-top-color: var(--color-border-subtle);
  }

  .ledger-record-list__record:hover {
    background: var(--color-surface);
  }
}

@media (max-width: 560px) {
  .ledger-record-list__record {
    padding: 14px 13px;
  }
}
</style>
