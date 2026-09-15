<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ModelPrice, PriceSlots } from '@modern/api/models'
import { priceFields } from '@modern/api/models'
import { formatCompactNumber } from '@modern/components/ui/format'
import { modelUnitPrice } from './models-display'

const props = defineProps<{ price: ModelPrice }>()
const { t, locale } = useI18n()
const modeLabel = (mode: string) =>
  ['standard', 'fast', 'ultrafast'].includes(mode) ? t('modelManager.' + mode) : mode
// 摊平成表格行：四个价格的表头只写一次，标准档与各阶梯档上下对齐可比。
const rows = computed(() => {
  const schedules = [
    { mode: 'standard', prices: props.price.prices, context_tiers: props.price.context_tiers },
    ...Object.entries(props.price.mode_schedules).map(([mode, schedule]) => ({
      mode,
      ...schedule,
    })),
  ]
  const multiMode = schedules.length > 1
  return schedules.flatMap((schedule) => [
    {
      key: schedule.mode,
      label: multiMode ? modeLabel(schedule.mode) : t('modelManager.basePrices'),
      prices: schedule.prices,
      tier: false,
    },
    ...schedule.context_tiers.map((item) => ({
      key: `${schedule.mode}-${item.threshold_tokens}`,
      label: '> ' + formatCompactNumber(item.threshold_tokens, locale.value),
      prices: item.prices,
      tier: true,
    })),
  ])
})
const value = (prices: PriceSlots, field: (typeof priceFields)[number]) =>
  modelUnitPrice(prices[field], locale.value)
</script>

<template>
  <div class="modern-model-price-table">
    <div class="modern-model-price-row modern-model-price-row--head">
      <span class="modern-model-price-unit">{{ t('modelManager.unit') }}</span>
      <span v-for="field in priceFields" :key="field">{{ t('modelManager.slots.' + field) }}</span>
    </div>
    <div
      v-for="row in rows"
      :key="row.key"
      class="modern-model-price-row"
      :class="{ 'is-tier': row.tier }"
    >
      <span class="modern-model-price-label">{{ row.label }}</span>
      <span
        v-for="field in priceFields"
        :key="field"
        :class="{ 'is-empty': row.prices[field] === null }"
        >{{ value(row.prices, field) }}</span
      >
    </div>
  </div>
</template>

<style scoped>
/* 单位说明占住表头首列，否则标签列会空出近一半宽度。
   不加外框与底色：540px 侧栏里那会显得过重。 */
.modern-model-price-table {
  display: grid;
  font-variant-numeric: tabular-nums;
}
.modern-model-price-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) repeat(4, minmax(64px, auto));
  align-items: center;
  gap: var(--modern-space-2);
  min-height: var(--modern-space-8);
}
.modern-model-price-row > :not(.modern-model-price-label, .modern-model-price-unit) {
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-small);
  text-align: right;
  white-space: nowrap;
}
.modern-model-price-row--head {
  min-height: var(--modern-space-6);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  color: var(--modern-muted);
}
.modern-model-price-row--head > * {
  font-family: inherit;
  font-size: var(--modern-font-size-caption);
  letter-spacing: var(--modern-tracking-label);
}
.modern-model-price-unit {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
/* 前一项必须也是数据行：否则紧跟表头的第一行会画出上边框，
   与表头自己的下边框叠在同一像素上，那条线就显得格外粗。 */
.modern-model-price-row:not(.modern-model-price-row--head) + .modern-model-price-row {
  border-top: var(--modern-line-width) solid
    color-mix(in srgb, var(--modern-border) 45%, transparent);
}
.modern-model-price-label {
  overflow: hidden;
  color: var(--modern-text);
  font-size: var(--modern-font-size-small);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.modern-model-price-row.is-tier .modern-model-price-label {
  padding-inline-start: var(--modern-space-3);
  color: var(--modern-muted);
}
.modern-model-price-row .is-empty {
  color: var(--modern-control-placeholder);
}
</style>
