<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { ModelPriceSlotsDto } from '@/app/resources/model-prices'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import { modelPriceFields } from './model-price-form'

defineProps<{ prices: ModelPriceSlotsDto; columns?: boolean }>()
const { t } = useI18n()
</script>

<template>
  <span class="price-summary" :class="{ 'is-columns': columns }">
    <span v-for="field in modelPriceFields" :key="field"
      ><span class="price-summary-label">{{ t('modelPrices.fields.' + field) }}</span
      ><OverflowTooltip
        as="span"
        :class="{ 'price-summary-value': prices[field] !== null }"
        :content="prices[field] ?? '—'"
        >{{ prices[field] ?? '—' }}</OverflowTooltip
      ></span
    >
  </span>
</template>

<style scoped>
.price-summary {
  display: inline-flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--space-1) var(--space-3);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  font-variant-numeric: tabular-nums;
}
.price-summary > span {
  display: inline-flex;
  gap: var(--space-1);
  white-space: nowrap;
}
.price-summary-label {
  font-size: var(--text-label-xs);
}
.price-summary-value {
  color: var(--color-text);
}
.price-summary.is-columns {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  width: 100%;
  gap: var(--space-1) var(--space-2);
}
.is-columns > span {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: baseline;
  min-width: 0;
}
@container classic-price-targets (max-width: 400px) {
  .price-summary.is-columns {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
