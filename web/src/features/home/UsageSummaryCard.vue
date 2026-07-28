<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { Activity, CircleDollarSign, Database, TriangleAlert } from 'lucide-vue-next'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { usageQueryOptions, type UsageAggregateDto } from '@/app/resources/usage'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'
import { formatEstimatedUSD } from '@/features/usage/estimated-cost'

withDefaults(
  defineProps<{
    headingAs?: 'h2' | 'h3'
  }>(),
  {
    headingAs: 'h2',
  },
)

const filters = { range: '24h' } as const
const client = useApiClient()
const { locale, t } = useI18n()
const usageQuery = useQuery(usageQueryOptions(client, filters))
const report = computed(() => usageQuery.data.value)
const hasPipelineWarning = computed(() =>
  Boolean(
    report.value &&
    (report.value.collection_health.dropped_total > 0 ||
      report.value.collection_health.write_failure_total > 0),
  ),
)
function formatCount(value: number): string {
  return new Intl.NumberFormat(locale.value).format(value)
}

function formatReportedTokens(aggregate: UsageAggregateDto): string {
  const hasExcludedUsage =
    aggregate.usage_missing_count > 0 ||
    aggregate.partial_count > 0 ||
    aggregate.unpriced_request_count > 0
  if (hasExcludedUsage) {
    if (aggregate.total_tokens === 0) return t('home.usage.tokenValue.unknown')
    return t('home.usage.tokenValue.knownPlusUnknown', {
      tokens: formatCount(aggregate.total_tokens),
    })
  }
  return formatCount(aggregate.total_tokens)
}

function formatEstimatedCost(aggregate: UsageAggregateDto): string {
  if (aggregate.unpriced_request_count > 0) {
    return t('home.usage.cost.knownPlusUnknown', {
      cost: formatEstimatedUSD(aggregate.estimated_cost_usd, locale.value),
    })
  }
  return formatEstimatedUSD(aggregate.estimated_cost_usd, locale.value)
}
</script>

<template>
  <section class="usage-summary-section" aria-labelledby="home-usage-heading">
    <SurfaceCard class="usage-summary-card">
      <header class="usage-summary-card__header">
        <div>
          <p class="eyebrow">{{ t('home.usage.eyebrow') }}</p>
          <component :is="headingAs" id="home-usage-heading">{{ t('home.usage.title') }}</component>
          <p>{{ t('home.usage.description') }}</p>
        </div>
        <RouterLink
          class="usage-summary-card__detail"
          data-test="home-usage-detail"
          to="/monitor?tab=usage&range=24h"
        >
          {{ t('home.usage.detail') }}
        </RouterLink>
      </header>

      <QueryFeedback
        v-if="usageQuery.isPending.value"
        state="loading"
        :message="t('home.usage.loading')"
      />
      <QueryFeedback
        v-else-if="usageQuery.isError.value && !report"
        data-test="home-usage-error"
        state="error"
        :message="t('home.usage.loadFailed')"
        :retry-label="t('common.retry')"
        @retry="usageQuery.refetch()"
      />
      <template v-else-if="report">
        <QueryFeedback
          v-if="usageQuery.isError.value"
          data-test="home-usage-stale"
          state="stale"
          :message="t('home.usage.stale')"
          :retry-label="t('common.retry')"
          @retry="usageQuery.refetch()"
        />

        <EmptyState
          v-if="report.summary.request_count === 0"
          data-test="home-usage-empty"
          :title="t('home.usage.empty.title')"
          :description="t('home.usage.empty.description')"
        />
        <div v-else class="usage-summary-card__metrics">
          <div data-test="home-usage-requests">
            <Activity :size="19" aria-hidden="true" />
            <span>{{ t('home.usage.requests') }}</span>
            <strong>{{ formatCount(report.summary.request_count) }}</strong>
          </div>
          <div data-test="home-usage-tokens">
            <Database :size="19" aria-hidden="true" />
            <span>{{ t('home.usage.tokens') }}</span>
            <strong>{{ formatReportedTokens(report.summary) }}</strong>
          </div>
          <div data-test="home-usage-cost">
            <CircleDollarSign :size="19" aria-hidden="true" />
            <span>{{ t('home.usage.estimatedCost') }}</span>
            <strong>{{ formatEstimatedCost(report.summary) }}</strong>
          </div>
        </div>

        <div
          v-if="report.summary.request_count > 0"
          class="usage-summary-card__quality"
          data-test="home-usage-quality"
        >
          <StatusBadge
            data-test="home-usage-quality-missing"
            :tone="report.summary.usage_missing_count ? 'warning' : 'neutral'"
          >
            {{ t('home.usage.quality.missing') }}
            {{ formatCount(report.summary.usage_missing_count) }}
          </StatusBadge>
          <StatusBadge
            data-test="home-usage-quality-partial"
            :tone="report.summary.partial_count ? 'warning' : 'neutral'"
          >
            {{ t('home.usage.quality.partial') }}
            {{ formatCount(report.summary.partial_count) }}
          </StatusBadge>
          <StatusBadge
            data-test="home-usage-quality-unpriced"
            :tone="report.summary.unpriced_request_count ? 'warning' : 'neutral'"
          >
            {{ t('home.usage.quality.unpriced') }}
            {{ formatCount(report.summary.unpriced_request_count) }}
          </StatusBadge>
        </div>
        <InlineFeedback
          v-if="hasPipelineWarning"
          data-test="home-usage-pipeline-warning"
          tone="danger"
        >
          <TriangleAlert :size="16" aria-hidden="true" />
          {{
            t('home.usage.pipelineWarning', {
              dropped: formatCount(report.collection_health.dropped_total),
              failures: formatCount(report.collection_health.write_failure_total),
            })
          }}
        </InlineFeedback>
        <p class="usage-summary-card__observed">
          {{ t('home.usage.observedAt') }}
          <AppDateTime :instant="report.observed_at" :locale="locale" />
        </p>
      </template>
    </SurfaceCard>
  </section>
</template>

<style scoped>
.usage-summary-card {
  display: grid;
  gap: var(--space-4);
  padding: var(--space-5);
}
.usage-summary-card__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
}
.usage-summary-card__header :is(h2, h3),
.usage-summary-card__header p {
  margin: 0;
}
.usage-summary-card__header :is(h2, h3) {
  font-size: 1.125rem;
}
.usage-summary-card__header :is(h2, h3) + p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
}
.usage-summary-card__detail {
  display: inline-flex;
  min-height: 44px;
  flex: 0 0 auto;
  align-items: center;
  border-radius: var(--radius-control);
  color: var(--color-primary);
  font-weight: 650;
}
.usage-summary-card__detail:hover {
  text-decoration: underline;
}
.usage-summary-card__metrics {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--space-3);
}
.usage-summary-card__metrics > div {
  display: grid;
  min-width: 0;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: center;
  gap: var(--space-1) var(--space-2);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
  padding: var(--space-3);
}
.usage-summary-card__metrics span {
  color: var(--color-text-muted);
}
.usage-summary-card__metrics strong {
  grid-column: 1 / -1;
  overflow-wrap: anywhere;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 1.125rem;
}
.usage-summary-card__quality {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.usage-summary-card__observed {
  margin: 0;
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}
@media (max-width: 700px) {
  .usage-summary-card__header {
    flex-direction: column;
    gap: var(--space-2);
  }
  .usage-summary-card__metrics {
    grid-template-columns: 1fr;
  }
}
</style>
