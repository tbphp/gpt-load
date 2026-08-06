<script setup lang="ts">
import { CircleHelp } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import type { UsageAggregateDto } from '@/app/resources/usage'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import { formatEstimatedCost, formatInteger, formatPercent, formatTokens } from '@/lib/format'

import { formatCacheHitRate } from '@/lib/cache-rate'

const props = defineProps<{
  summary: UsageAggregateDto
}>()
const { locale, t } = useI18n()

function formattedEstimatedCost(): string {
  return formatEstimatedCost(props.summary.estimated_cost_nano_usd, locale.value)
}

function hasUnknownCost(): boolean {
  return (
    props.summary.usage_missing_count > 0 ||
    props.summary.partial_count > 0 ||
    props.summary.unpriced_request_count > 0 ||
    props.summary.pricing_partial_count > 0
  )
}

function inputTokens(): number {
  return (
    props.summary.uncached_input_tokens +
    props.summary.cache_read_tokens +
    props.summary.cache_write_5m_tokens +
    props.summary.cache_write_1h_tokens +
    props.summary.cache_write_unknown_tokens
  )
}
</script>

<template>
  <section class="usage-kpis" :aria-label="t('monitor.usage.kpi.title')">
    <article class="usage-kpi usage-kpi--accent">
      <span>{{ t('monitor.usage.kpi.requests') }}</span>
      <strong>{{ formatInteger(summary.request_count, locale) }}</strong>
      <small>
        {{
          t('monitor.usage.kpi.outcomeSummary', {
            success: formatInteger(summary.success_count, locale),
            failure: formatInteger(summary.failure_count, locale),
            rate: formatPercent(summary.failure_count, summary.request_count, locale),
          })
        }}
      </small>
    </article>
    <article class="usage-kpi usage-kpi--cache">
      <span>
        {{ t('monitor.usage.kpi.cacheHitRate') }}
        <AppTooltip :content="t('monitor.usage.kpi.cacheHitRateHelp')" side="bottom">
          <button
            type="button"
            class="usage-kpi__help"
            :aria-label="t('monitor.usage.kpi.cacheHitRateHelp')"
          >
            <CircleHelp :size="13" aria-hidden="true" />
          </button>
        </AppTooltip>
      </span>
      <strong>
        {{ formatCacheHitRate(summary.cache_read_tokens, inputTokens(), locale) }}
      </strong>
      <small>
        {{
          t('monitor.usage.kpi.cacheTokenSummary', {
            read: formatTokens(summary.cache_read_tokens, locale),
            input: formatTokens(inputTokens(), locale),
          })
        }}
      </small>
    </article>
    <article class="usage-kpi usage-kpi--accent">
      <span>{{ t('monitor.usage.kpi.totalTokens') }}</span>
      <strong>{{ formatTokens(summary.total_tokens, locale) }}</strong>
      <small>{{ t('monitor.usage.kpi.persistedWindow') }}</small>
    </article>
    <article class="usage-kpi usage-kpi--cost">
      <span>{{ t('monitor.usage.kpi.estimatedCost') }}</span>
      <strong>{{ formattedEstimatedCost() }}</strong>
      <small :class="{ 'usage-kpi__quality': hasUnknownCost() }">
        {{
          hasUnknownCost()
            ? t('monitor.usage.cost.knownPlusUnknownShort')
            : t('monitor.usage.cost.known')
        }}
      </small>
    </article>
  </section>
</template>

<style scoped>
.usage-kpis {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-border-subtle);
  gap: 1px;
}

.usage-kpi {
  --usage-kpi-color: var(--color-text);
  --usage-kpi-dot: var(--color-text-faint);

  min-width: 0;
  min-height: 108px;
  background: var(--color-surface);
  padding: 16px 18px;
}

.usage-kpi--accent {
  --usage-kpi-color: var(--color-action);
  --usage-kpi-dot: var(--color-action);
}

.usage-kpi--cost {
  --usage-kpi-color: var(--color-warning);
  --usage-kpi-dot: var(--color-warning);
}

.usage-kpi--cache {
  --usage-kpi-color: var(--color-success);
  --usage-kpi-dot: var(--color-success);
}

.usage-kpi > span,
.usage-kpi small {
  display: block;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.usage-kpi > span {
  display: flex;
  align-items: center;
  gap: 7px;
  font-weight: 560;
}

.usage-kpi > span::before {
  width: 6px;
  height: 6px;
  flex: 0 0 6px;
  border-radius: 50%;
  background: var(--usage-kpi-dot);
  content: '';
}

.usage-kpi strong {
  display: block;
  margin-top: 8px;
  color: var(--usage-kpi-color);
  font-family: var(--font-mono);
  font-size: clamp(1.45rem, 2.1vw, 1.9rem);
  font-weight: 580;
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.045em;
  line-height: 1;
  overflow-wrap: anywhere;
}

.usage-kpi small {
  margin-top: 9px;
  line-height: 1.4;
}

.usage-kpi__help {
  display: inline-grid;
  width: 20px;
  height: 20px;
  padding: 0;
  place-items: center;
  border: 0;
  background: transparent;
  color: var(--color-text-faint);
  cursor: help;
}

.usage-kpi__quality {
  color: var(--color-warning) !important;
}

@media (max-width: 900px) {
  .usage-kpis {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 560px) {
  .usage-kpis {
    grid-template-columns: minmax(0, 1fr);
  }

  .usage-kpi {
    min-height: 94px;
  }
}
</style>
