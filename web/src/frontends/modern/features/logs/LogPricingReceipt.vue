<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { LogPricingLine, LogReceipt } from '@modern/api/logs'
import { AppOverflowText } from '@modern/components/ui'
import { exactLogMoney, logMoney, logNumber } from './log-display'
defineProps<{ receipt: LogReceipt }>()
const { t, te, locale } = useI18n()
function lineName(value: string): string {
  return te('logs.priceLines.' + value) ? t('logs.priceLines.' + value) : value
}
function rate(line: LogPricingLine): string {
  return line.rate_nano_usd_per_million === null
    ? t('logs.values.unpriced')
    : exactLogMoney(line.rate_nano_usd_per_million) + ' / 1M'
}
function adjustment(line: LogPricingLine): string | undefined {
  return line.multiplier.numerator !== line.multiplier.denominator
    ? `× ${line.multiplier.numerator}/${line.multiplier.denominator}`
    : undefined
}
</script>

<template>
  <div class="modern-log-receipt">
    <div class="modern-log-receipt-meta">
      <span>{{ t('logs.pricingModel') }}</span
      ><AppOverflowText :text="receipt.rule.model_id" /><span
        >{{ t('logs.columns.pricing_mode') }} ·
        {{
          te('logs.values.' + receipt.pricing_mode)
            ? t('logs.values.' + receipt.pricing_mode)
            : receipt.pricing_mode
        }}</span
      >
    </div>
    <div class="modern-log-receipt-scroll">
      <div class="modern-log-price-row modern-log-price-head">
        <span>{{ t('logs.priceItem') }}</span
        ><span>{{ t('logs.quantity') }}</span
        ><span>{{ t('logs.unitPrice') }}</span
        ><span>{{ t('logs.amount') }}</span>
      </div>
      <div v-for="line in receipt.line_items" :key="line.code" class="modern-log-price-row">
        <span>{{ lineName(line.code) }}</span>
        <AppOverflowText
          :text="logNumber(line.quantity, locale)"
          :full-text="logNumber(line.quantity, locale, false)"
        />
        <AppOverflowText
          :text="rate(line)"
          :hint="adjustment(line)"
          :full-text="[rate(line), adjustment(line)].filter(Boolean).join(' ')"
        />
        <AppOverflowText
          :text="
            line.amount_nano_usd === null
              ? t('logs.values.unpriced')
              : logMoney(line.amount_nano_usd, locale)
          "
          :full-text="
            line.amount_nano_usd === null ? undefined : exactLogMoney(line.amount_nano_usd)
          "
        />
      </div>
    </div>
    <div v-if="receipt.price_multipliers" class="modern-log-receipt-meta">
      <span>{{ t('logs.groupMultiplier') }} ×{{ receipt.price_multipliers.group }}</span
      ><span>{{ t('logs.keyMultiplier') }} ×{{ receipt.price_multipliers.access_key }}</span>
    </div>
    <div class="modern-log-receipt-total">
      <span v-if="receipt.base_total_nano_usd !== null"
        >{{ t('logs.baseCost') }} {{ exactLogMoney(receipt.base_total_nano_usd) }}</span
      ><span
        >{{ t('logs.totalCost') }}
        <strong>{{ exactLogMoney(receipt.total_nano_usd) }}</strong></span
      >
    </div>
  </div>
</template>

<style scoped>
.modern-log-receipt {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-log-receipt-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-1) var(--modern-space-2);
  min-width: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-log-receipt-meta > :nth-child(2) {
  max-width: 26ch;
}
.modern-log-receipt-scroll {
  overflow-x: auto;
}
.modern-log-price-row {
  display: grid;
  grid-template-columns: 92px 64px minmax(130px, 1fr) 96px;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 414px;
  padding-block: var(--modern-space-1-5);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  color: var(--modern-text);
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
}
.modern-log-price-head {
  color: var(--modern-muted);
}
.modern-log-receipt-total {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-log-receipt-total strong {
  color: var(--modern-text);
  font-weight: var(--modern-weight-semibold);
}
</style>
