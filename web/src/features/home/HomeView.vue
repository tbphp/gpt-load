<script setup lang="ts">
import { keepPreviousData, useQuery } from '@tanstack/vue-query'
import { Activity, CircleHelp, Layers3, TriangleAlert } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { GroupSummary } from '@/api/control/types'
import { useApiClient } from '@/api/client-context'
import { groupListQueryOptions } from '@/app/resources/groups'
import { healthQueryOptions, type RuntimeHealthDto } from '@/app/resources/health'
import { usageQueryOptions, type UsageFilters, type UsageReportDto } from '@/app/resources/usage'
import { useVisibleRefetch } from '@/app/use-visible-refetch'
import TrendChart from '@/components/charts/TrendChart.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import StatFigure from '@/components/ui/StatFigure.vue'
import { formatEstimatedUSD } from '@/features/usage/estimated-cost'

import ConnectionPlaceholder from './ConnectionPlaceholder.vue'
import HomeCostRanking from './HomeCostRanking.vue'
import HomeLede from './HomeLede.vue'
import HomeProblemGroups from './HomeProblemGroups.vue'
import { formatCompactMetric } from './home-format'
import { failureLogsLocation, presentHome, type HomeQueryResult } from './home-presenter'

const client = useApiClient()
const { locale, t } = useI18n()
const selectedRange = ref<'24h' | '30d'>('24h')
const usageFilters = computed<UsageFilters>(() => ({
  range: selectedRange.value,
  breakdown_order: 'cost',
}))

const groupsQuery = useQuery(groupListQueryOptions(client))
const healthQuery = useQuery(healthQueryOptions(client, 15_000))
const usageQuery = useQuery({
  ...usageQueryOptions(client, usageFilters, 60_000),
  placeholderData: keepPreviousData,
})

useVisibleRefetch([
  () => groupsQuery.refetch(),
  () => healthQuery.refetch(),
  () => usageQuery.refetch(),
])

function queryResult<T>(
  pending: boolean,
  failed: boolean,
  data: T | undefined,
  errorUpdatedAt: number,
): HomeQueryResult<T> {
  if (failed) {
    const failedAt = new Date(errorUpdatedAt > 0 ? errorUpdatedAt : Date.now()).toISOString()
    return data === undefined ? { status: 'error', failedAt } : { status: 'error', failedAt, data }
  }
  if (pending || data === undefined) return { status: 'loading' }
  return { status: 'success', data }
}

const inventoryResult = computed(() =>
  queryResult<GroupSummary[]>(
    groupsQuery.isPending.value,
    groupsQuery.isError.value,
    groupsQuery.data.value,
    groupsQuery.errorUpdatedAt.value,
  ),
)
const healthResult = computed(() =>
  queryResult<RuntimeHealthDto>(
    healthQuery.isPending.value,
    healthQuery.isError.value,
    healthQuery.data.value,
    healthQuery.errorUpdatedAt.value,
  ),
)
const usageResult = computed(() =>
  queryResult<UsageReportDto>(
    usageQuery.isPending.value,
    usageQuery.isError.value,
    usageQuery.data.value,
    usageQuery.errorUpdatedAt.value,
  ),
)
const presentation = computed(() =>
  presentHome({
    inventory: inventoryResult.value,
    health: healthResult.value,
    usage: usageResult.value,
  }),
)

const report = computed(() => {
  const state = presentation.value.usage
  if (state.kind === 'data' || state.kind === 'empty' || state.kind === 'stale') {
    return state.report
  }
  return undefined
})
const observedDate = computed(() => {
  const state = presentation.value.health
  if (state.kind === 'normal' || state.kind === 'problem' || state.kind === 'stale') {
    return state.health.observed_at.slice(0, 10)
  }
  return new Date().toISOString().slice(0, 10)
})
const groupNames = computed(
  () => new Map((groupsQuery.data.value ?? []).map((group) => [group.id, group.name] as const)),
)
const rangeOptions = computed(() => [
  { value: '24h', label: t('home.range.last24Hours') },
  { value: '30d', label: t('home.range.last30Days') },
])
const successRate = computed(() => {
  const value = presentation.value.successRate
  if (value === null) return '—'
  return new Intl.NumberFormat(locale.value, {
    style: 'percent',
    maximumFractionDigits: 1,
  }).format(value / 100)
})
const successDetail = computed(() => {
  if (report.value === undefined) return ''
  return t('home.metrics.successDetail', {
    requests: formatCount(report.value.summary.request_count),
    failures: formatCount(report.value.summary.failure_count),
  })
})
const estimatedCost = computed(() => {
  if (report.value === undefined) return '—'
  return formatEstimatedUSD(report.value.summary.estimated_cost_usd, locale.value)
})
const costDetail = computed(() => {
  if (report.value === undefined) return ''
  return t('home.metrics.costDetail', {
    tokens: formatCompactMetric(report.value.summary.total_tokens, locale.value),
    unpriced: formatCount(report.value.summary.unpriced_request_count),
  })
})

