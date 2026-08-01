<script setup lang="ts">
import { computed } from 'vue'

import type { GroupCollectionStatus, GroupKeyStatus } from '@/api/control/types'

export type StatusSummaryFilterValue = GroupCollectionStatus | GroupKeyStatus

export interface StatusSummaryCounts {
  total: number
  available: number
  unavailable?: number
  cooldown?: number
  blacklisted?: number
  disabled: number
}

interface StatusSummaryLabels {
  region: string
  current: string
  all: string
  available: string
  unavailable: string
  disabled: string
  cooldown?: string
  blacklisted?: string
  tooltips?: Partial<Record<'all' | StatusSummaryFilterValue, string>>
}

const props = defineProps<{
  summary: StatusSummaryCounts
  modelValue?: StatusSummaryFilterValue
  labels: StatusSummaryLabels
}>()

const emit = defineEmits<{
  'update:modelValue': [status: StatusSummaryFilterValue | undefined]
}>()

const filters = computed(() => [
  { value: undefined, label: props.labels.all, count: props.summary.total, tone: 'neutral' },
  {
    value: 'available' as const,
    label: props.labels.available,
    count: props.summary.available,
    tone: 'available',
  },
  ...(props.labels.cooldown !== undefined && props.summary.cooldown !== undefined
    ? [
        {
          value: 'cooldown' as const,
          label: props.labels.cooldown,
          count: props.summary.cooldown,
          tone: 'cooldown',
        },
      ]
    : []),
  ...(props.labels.blacklisted !== undefined && props.summary.blacklisted !== undefined
    ? [
        {
          value: 'blacklisted' as const,
          label: props.labels.blacklisted,
          count: props.summary.blacklisted,
          tone: 'blacklisted',
        },
      ]
    : []),
  ...(props.summary.unavailable !== undefined
    ? [
        {
          value: 'unavailable' as const,
          label: props.labels.unavailable,
          count: props.summary.unavailable,
          tone: 'unavailable',
        },
      ]
    : []),
  {
    value: 'disabled' as const,
    label: props.labels.disabled,
    count: props.summary.disabled,
    tone: 'disabled',
  },
])
</script>

<template>
  <section class="status-summary" :aria-label="labels.region">
    <div class="status-summary__total">
      <div class="status-summary__label">{{ labels.current }}</div>
      <div class="status-summary__number">{{ summary.total }}</div>
    </div>
    <div class="status-summary__detail">
      <div class="status-summary__bar" aria-hidden="true">
        <span
          v-if="summary.available > 0"
          class="status-summary__segment status-summary__segment--available"
          :style="{ flexGrow: summary.available }"
        ></span>
        <span
          v-if="summary.cooldown && summary.cooldown > 0"
          class="status-summary__segment status-summary__segment--cooldown"
          :style="{ flexGrow: summary.cooldown }"
        ></span>
        <span
          v-if="summary.blacklisted && summary.blacklisted > 0"
          class="status-summary__segment status-summary__segment--blacklisted"
          :style="{ flexGrow: summary.blacklisted }"
        ></span>
        <span
          v-if="summary.unavailable && summary.unavailable > 0"
          class="status-summary__segment status-summary__segment--unavailable"
          :style="{ flexGrow: summary.unavailable }"
        ></span>
        <span
          v-if="summary.disabled > 0"
          class="status-summary__segment status-summary__segment--disabled"
          :style="{ flexGrow: summary.disabled }"
        ></span>
      </div>
      <div class="status-summary__filters">
        <button
          v-for="filter in filters"
          :key="filter.value ?? 'all'"
          class="status-summary__filter"
          type="button"
          :aria-pressed="modelValue === filter.value"
          :title="labels.tooltips?.[filter.value ?? 'all']"
          @click="emit('update:modelValue', filter.value)"
        >
          <span
            class="status-summary__dot"
            :class="`status-summary__dot--${filter.tone}`"
            aria-hidden="true"
          ></span>
          <span class="status-summary__filter-label">{{ filter.label }}</span>
          <span class="status-summary__filter-value">{{ filter.count }}</span>
        </button>
      </div>
    </div>
  </section>
</template>

<style scoped>
.status-summary {
  display: grid;
  grid-template-columns: var(--status-overview-total-width) minmax(0, 1fr);
  gap: var(--space-7);
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 18px 0;
}
.status-summary__label {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  letter-spacing: 0.06em;
  text-transform: uppercase;
}
.status-summary__number {
  margin-top: 3px;
  font-family: var(--font-mono);
  font-size: 30px;
  font-weight: 520;
  letter-spacing: -0.04em;
  line-height: 1.1;
}
.status-summary__detail {
  min-width: 0;
}
.status-summary__bar {
  display: flex;
  height: var(--status-bar-height);
  overflow: hidden;
  border-radius: 2px;
  background: var(--color-neutral-bg);
}
.status-summary__segment {
  min-width: 0;
  flex-basis: 0;
}
.status-summary__segment + .status-summary__segment {
  border-left: 2px solid var(--color-surface);
}
.status-summary__segment--available,
.status-summary__dot--available {
  background: var(--color-success);
}
.status-summary__segment--cooldown,
.status-summary__dot--cooldown {
  background: var(--color-warning);
}
.status-summary__segment--blacklisted,
.status-summary__segment--unavailable,
.status-summary__dot--blacklisted,
.status-summary__dot--unavailable {
  background: var(--color-danger);
}
.status-summary__segment--disabled,
.status-summary__dot {
  background: var(--color-neutral);
}
.status-summary__filters {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  margin-top: var(--space-3);
  border-block: 1px solid var(--color-border-subtle);
}
.status-summary__filter {
  position: relative;
  display: grid;
  min-width: 0;
  min-height: var(--touch-target);
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 7px;
  border: 0;
  border-left: 1px solid var(--color-border-subtle);
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
.status-summary__filter:first-child {
  border-left: 0;
}
.status-summary__filter::after {
  position: absolute;
  right: 12px;
  bottom: -1px;
  left: 12px;
  height: 2px;
  background: transparent;
  content: '';
}
.status-summary__filter:hover {
  background: var(--color-interactive-hover);
  color: var(--color-text);
}
.status-summary__filter[aria-pressed='true'] {
  color: var(--color-text);
  font-weight: 600;
}
.status-summary__filter[aria-pressed='true']::after {
  background: var(--color-action);
}
.status-summary__dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
}
.status-summary__filter-label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.status-summary__filter-value {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-weight: 400;
}
.status-summary__filter[aria-pressed='true'] .status-summary__filter-value {
  color: var(--color-text-muted);
}
@media (max-width: 860px) {
  .status-summary {
    grid-template-columns: 1fr;
    gap: 14px;
  }
}
</style>
