<script setup lang="ts">
import { computed } from 'vue'

type CollectionStatusSummaryTone = 'neutral' | 'success' | 'warning' | 'danger'

interface CollectionStatusSummaryItem {
  value?: string
  label: string
  count: number
  tone: CollectionStatusSummaryTone
}

const props = defineProps<{
  total: number
  items: readonly CollectionStatusSummaryItem[]
  modelValue?: string
  label: string
  totalLabel: string
  appearance?: 'default' | 'detail'
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string | undefined]
}>()

const barItems = computed(() =>
  props.items.filter((item) => item.value !== undefined && item.count > 0),
)
</script>

<template>
  <section
    class="collection-status-summary"
    :class="`collection-status-summary--${appearance ?? 'default'}`"
    :aria-label="label"
  >
    <div class="collection-status-summary__total">
      <div class="collection-status-summary__label">{{ totalLabel }}</div>
      <div class="collection-status-summary__number">{{ total }}</div>
    </div>

    <div class="collection-status-summary__detail">
      <div class="collection-status-summary__bar" aria-hidden="true">
        <span
          v-for="item in barItems"
          :key="item.value"
          class="collection-status-summary__segment"
          :class="`collection-status-summary__segment--${item.tone}`"
          :style="{ flexGrow: item.count }"
        ></span>
      </div>

      <div
        class="collection-status-summary__filters"
        :style="{ '--collection-status-summary-columns': Math.max(items.length, 1) }"
      >
        <button
          v-for="item in items"
          :key="item.value ?? 'all'"
          class="collection-status-summary__filter"
          type="button"
          :aria-pressed="modelValue === item.value"
          @click="emit('update:modelValue', item.value)"
        >
          <span
            class="collection-status-summary__dot"
            :class="`collection-status-summary__dot--${item.tone}`"
            aria-hidden="true"
          ></span>
          <span class="collection-status-summary__filter-label">{{ item.label }}</span>
          <span class="collection-status-summary__filter-value">{{ item.count }}</span>
        </button>
      </div>
    </div>
  </section>
</template>

<style scoped>
.collection-status-summary {
  display: grid;
  grid-template-columns: var(--status-overview-total-width) minmax(0, 1fr);
  gap: var(--space-7);
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 18px 0;
}
.collection-status-summary--detail {
  padding-top: 0;
}

.collection-status-summary__label {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  letter-spacing: 0.06em;
  text-transform: uppercase;
}

.collection-status-summary__number {
  margin-top: 3px;
  font-family: var(--font-mono);
  font-size: 30px;
  font-weight: 520;
  letter-spacing: -0.04em;
  line-height: 1.1;
}

.collection-status-summary__detail {
  min-width: 0;
}

.collection-status-summary__bar {
  display: flex;
  height: var(--status-bar-height);
  overflow: hidden;
  border-radius: 2px;
  background: var(--color-neutral-bg);
}

.collection-status-summary__segment {
  min-width: 0;
  flex-basis: 0;
}

.collection-status-summary__segment + .collection-status-summary__segment {
  border-left: 2px solid var(--color-surface);
}

.collection-status-summary__segment--success,
.collection-status-summary__dot--success {
  background: var(--color-success);
}

.collection-status-summary__segment--warning,
.collection-status-summary__dot--warning {
  background: var(--color-warning);
}

.collection-status-summary__segment--danger,
.collection-status-summary__dot--danger {
  background: var(--color-danger);
}

.collection-status-summary__segment--neutral,
.collection-status-summary__dot--neutral {
  background: var(--color-neutral);
}

.collection-status-summary__filters {
  display: grid;
  grid-template-columns: repeat(var(--collection-status-summary-columns), minmax(0, 1fr));
  margin-top: var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
  border-bottom: 1px solid var(--color-border-subtle);
}

.collection-status-summary__filter {
  position: relative;
  display: grid;
  min-width: 0;
  min-height: 40px;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 7px;
  border: 0;
  appearance: none;
  background: transparent;
  color: var(--color-text-muted);
  padding: 10px 12px 11px;
  font: inherit;
  text-align: left;
  cursor: pointer;
  transition:
    color var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard);
}

.collection-status-summary__filter + .collection-status-summary__filter {
  border-left: 1px solid var(--color-border-subtle);
}

.collection-status-summary__filter::after {
  position: absolute;
  right: 12px;
  bottom: -1px;
  left: 12px;
  height: 2px;
  background: transparent;
  content: '';
}

.collection-status-summary__filter:hover {
  background: var(--color-interactive-hover);
  color: var(--color-text);
}

.collection-status-summary__filter[aria-pressed='true'] {
  color: var(--color-text);
  font-weight: 600;
}

.collection-status-summary__filter[aria-pressed='true']::after {
  background: var(--color-action);
}

.collection-status-summary__dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
}

.collection-status-summary__filter-label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.collection-status-summary__filter-value {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-weight: 400;
}

.collection-status-summary__filter[aria-pressed='true'] .collection-status-summary__filter-value {
  color: var(--color-text-muted);
}

@media (max-width: 860px) {
  .collection-status-summary {
    grid-template-columns: 1fr;
    gap: 14px;
  }

  .collection-status-summary__filters {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .collection-status-summary__filter {
    min-height: var(--touch-target);
  }

  .collection-status-summary__filter:nth-child(odd) {
    border-left: 0;
  }

  .collection-status-summary__filter:nth-child(n + 3) {
    border-top: 1px solid var(--color-border-subtle);
  }

  .collection-status-summary__filter:last-child:nth-child(odd) {
    grid-column: 1 / -1;
  }
}
</style>
