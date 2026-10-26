<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { priceFields, type PriceSlots } from '@modern/api/models'
import { AppOverflowText } from '@modern/components/ui'
import { modelUnitPrice } from './models-display'

defineProps<{ prices: PriceSlots; columns?: boolean }>()
const { t, locale } = useI18n()
</script>

<template>
  <span class="modern-price-summary" :class="{ 'is-columns': columns }">
    <span v-for="field in priceFields" :key="field">
      <span class="modern-price-summary-label">{{ t('modelManager.slots.' + field) }}</span>
      <AppOverflowText
        :class="{ 'modern-price-summary-value': prices[field] !== null }"
        :text="modelUnitPrice(prices[field], locale)"
      />
    </span>
  </span>
</template>

<style scoped>
.modern-price-summary {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: var(--modern-space-1) var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
}
.modern-price-summary > span {
  display: inline-flex;
  gap: var(--modern-space-1);
  white-space: nowrap;
}
.modern-price-summary-label {
  font-size: var(--modern-font-size-caption);
}
.modern-price-summary-value {
  color: var(--modern-text);
}
.modern-price-summary.is-columns {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  width: 100%;
  gap: var(--modern-space-1) var(--modern-space-2);
}
.is-columns > span {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: baseline;
  min-width: 0;
}
@container price-targets (max-width: 400px) {
  .modern-price-summary.is-columns {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