function formatCount(value: number): string {
  return new Intl.NumberFormat(locale.value).format(value)
}

function setRange(value: string): void {
  if (value === '24h' || value === '30d') selectedRange.value = value
}
</script>

<template>
  <div class="home-page">
    <QueryFeedback
      v-if="presentation.inventory.kind === 'loading'"
      data-test="home-inventory-loading"
      state="loading"
      :message="t('home.inventory.loading')"
    />
    <QueryFeedback
      v-else-if="presentation.inventory.kind === 'error'"
      data-test="home-inventory-error"
      state="error"
      :message="t('home.inventory.error')"
      :retry-label="t('common.retry')"
      @retry="groupsQuery.refetch()"
    />
    <QueryFeedback
      v-else-if="presentation.inventory.kind === 'stale'"
      data-test="home-inventory-stale"
      state="stale"
      :message="t('home.inventory.stale')"
      :retry-label="t('common.retry')"
      @retry="groupsQuery.refetch()"
    />

    <EmptyState
      v-if="presentation.zeroGroups"
      data-test="home-zero-groups"
      :title="t('home.noGroupsTitle')"
      :description="t('home.noGroupsDescription')"
      heading-as="h1"
    >
      <template #icon><Layers3 :size="34" /></template>
      <template #actions>
        <RouterLink class="button-link" to="/import">{{ t('home.importKeys') }}</RouterLink>
      </template>
    </EmptyState>

    <template v-else>
      <HomeLede
        :state="presentation.health"
        :observed-date="observedDate"
        @retry="healthQuery.refetch()"
      />

      <ConnectionPlaceholder v-if="presentation.usage.kind === 'empty'" />

      <InlineFeedback
        v-if="presentation.pipelineWarning"
        data-test="home-pipeline-warning"
        tone="warning"
      >
        {{
          t('home.pipeline.warning', {
            dropped: formatCount(presentation.pipelineWarning.droppedTotal),
            failures: formatCount(presentation.pipelineWarning.writeFailureTotal),
          })
        }}
      </InlineFeedback>

      <HomeProblemGroups
        v-if="presentation.problemGroups.length > 0"
        :groups="presentation.problemGroups"
      />

      <section
        class="home-usage"
        :class="{
          'home-usage--after-problems': presentation.problemGroups.length > 0,
        }"
        data-test="home-usage"
        aria-labelledby="home-usage-title"
      >
        <QueryFeedback
          v-if="presentation.usage.kind === 'loading'"
          data-test="home-usage-loading"
          state="loading"
          :message="t('home.usageState.loading')"
        />
        <QueryFeedback
          v-else-if="presentation.usage.kind === 'error'"
          data-test="home-usage-error"
          state="error"
          :message="t('home.usageState.error')"
          :retry-label="t('common.retry')"
          @retry="usageQuery.refetch()"
        />
        <template v-else>
          <QueryFeedback
            v-if="presentation.usage.kind === 'stale'"
            data-test="home-usage-stale"
            state="stale"
            :message="t('home.usageState.stale')"
            :retry-label="t('common.retry')"
            @retry="usageQuery.refetch()"
          />

          <EmptyState
            v-if="presentation.usage.kind === 'empty'"
            data-test="home-zero-usage"
            :title="t('home.zeroUsage.title')"
            :description="t('home.zeroUsage.description')"
          >
            <template #icon><Activity :size="34" /></template>
          </EmptyState>

          <template v-else-if="report">
            <div class="home-metrics">
              <StatFigure
                data-test="home-success-rate"
                :label="t('home.metrics.successRate', { range: report.range })"
                :value="successRate"
                :detail="successDetail"
              />
              <StatFigure
                data-test="home-estimated-cost"
                :label="t('home.metrics.estimatedCost', { range: report.range })"
                :value="estimatedCost"
                :detail="costDetail"
              />
              <SegmentedControl
                class="home-metrics__range"
                :model-value="selectedRange"
                :label="t('home.range.label')"
                :options="rangeOptions"
                @update:model-value="setRange"
              />
            </div>

            <p
              v-if="presentation.health.kind === 'unknown' || presentation.health.kind === 'stale'"
              class="home-health-note"
              data-test="home-health-usage-independence"
              role="status"
            >
              <CircleHelp :size="16" aria-hidden="true" />
              {{ t('home.healthUsageIndependence') }}
            </p>

            <section class="home-trend" aria-labelledby="home-usage-title">
              <header class="home-section-heading home-trend__header">
                <h2 id="home-usage-title">
                  {{ t('home.trend.title', { range: report.range }) }}
                </h2>
                <RouterLink
                  v-if="report.summary.failure_count > 0"
                  class="home-trend__failure-link"
                  :to="failureLogsLocation(report)"
                >
                  <TriangleAlert :size="16" aria-hidden="true" />
                  {{ t('home.trend.failureLink') }}
                </RouterLink>
              </header>
              <TrendChart
                :series="report.series"
                :title="t('home.trend.title', { range: report.range })"
                :description="t('home.trend.chartDescription')"
                :empty-label="t('home.trend.empty')"
                :request-label="t('home.trend.requests')"
                :failure-label="t('home.trend.failures')"
                :locale="locale"
                :now-label="t('home.trend.now')"
                :rate-suffix="
                  report.granularity === 'hour' ? t('home.trend.perHour') : t('home.trend.perDay')
                "
                :failure-strip-label="
                  report.granularity === 'hour'
                    ? t('home.trend.failureStripHourly')
                    : t('home.trend.failureStripDaily')
                "
              />
            </section>

            <HomeCostRanking :report="report" :group-names="groupNames" />
          </template>
        </template>
      </section>

      <ConnectionPlaceholder v-if="presentation.usage.kind !== 'empty'" />
    </template>
  </div>
