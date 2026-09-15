<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ModelPrice } from '@modern/api/models'
import { priceFields } from '@modern/api/models'
import { AppBadge, AppOverflowText } from '@modern/components/ui'
import { formatCompactNumber } from '@modern/components/ui/format'
import { modelUnitPrice, priceStatus } from './models-display'

const props = defineProps<{ price: ModelPrice; summary?: boolean }>()
const { t, locale } = useI18n()
const schedules = computed(() => [
  { mode: 'standard', prices: props.price.prices, context_tiers: props.price.context_tiers },
  ...(props.summary
    ? []
    : Object.entries(props.price.mode_schedules).map(([mode, schedule]) => ({
        mode,
        ...schedule,
      }))),
])
const modeLabel = (mode: string) =>
  ['standard', 'fast', 'ultrafast'].includes(mode) ? t('modelManager.' + mode) : mode
</script>

<template>
  <div class="modern-model-pricing" :class="{ 'is-summary': summary }">
    <div class="modern-model-pricing-caption">
      <span>{{ t(summary ? 'modelManager.basePrices' : 'modelManager.unit') }}</span>
      <AppBadge
        :tone="price.status === 'pending' ? 'warning' : 'neutral'"
        size="xs"
        variant="plain"
      >
        {{ t('modelManager.priceMethods.' + priceStatus(price)) }}
      </AppBadge>
    </div>
    <div v-for="schedule in schedules" :key="schedule.mode" class="modern-model-price-schedule">
      <div v-if="schedules.length > 1" class="modern-model-mode-title">
        {{ modeLabel(schedule.mode) }}
      </div>
      <dl class="modern-model-price-values">
        <div v-for="field in priceFields" :key="field">
          <dt>{{ t('modelManager.slots.' + field) }}</dt>
          <dd><AppOverflowText :text="modelUnitPrice(schedule.prices[field], locale)" /></dd>
        </div>
      </dl>
      <div v-if="!summary && schedule.context_tiers.length" class="modern-model-tiers">
        <div
          v-for="tier in schedule.context_tiers"
          :key="tier.threshold_tokens"
          class="modern-model-tier"
        >
          <p>
            {{
              t('modelManager.tierFrom', {
                count: formatCompactNumber(tier.threshold_tokens, locale),
              })
            }}
          </p>
          <dl class="modern-model-price-values">
            <div v-for="field in priceFields" :key="field">
              <dt>{{ t('modelManager.slots.' + field) }}</dt>
              <dd><AppOverflowText :text="modelUnitPrice(tier.prices[field], locale)" /></dd>
            </div>
          </dl>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modern-model-pricing {
  display: grid;
  gap: var(--modern-space-2);
}
.modern-model-pricing-caption {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-model-price-schedule {
  display: grid;
  gap: var(--modern-space-3);
}
.modern-model-price-schedule + .modern-model-price-schedule {
  padding-top: var(--modern-space-4);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-model-mode-title {
  font-weight: var(--modern-weight-medium);
  font-size: var(--modern-font-size-secondary);
}
.modern-model-price-values {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--modern-space-3);
  margin: 0;
  padding: var(--modern-space-3);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
}
.modern-model-price-values > div {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-1);
}
.modern-model-price-values dt,
.modern-model-tier > p {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-model-price-values dd {
  min-width: 0;
  margin: 0;
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
  font-variant-numeric: tabular-nums;
}
.modern-model-tiers,
.modern-model-tier {
  display: grid;
  gap: var(--modern-space-2);
}
.modern-model-tier {
  padding-block: var(--modern-space-3);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-model-tier .modern-model-price-values {
  background: transparent;
  padding: 0;
}
.is-summary .modern-model-price-values {
  background: transparent;
  padding: 0;
}
.is-summary .modern-model-pricing-caption {
  font-size: var(--modern-font-size-caption);
}
@media (max-width: 760px) {
  .modern-model-price-values {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
