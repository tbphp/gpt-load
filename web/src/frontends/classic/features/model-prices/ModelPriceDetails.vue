<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ModelPriceDto } from '@/app/resources/model-prices'
import { modelPriceFields } from './model-price-form'

const props = defineProps<{
  price: Pick<ModelPriceDto, 'prices' | 'context_tiers' | 'mode_schedules'>
}>()
const { t } = useI18n()
const rows = computed(() =>
  [
    { ...props.price, name: t('modelPrices.matrix.baseRow') },
    ...Object.entries(props.price.mode_schedules).map(([mode, schedule]) => ({
      ...schedule,
      name:
        mode === 'fast'
          ? t('models.drawer.fastPrices')
          : mode === 'ultrafast'
            ? t('models.drawer.ultrafastPrices')
            : mode,
    })),
  ].flatMap((schedule) => [
    { label: schedule.name, prices: schedule.prices },
    ...schedule.context_tiers.map((tier) => ({
      label: `${schedule.name} ≥ ${tier.threshold_tokens}`,
      prices: tier.prices,
    })),
  ]),
)
</script>

<template>
  <div class="model-price-details">
    <table>
      <thead>
        <tr>
          <th scope="col">
            <span class="sr-only">{{ t('models.drawer.prices') }}</span>
          </th>
          <th v-for="field in modelPriceFields" :key="field" scope="col">
            {{ t('modelPrices.fields.' + field) }}
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in rows" :key="row.label">
          <th scope="row">{{ row.label }}</th>
          <td v-for="field in modelPriceFields" :key="field">{{ row.prices[field] ?? '—' }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.model-price-details {
  overflow-x: auto;
}
.model-price-details table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--text-sm);
}
.model-price-details th,
.model-price-details td {
  padding: var(--space-2);
  border-bottom: 1px solid var(--color-border-subtle);
  text-align: right;
  font-weight: 400;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.model-price-details th:first-child {
  text-align: left;
}
.model-price-details thead {
  color: var(--color-text-muted);
}
</style>