</template>

<style scoped>
.home-page {
  display: grid;
  min-width: 0;
  gap: var(--space-7);
}
.home-usage {
  display: grid;
  min-width: 0;
  gap: var(--space-7);
}
.home-usage--after-problems {
  border-top: 1px solid var(--color-border-strong);
  padding-top: var(--space-6);
}
.home-metrics {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto;
  align-items: end;
  gap: var(--space-6);
  border-bottom: 1px solid var(--color-border-subtle);
  padding-bottom: calc(var(--space-10) + var(--space-2));
}
.home-metrics > :nth-child(2) {
  border-left: 1px solid var(--color-border-subtle);
  padding-left: var(--space-6);
}
.home-metrics__range {
  justify-self: end;
}
.home-trend {
  display: grid;
  min-width: 0;
  gap: var(--space-6);
}
.home-section-heading h2,
.home-section-heading p {
  margin: 0;
}
.home-section-heading h2 {
  font-family: var(--font-serif);
  font-size: 1.45rem;
  font-weight: 500;
  line-height: var(--line-compact);
}
.home-section-heading p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
}
.home-trend__header {
  display: flex;
  min-width: 0;
  align-items: start;
  justify-content: space-between;
  gap: var(--space-5);
}
.home-trend__failure-link {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--color-danger);
  font-weight: 650;
  white-space: nowrap;
}
.home-health-note {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}
.home-health-note svg {
  flex: 0 0 auto;
}
@media (max-width: 1199px) {
  .home-metrics {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  }
  .home-metrics__range {
    grid-column: 1 / -1;
    justify-self: start;
  }
}
@media (max-width: 759px) {
  .home-page,
  .home-usage {
    gap: var(--space-5);
  }
  .home-metrics {
    grid-template-columns: minmax(0, 1fr);
  }
  .home-metrics > :nth-child(2) {
    border-top: 1px solid var(--color-border-subtle);
    border-left: 0;
    padding-top: var(--space-5);
    padding-left: 0;
  }
  .home-trend__header {
    flex-direction: column;
  }
  .home-trend__failure-link {
    white-space: normal;
  }
}
</style>
