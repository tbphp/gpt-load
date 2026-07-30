<script setup lang="ts">
import { Activity, CircleDollarSign, Database, Gauge } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import type { UsageAggregateDto } from '@/app/resources/usage'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'
import { formatEstimatedUSD } from '@/features/usage/estimated-cost'

const props = defineProps<{
  observedAt: string
  summary: UsageAggregateDto
}>()
const { locale, t } = useI18n()

function formatCount(value: number): string {
  return new Intl.NumberFormat(locale.value).format(value)
}

function formatEstimatedCost(): string {
  const cost = formatEstimatedUSD(props.summary.estimated_cost_usd, locale.value)
  return props.summary.unpriced_request_count > 0
    ? t('monitor.usage.cost.knownPlusUnknown', { cost })
    : cost
}
</script>

<template>
  <section class="usage-section" aria-labelledby="usage-kpi-title">
    <div class="usage-heading">
      <div>
        <h2 id="usage-kpi-title">{{ t('monitor.usage.kpi.title') }}</h2>
        <p>{{ t('monitor.usage.kpi.description', { time: observedAt }) }}</p>
      </div>
    </div>
    <div class="usage-kpi-grid">
      <SurfaceCard class="usage-kpi">
        <Activity :size="20" aria-hidden="true" />
        <span>{{ t('monitor.usage.kpi.requests') }}</span>
        <strong>{{ formatCount(summary.request_count) }}</strong>
      </SurfaceCard>
      <SurfaceCard class="usage-kpi">
        <Gauge :size="20" aria-hidden="true" />
        <span>{{ t('monitor.usage.kpi.outcomes') }}</span>
        <strong>
          {{
            t('monitor.usage.kpi.outcomeCounts', {
              success: formatCount(summary.success_count),
              failure: formatCount(summary.failure_count),
            })
          }}
        </strong>
      </SurfaceCard>
      <SurfaceCard class="usage-kpi">
        <Database :size="20" aria-hidden="true" />
        <span>{{ t('monitor.usage.kpi.totalTokens') }}</span>
        <strong>{{ formatCount(summary.total_tokens) }}</strong>
      </SurfaceCard>
      <SurfaceCard class="usage-kpi">
        <CircleDollarSign :size="20" aria-hidden="true" />
        <span>{{ t('monitor.usage.kpi.estimatedCost') }}</span>
        <strong>{{ formatEstimatedCost() }}</strong>
      </SurfaceCard>
    </div>
    <dl class="usage-token-definition" :aria-label="t('monitor.usage.tokens.title')">
      <div>
        <dt>{{ t('monitor.usage.tokens.uncachedInput') }}</dt>
        <dd>{{ formatCount(summary.uncached_input_tokens) }}</dd>
      </div>
      <div>
        <dt>{{ t('monitor.usage.tokens.cacheRead') }}</dt>
        <dd>{{ formatCount(summary.cache_read_tokens) }}</dd>
      </div>
      <div>
        <dt>{{ t('monitor.usage.tokens.cacheWrite5m') }}</dt>
        <dd>{{ formatCount(summary.cache_write_5m_tokens) }}</dd>
      </div>
      <div>
        <dt>{{ t('monitor.usage.tokens.cacheWrite1h') }}</dt>
        <dd>{{ formatCount(summary.cache_write_1h_tokens) }}</dd>
      </div>
      <div>
        <dt>{{ t('monitor.usage.tokens.output') }}</dt>
        <dd>{{ formatCount(summary.output_tokens) }}</dd>
      </div>
    </dl>
  </section>
</template>

<style scoped>
.usage-section {
  display: grid;
  min-width: 0;
  gap: var(--space-4);
}
.usage-heading {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-3);
}
.usage-heading h2 {
  margin: 0;
  font-size: 1rem;
}
.usage-heading p {
  margin: var(--space-1) 0 0;
  color: var(--color-text-muted);
}
.usage-kpi-grid,
.usage-token-definition {
  display: grid;
  gap: var(--space-3);
}
.usage-kpi-grid {
  grid-template-columns: repeat(4, minmax(0, 1fr));
}
.usage-token-definition {
  grid-template-columns: repeat(5, minmax(0, 1fr));
  margin: 0;
}
.usage-token-definition > div {
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-2);
}
.usage-token-definition dt {
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}
.usage-token-definition dd {
  margin: var(--space-1) 0 0;
  font-weight: 700;
}
.usage-kpi {
  display: grid;
  min-width: 0;
  gap: var(--space-2);
}
.usage-kpi > svg {
  color: var(--color-action);
}
.usage-kpi > span {
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}
.usage-kpi strong {
  font-size: 1.25rem;
  overflow-wrap: anywhere;
}
@media (max-width: 1000px) {
  .usage-kpi-grid,
  .usage-token-definition {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 720px) {
  .usage-kpi-grid,
  .usage-token-definition {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
