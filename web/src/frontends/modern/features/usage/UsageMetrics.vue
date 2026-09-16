<script setup lang="ts">
import { Activity, CircleCheck, Coins, Database, Layers2 } from '@lucide/vue'
import { computed, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UsageAggregate, UsageReport } from '@modern/api/usage'
import { AppIcon, AppTooltip } from '@modern/components/ui'
import { formatCompactNumber } from '@modern/components/ui/format'
import { inputTokens, cacheRate, formatUsageCost, successRate, percentage } from './usage-display'
import UsageMetricGraphic from './UsageMetricGraphic.vue'

const props = defineProps<{ report: UsageReport }>()
const { t, locale } = useI18n()
const report = computed(() => props.report)
const compact = (value: number) => formatCompactNumber(value, locale.value)
const cost = (row: UsageAggregate) =>
  row.estimated_cost_nano_usd === '0' && (row.unpriced_request_count || row.pricing_partial_count)
    ? '—'
    : formatUsageCost(row.estimated_cost_nano_usd, locale.value)
interface UsageCard {
  id: 'requests' | 'success' | 'tokens' | 'cache' | 'cost'
  icon: Component
  value: string
  detail: string
}
const cards = computed<UsageCard[]>(() => {
  const row = report.value?.summary
  if (!row) return []
  const tokensMissing = !row.total_tokens && (row.usage_missing_count > 0 || row.partial_count > 0)
  return [
    {
      id: 'requests',
      icon: Activity,
      value: compact(row.request_count),
      detail: t('usage.requestDetail', {
        success: compact(row.success_count),
        failure: compact(row.failure_count),
      }),
    },
    {
      id: 'success',
      icon: CircleCheck,
      value: percentage(successRate(row), locale.value),
      detail: t('usage.successDetail'),
    },
    {
      id: 'tokens',
      icon: Layers2,
      value: tokensMissing ? '—' : compact(row.total_tokens),
      detail: t('usage.tokenDetail', {
        input: tokensMissing ? '—' : compact(inputTokens(row)),
        output: tokensMissing ? '—' : compact(row.output_tokens),
      }),
    },
    {
      id: 'cache',
      icon: Database,
      value: percentage(cacheRate(row), locale.value),
      detail: t('usage.cacheDetail', { count: compact(row.cache_read_tokens) }),
    },
    {
      id: 'cost',
      icon: Coins,
      value: cost(row),
      ...(row.unpriced_request_count || row.pricing_partial_count
        ? {
            detail: t('usage.unpriced', {
              count: compact(row.unpriced_request_count),
              partial: compact(row.pricing_partial_count),
            }),
          }
        : { detail: t('usage.costDetail') }),
    },
  ]
})
</script>
<template>
  <div class="modern-usage-metrics">
    <article v-for="card in cards" :key="card.id" class="modern-usage-stat" :data-kind="card.id">
      <div class="modern-usage-stat-label">
        <span class="modern-usage-stat-symbol"><AppIcon :icon="card.icon" size="sm" /></span>
        <AppTooltip :label="card.id === 'cache' ? t('usage.cacheHint') : undefined">
          <span :tabindex="card.id === 'cache' ? 0 : undefined">
            {{ t('usage.' + card.id) }}
          </span>
        </AppTooltip>
      </div>
      <div class="modern-usage-stat-main">
        <div class="modern-usage-stat-value">
          <strong class="modern-usage-stat-number">{{ card.value }}</strong>
        </div>
        <UsageMetricGraphic class="modern-usage-stat-graphic" :report="report" :metric="card.id" />
      </div>
      <span class="modern-usage-stat-detail">{{ card.detail }}</span>
    </article>
  </div>
</template>
<style scoped>
.modern-usage-metrics {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: var(--modern-space-3);
}
.modern-usage-stat {
  --modern-usage-stat-tone: var(--modern-accent);
  container: usage-stat / inline-size;
  position: relative;
  isolation: isolate;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--modern-space-3);
  min-width: 0;
  overflow: hidden;
  padding: var(--modern-space-4) var(--modern-space-5);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
}
.modern-usage-stat::before {
  position: absolute;
  inset: 0;
  z-index: var(--modern-layer-underlay);
  background: radial-gradient(
    ellipse at top right,
    color-mix(in srgb, var(--modern-usage-stat-tone) 8%, var(--modern-surface)),
    transparent 75%
  );
  content: '';
  pointer-events: none;
}
.modern-usage-stat[data-kind='tokens'] {
  --modern-usage-stat-tone: var(--modern-chart-input);
}
.modern-usage-stat[data-kind='success'],
.modern-usage-stat[data-kind='cache'] {
  --modern-usage-stat-tone: var(--modern-chart-cache);
}
.modern-usage-stat[data-kind='cost'] {
  --modern-usage-stat-tone: var(--modern-chart-output);
}
.modern-usage-stat-symbol {
  display: grid;
  width: var(--modern-control-xs);
  height: var(--modern-control-xs);
  flex: none;
  place-items: center;
  border-radius: var(--modern-radius-control);
  color: var(--modern-usage-stat-tone);
  background: color-mix(in srgb, var(--modern-usage-stat-tone) 8%, var(--modern-surface));
}
.modern-usage-stat-label {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-usage-stat-main {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
  min-width: 0;
  width: 100%;
}
.modern-usage-stat-value {
  display: flex;
  flex: 1;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-usage-stat-number {
  min-width: 0;
  color: var(--modern-text);
  font-size: var(--modern-font-size-title);
  font-weight: var(--modern-weight-semibold);
  line-height: var(--modern-leading-title);
  letter-spacing: var(--modern-tracking-title);
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
}
.modern-usage-stat-detail {
  margin-top: auto;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-compact);
}
@media (max-width: 1150px) {
  .modern-usage-metrics {
    grid-template-columns: repeat(6, minmax(0, 1fr));
  }
  .modern-usage-stat {
    grid-column: span 2;
  }
  .modern-usage-stat:nth-last-child(-n + 2) {
    grid-column: span 3;
  }
}
@media (max-width: 760px) {
  .modern-usage-metrics {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .modern-usage-stat:nth-child(n) {
    grid-column: auto;
    padding: var(--modern-space-4);
  }
  .modern-usage-stat:last-child {
    grid-column: 1 / -1;
  }
}
@container usage-stat (max-width: 164px) {
  .modern-usage-stat-graphic {
    display: none;
  }
}
</style>
