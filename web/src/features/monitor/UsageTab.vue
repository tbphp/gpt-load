<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { Database } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { useCollectionLoading } from '@/app/loading-state'
import { groupOptionsQueryOptions } from '@/app/resources/groups'
import { listChannels } from '@/app/resources/channels'
import { controlQueryKeys } from '@/app/query-keys'
import {
  usageQueryOptions,
  type UsageAggregateDto,
  type UsageDistributionDimension,
  type UsageDistributionMetric,
  type UsageFilters,
  type UsageRange,
} from '@/app/resources/usage'
import { monitorLocation } from '@/app/route-locations'
import TrendChart from '@/components/charts/TrendChart.vue'
import type { TrendDatum } from '@/components/charts/trend-chart'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import DataTable from '@/components/ui/DataTable.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'
import { formatEstimatedCost, formatInteger, formatTokens } from '@/lib/format'
import { useAuthSession } from '@/features/auth/auth-session'

import MonitorSectionHeading from './MonitorSectionHeading.vue'
import UsageBarChart from './UsageBarChart.vue'
import UsageDistribution from './UsageDistribution.vue'
import {
  applyUsageFilterDraft,
  createUsageFilterDraft,
  parseAppliedUsageFilters,
  validateUsageFilterDraft,
  type UsageFilterDraft,
  type UsageFilterErrors,
} from './usage-filters'
import type { UsageBarDatum } from './usage-bar-chart'
import {
  parseUsageMonitorState,
  usageMonitorQuery,
  type UsageMonitorState,
  type UsageTrendMetric,
  scopeAccessKeyUsageFilters,
} from './monitor-route'
import UsageFilterForm from './UsageFilterForm.vue'
import UsageSummary from './UsageSummary.vue'

const client = useApiClient()
const session = useAuthSession()
const route = useRoute()
const router = useRouter()
const { locale, t } = useI18n()
const isAccessKey = computed(() => session.state.principalType === 'access_key')
const appliedFilters = computed(() => {
  const filters = parseAppliedUsageFilters(route.query)
  return isAccessKey.value ? scopeAccessKeyUsageFilters(filters) : filters
})
const routeState = computed(() => parseUsageMonitorState(route.query))
const filterOpen = computed(() => routeState.value.filtersOpen)
const draft = ref<UsageFilterDraft>(createUsageFilterDraft(appliedFilters.value))
const filterErrors = ref<UsageFilterErrors>({})

const groupsQuery = useQuery({
  ...groupOptionsQueryOptions(client, () => !isAccessKey.value),
  refetchOnMount: false,
})
const channelsQuery = useQuery({
  queryKey: controlQueryKeys.channels.list(''),
  queryFn: ({ signal }) => listChannels(client, '', signal),
  enabled: computed(() => !isAccessKey.value),
  staleTime: Number.POSITIVE_INFINITY,
  refetchOnMount: false,
  refetchOnWindowFocus: false,
  refetchOnReconnect: false,
})
const usageQuery = useQuery(usageQueryOptions(client, appliedFilters))
const report = computed(() => usageQuery.data.value)
const distributionDimension = ref<UsageDistributionDimension>('model')
const distributionMetric = ref<UsageDistributionMetric>('cost')
const distribution = computed(() => {
  const distributions = report.value?.distributions
  if (distributions === undefined) return undefined
  if (isAccessKey.value || distributionDimension.value === 'model') {
    return distributions.model[distributionMetric.value]
  }
  return (
    distributions.group?.[distributionMetric.value] ?? distributions.model[distributionMetric.value]
  )
})
const {
  initial: initialLoading,
  transition: reportTransition,
  refreshing: reportRefreshing,
} = useCollectionLoading(
  {
    pending: () => usageQuery.isPending.value,
    placeholder: () => usageQuery.isPlaceholderData.value,
    fetching: () => usageQuery.isFetching.value,
    hasData: () => report.value !== undefined,
    itemCount: () =>
      (distribution.value?.items.length ?? 0) + (distribution.value?.other === null ? 0 : 1),
  },
  { fallbackRows: 5 },
)
const usageRefreshing = computed(
  () =>
    reportRefreshing.value ||
    (!isAccessKey.value && groupsQuery.data.value !== undefined && groupsQuery.isFetching.value) ||
    (!isAccessKey.value &&
      channelsQuery.data.value !== undefined &&
      channelsQuery.isFetching.value),
)
const hasData = computed(() => (report.value?.summary.request_count ?? 0) > 0)
const distributionDimensionOptions = computed(() => [
  { value: 'model', label: t('monitor.usage.distribution.dimensions.model') },
  { value: 'group', label: t('monitor.usage.distribution.dimensions.group') },
])
const distributionMetricOptions = computed(() => [
  { value: 'cost', label: t('monitor.usage.distribution.metrics.cost') },
  { value: 'requests', label: t('monitor.usage.distribution.metrics.requests') },
])
const costChartResolution = 1_000_000_000n
const trendMetricOptions = computed(() => [
  { value: 'requests', label: t('monitor.usage.trend.metrics.requests') },
  { value: 'tokens', label: t('monitor.usage.trend.metrics.tokens') },
  { value: 'cost', label: t('monitor.usage.trend.metrics.cost') },
])
const trendPresentation = computed(() => {
  switch (routeState.value.metric) {
    case 'tokens':
      return {
        title: t('monitor.usage.trend.tokensTitle'),
        description: t('monitor.usage.trend.tokensDescription'),
        accessibleDescription: t('monitor.usage.trend.tokensAccessibleDescription'),
        valueLabel: t('monitor.usage.columns.totalTokens'),
        secondaryLabel: t('monitor.usage.tokens.cacheRead'),
      }
    case 'cost':
      return {
        title: t('monitor.usage.trend.costTitle'),
        description: t('monitor.usage.trend.costDescription'),
        accessibleDescription: t('monitor.usage.trend.costAccessibleDescription'),
        valueLabel: t('monitor.usage.columns.estimatedCost'),
        secondaryLabel: undefined,
      }
    default:
      return {
        title: t('monitor.usage.trend.title'),
        description: t('monitor.usage.trend.description'),
        accessibleDescription: t('monitor.usage.trend.accessibleDescription'),
        valueLabel: t('monitor.usage.columns.success'),
        secondaryLabel: t('monitor.usage.columns.failure'),
      }
  }
})

function formatTrendCost(aggregate: UsageAggregateDto): string {
  return formatEstimatedCost(aggregate.estimated_cost_nano_usd, locale.value)
}

function formatTrendTokens(value: number): string {
  return formatTokens(value, locale.value)
}

function normalizeTrendCost(value: bigint, maximum: bigint): number {
  if (maximum === 0n) return 0
  return Number((value * costChartResolution + maximum / 2n) / maximum)
}

const requestTrendSeries = computed<TrendDatum[]>(() =>
  (report.value?.series ?? []).map((bucket) => ({
    bucket_start_ms: bucket.bucket_start_ms,
    bucket_end_ms: bucket.bucket_end_ms,
    request_count: bucket.success_count,
    failure_count: bucket.failure_count,
  })),
)

const barTrendSeries = computed<UsageBarDatum[]>(() => {
  const buckets = report.value?.series ?? []
  if (routeState.value.metric === 'tokens') {
    return buckets.map((bucket) => ({
      bucket_start_ms: bucket.bucket_start_ms,
      bucket_end_ms: bucket.bucket_end_ms,
      primary_value: bucket.total_tokens,
      secondary_value: bucket.cache_read_tokens,
      primary_display: formatTrendTokens(bucket.total_tokens),
      secondary_display: formatTrendTokens(bucket.cache_read_tokens),
      details: [
        {
          label: t('monitor.usage.trend.inputTokens'),
          display: formatTrendTokens(inputTokens(bucket)),
        },
        {
          label: t('monitor.usage.trend.outputTokens'),
          display: formatTrendTokens(bucket.output_tokens),
        },
      ],
    }))
  }
  if (routeState.value.metric === 'cost') {
    const costs = buckets.map((bucket) => BigInt(bucket.estimated_cost_nano_usd))
    const maximum = costs.reduce((current, value) => (value > current ? value : current), 0n)
    return buckets.map((bucket, index) => ({
      bucket_start_ms: bucket.bucket_start_ms,
      bucket_end_ms: bucket.bucket_end_ms,
      primary_value: normalizeTrendCost(costs[index]!, maximum),
      secondary_value: 0,
      primary_display: formatTrendCost(bucket),
    }))
  }
  return []
})

watch(
  [
    () => appliedFilters.value.range,
    () => appliedFilters.value.group_id,
    () => appliedFilters.value.channel_id,
    () => appliedFilters.value.credential_id,
    () => appliedFilters.value.upstream_model,
  ],
  () => {
    draft.value = createUsageFilterDraft(appliedFilters.value)
    filterErrors.value = {}
  },
)

watch(
  () => routeState.value.filtersOpen,
  () => {
    draft.value = createUsageFilterDraft(appliedFilters.value)
    filterErrors.value = {}
  },
)

function rangeLabel(range: UsageRange): string {
  return t(`monitor.usage.filters.ranges.${range}`)
}

function granularityLabel(): string {
  const bucketWidthMS = report.value?.bucket_width_ms
  if (bucketWidthMS === 60 * 60 * 1000) return t('monitor.usage.trend.hourly')
  if (bucketWidthMS === 24 * 60 * 60 * 1000) return t('monitor.usage.trend.daily')
  return t('monitor.usage.trend.everyHours', {
    count: (bucketWidthMS ?? 0) / (60 * 60 * 1000),
  })
}

function formatCost(aggregate: UsageAggregateDto): string {
  return formatEstimatedCost(aggregate.estimated_cost_nano_usd, locale.value)
}

function inputTokens(aggregate: UsageAggregateDto): number {
  return (
    aggregate.uncached_input_tokens +
    aggregate.cache_read_tokens +
    aggregate.cache_write_5m_tokens +
    aggregate.cache_write_1h_tokens +
    aggregate.cache_write_unknown_tokens
  )
}

function updateDraftField(field: keyof UsageFilterDraft, value: string): void {
  draft.value = { ...draft.value, [field]: value }
}

function setFilterOpen(open: boolean): void {
  if (!open) {
    draft.value = createUsageFilterDraft(appliedFilters.value)
    filterErrors.value = {}
  }
  void navigate(appliedFilters.value, { ...routeState.value, filtersOpen: open })
}

function openFilters(): void {
  draft.value = createUsageFilterDraft(appliedFilters.value)
  filterErrors.value = {}
  void navigate(appliedFilters.value, { ...routeState.value, filtersOpen: true })
}

async function applyFilters(): Promise<void> {
  const errors = validateUsageFilterDraft(draft.value)
  filterErrors.value = errors
  if (Object.keys(errors).length > 0) return
  await navigate(applyUsageFilterDraft(draft.value))
}

async function resetFilters(): Promise<void> {
  await navigate({ range: appliedFilters.value.range })
}

function updateDistributionDimension(value: string): void {
  if (value !== 'group' && value !== 'model') return
  if (isAccessKey.value || (value === 'group' && report.value?.distributions.group === undefined))
    return
  distributionDimension.value = value as UsageDistributionDimension
}

function updateDistributionMetric(value: string): void {
  if (value !== 'requests' && value !== 'cost') return
  distributionMetric.value = value as UsageDistributionMetric
}

async function updateTrendMetric(value: string): Promise<void> {
  if (value !== 'requests' && value !== 'tokens' && value !== 'cost') return
  await navigate(appliedFilters.value, {
    ...routeState.value,
    metric: value as UsageTrendMetric,
  })
}

async function navigate(
  filters: UsageFilters,
  state: UsageMonitorState = {
    filtersOpen: false,
    seriesExpanded: false,
    metric: routeState.value.metric,
  },
): Promise<void> {
  const scopedFilters = isAccessKey.value ? scopeAccessKeyUsageFilters(filters) : filters
  await router.push(monitorLocation(usageMonitorQuery(scopedFilters, state)))
}

function setSeriesExpanded(event: Event): void {
  const expanded = (event.currentTarget as HTMLDetailsElement).open
  if (expanded === routeState.value.seriesExpanded) return
  void navigate(appliedFilters.value, { ...routeState.value, seriesExpanded: expanded })
}

async function refresh(): Promise<void> {
  await Promise.all([
    usageQuery.refetch(),
    ...(!isAccessKey.value ? [groupsQuery.refetch(), channelsQuery.refetch()] : []),
  ])
}

defineExpose({ openFilters, refresh })
</script>

<template>
  <div class="usage-tab">
    <AsyncRefreshIndicator :active="usageRefreshing" :label="t('monitor.usage.loading')" />

    <SkeletonSurface
      v-if="usageQuery.isPending.value || initialLoading || reportTransition"
      variant="dashboard"
      :concealed="usageQuery.isPending.value && !initialLoading"
      :label="t('monitor.usage.loading')"
    />
    <QueryFeedback
      v-else-if="usageQuery.isError.value && !report"
      state="error"
      :message="t('monitor.usage.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="usageQuery.refetch()"
    />

    <template v-else-if="report">
      <QueryFeedback
        v-if="usageQuery.isError.value"
        state="stale"
        :message="t('monitor.usage.stale')"
        :retry-label="t('common.retry')"
        @retry="usageQuery.refetch()"
      />

      <InlineFeedback
        v-if="!isAccessKey && (groupsQuery.isError.value || channelsQuery.isError.value)"
        tone="warning"
      >
        {{ t('monitor.usage.options.partialFailed') }}
      </InlineFeedback>

      <UsageSummary :summary="report.summary" />

      <InlineFeedback
        v-if="
          !isAccessKey &&
          (report.collection_health.dropped_total > 0 ||
            report.collection_health.write_failure_total > 0)
        "
        tone="danger"
        appearance="ledger"
      >
        {{
          t('monitor.usage.process.warning', {
            dropped: formatInteger(report.collection_health.dropped_total, locale),
            failures: formatInteger(report.collection_health.write_failure_total, locale),
          })
        }}
      </InlineFeedback>

      <EmptyState
        v-if="!hasData"
        variant="ledger"
        :title="t('monitor.usage.empty.title')"
        :description="t('monitor.usage.empty.description')"
      >
        <template #icon><Database :size="20" aria-hidden="true" /></template>
      </EmptyState>

      <template v-else>
        <section class="usage-trend-panel" aria-labelledby="usage-trend-title">
          <MonitorSectionHeading
            id="usage-trend-title"
            :title="trendPresentation.title"
            :description="trendPresentation.description"
            :meta="`${rangeLabel(report.range)} · ${granularityLabel()}`"
          >
            <template #actions>
              <SegmentedControl
                :model-value="routeState.metric"
                :label="t('monitor.usage.trend.metrics.label')"
                :options="trendMetricOptions"
                size="compact"
                @update:model-value="updateTrendMetric"
              />
            </template>
          </MonitorSectionHeading>
          <div class="usage-trend-panel__chart">
            <TrendChart
              v-if="routeState.metric === 'requests'"
              :series="requestTrendSeries"
              :title="trendPresentation.title"
              :description="trendPresentation.accessibleDescription"
              :empty-label="t('monitor.usage.trend.empty')"
              :request-label="trendPresentation.valueLabel"
              :failure-label="trendPresentation.secondaryLabel ?? ''"
              :range-start="report.from_ms"
              :range-end="report.to_ms"
              :locale="locale"
              show-bucket-range
              show-single-point
              :failure-rate-label="t('monitor.usage.trend.failureRate')"
            />
            <UsageBarChart
              v-else
              :series="barTrendSeries"
              :title="trendPresentation.title"
              :description="trendPresentation.accessibleDescription"
              :empty-label="t('monitor.usage.trend.empty')"
              :primary-label="trendPresentation.valueLabel"
              :secondary-label="trendPresentation.secondaryLabel"
              :primary-zero-display="
                routeState.metric === 'cost'
                  ? formatEstimatedCost('0', locale)
                  : formatTrendTokens(0)
              "
              :secondary-zero-display="formatTrendTokens(0)"
              :detail-zero-display="formatTrendTokens(0)"
              :range-start="report.from_ms"
              :range-end="report.to_ms"
              :locale="locale"
              :grouped="routeState.metric === 'tokens'"
            />
          </div>
        </section>

        <section class="usage-analysis-grid">
          <div class="usage-analysis-section">
            <MonitorSectionHeading
              :title="t('monitor.usage.tokens.title')"
              :description="t('monitor.usage.tokens.description')"
            />
            <dl class="usage-token-grid">
              <div>
                <dt>{{ t('monitor.usage.tokens.uncachedInput') }}</dt>
                <dd>{{ formatTokens(report.summary.uncached_input_tokens, locale) }}</dd>
              </div>
              <div>
                <dt>{{ t('monitor.usage.tokens.cacheRead') }}</dt>
                <dd>{{ formatTokens(report.summary.cache_read_tokens, locale) }}</dd>
              </div>
              <div>
                <dt>{{ t('monitor.usage.tokens.cacheWrite5m') }}</dt>
                <dd>{{ formatTokens(report.summary.cache_write_5m_tokens, locale) }}</dd>
              </div>
              <div>
                <dt>{{ t('monitor.usage.tokens.cacheWrite1h') }}</dt>
                <dd>{{ formatTokens(report.summary.cache_write_1h_tokens, locale) }}</dd>
              </div>
              <div>
                <dt>{{ t('monitor.usage.tokens.cacheWriteUnknown') }}</dt>
                <dd>{{ formatTokens(report.summary.cache_write_unknown_tokens, locale) }}</dd>
              </div>
              <div>
                <dt>{{ t('monitor.usage.tokens.output') }}</dt>
                <dd>{{ formatTokens(report.summary.output_tokens, locale) }}</dd>
              </div>
            </dl>
          </div>

          <div class="usage-analysis-section">
            <MonitorSectionHeading
              :title="t('monitor.usage.quality.title')"
              :description="t('monitor.usage.quality.description')"
            />
            <dl class="usage-quality-grid">
              <div>
                <dt>{{ t('monitor.usage.quality.missing') }}</dt>
                <dd>{{ formatInteger(report.summary.usage_missing_count, locale) }}</dd>
              </div>
              <div>
                <dt>{{ t('monitor.usage.quality.partial') }}</dt>
                <dd>{{ formatInteger(report.summary.partial_count, locale) }}</dd>
              </div>
              <div class="usage-quality-grid__danger">
                <dt>{{ t('monitor.usage.quality.unpriced') }}</dt>
                <dd>{{ formatInteger(report.summary.unpriced_request_count, locale) }}</dd>
              </div>
              <div>
                <dt>{{ t('monitor.usage.quality.pricingPartial') }}</dt>
                <dd>{{ formatInteger(report.summary.pricing_partial_count, locale) }}</dd>
              </div>
            </dl>
          </div>
        </section>

        <section class="usage-distribution-section" aria-labelledby="usage-distribution-title">
          <MonitorSectionHeading
            id="usage-distribution-title"
            :title="t('monitor.usage.distribution.title')"
            :description="t('monitor.usage.distribution.description')"
            :meta="t('monitor.usage.distribution.limit')"
          >
            <template #actions>
              <SegmentedControl
                v-if="!isAccessKey"
                :model-value="distributionDimension"
                :label="t('monitor.usage.distribution.dimensionLabel')"
                :options="distributionDimensionOptions"
                size="compact"
                @update:model-value="updateDistributionDimension"
              />
              <SegmentedControl
                :model-value="distributionMetric"
                :label="t('monitor.usage.distribution.metricLabel')"
                :options="distributionMetricOptions"
                size="compact"
                @update:model-value="updateDistributionMetric"
              />
            </template>
          </MonitorSectionHeading>

          <UsageDistribution
            v-if="distribution"
            :distribution="distribution"
            :summary="report.summary"
            :groups="groupsQuery.data.value ?? []"
            :channels="channelsQuery.data.value?.items ?? []"
          />
        </section>

        <details
          class="usage-buckets"
          :open="routeState.seriesExpanded"
          @toggle="setSeriesExpanded"
        >
          <summary>{{ t('monitor.usage.series.disclosure') }}</summary>
          <div class="usage-buckets__body">
            <DataTable :caption="t('monitor.usage.series.caption')" appearance="editorial" dense>
              <thead>
                <tr>
                  <th scope="col">{{ t('monitor.usage.columns.window') }}</th>
                  <th scope="col">{{ t('monitor.usage.columns.requests') }}</th>
                  <th scope="col">{{ t('monitor.usage.columns.success') }}</th>
                  <th scope="col">{{ t('monitor.usage.columns.failure') }}</th>
                  <th scope="col">{{ t('monitor.usage.columns.totalTokens') }}</th>
                  <th scope="col">{{ t('monitor.usage.columns.estimatedCost') }}</th>
                  <th scope="col">{{ t('monitor.usage.columns.quality') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="bucket in report.series" :key="bucket.bucket_start_ms">
                  <td><AppDateTime :instant="bucket.bucket_start_ms" :locale="locale" /></td>
                  <td>{{ formatInteger(bucket.request_count, locale) }}</td>
                  <td>{{ formatInteger(bucket.success_count, locale) }}</td>
                  <td>{{ formatInteger(bucket.failure_count, locale) }}</td>
                  <td>{{ formatTokens(bucket.total_tokens, locale) }}</td>
                  <td>{{ formatCost(bucket) }}</td>
                  <td>
                    {{
                      t('monitor.usage.columns.qualityCompact', {
                        missing: formatInteger(bucket.usage_missing_count, locale),
                        partial: formatInteger(bucket.partial_count, locale),
                        unpriced: formatInteger(bucket.unpriced_request_count, locale),
                        pricingPartial: formatInteger(bucket.pricing_partial_count, locale),
                      })
                    }}
                  </td>
                </tr>
              </tbody>
            </DataTable>
          </div>
        </details>
      </template>
    </template>

    <UsageFilterForm
      :open="filterOpen"
      :draft="draft"
      :errors="filterErrors"
      :groups="groupsQuery.data.value ?? []"
      :channels="channelsQuery.data.value?.items ?? []"
      :groups-failed="groupsQuery.isError.value"
      :channels-failed="channelsQuery.isError.value"
      :self-scoped="isAccessKey"
      @update:open="setFilterOpen"
      @update-field="updateDraftField"
      @apply="applyFilters"
      @reset="resetFilters"
    />
  </div>
</template>

<style scoped>
.usage-tab,
.usage-analysis-section,
.usage-distribution-section {
  display: grid;
  min-width: 0;
}

.usage-tab {
  gap: 22px;
}

.usage-analysis-section,
.usage-distribution-section {
  gap: 12px;
}

.usage-distribution-section :deep(.monitor-section-heading) {
  flex-wrap: wrap;
}

@media (max-width: 680px) {
  .usage-distribution-section :deep(.monitor-section-heading__actions) {
    width: 100%;
    flex-wrap: wrap;
    justify-content: flex-start;
    margin-left: 0;
  }
}

.usage-trend-panel {
  display: grid;
  min-width: 0;
  gap: 12px;
}

.usage-trend-panel :deep(.monitor-section-heading) {
  flex-wrap: wrap;
}

.usage-trend-panel__chart {
  min-width: 0;
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-surface);
  padding: 18px 20px 14px;
}

@media (max-width: 620px) {
  .usage-trend-panel :deep(.monitor-section-heading__actions) {
    width: 100%;
    justify-content: flex-end;
    margin-left: 0;
  }
}

.usage-analysis-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.45fr) minmax(300px, 0.9fr);
  align-items: start;
  gap: 20px;
}

.usage-token-grid,
.usage-quality-grid {
  display: grid;
  overflow: hidden;
  margin: 0;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-border-subtle);
  gap: 1px;
}

.usage-token-grid {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.usage-quality-grid {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.usage-token-grid > div,
.usage-quality-grid > div {
  min-width: 0;
  min-height: 78px;
  background: var(--color-surface);
  padding: 13px 15px;
}

.usage-token-grid dt,
.usage-quality-grid dt {
  display: flex;
  align-items: center;
  gap: 7px;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.usage-quality-grid dt::before {
  width: 6px;
  height: 6px;
  flex: 0 0 6px;
  border-radius: 50%;
  background: var(--color-warning);
  content: '';
}

.usage-quality-grid__danger dt::before {
  background: var(--color-danger);
}

.usage-token-grid dd,
.usage-quality-grid dd {
  margin: 7px 0 0;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: 1.1rem;
  font-variant-numeric: tabular-nums;
  font-weight: 560;
  letter-spacing: -0.035em;
}

.usage-buckets {
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-surface);
}

.usage-buckets > summary {
  display: flex;
  min-height: 48px;
  align-items: center;
  justify-content: space-between;
  padding: 0 16px;
  color: var(--color-text-muted);
  cursor: pointer;
  font-size: var(--text-sm);
  font-weight: 600;
  list-style: none;
}

.usage-buckets > summary::-webkit-details-marker {
  display: none;
}

.usage-buckets > summary::after {
  color: var(--color-text-faint);
  content: '＋';
}

.usage-buckets[open] > summary {
  border-bottom: 1px solid var(--color-border-subtle);
}

.usage-buckets[open] > summary::after {
  content: '−';
}

.usage-buckets__body {
  padding: 14px 16px;
}

@media (max-width: 980px) {
  .usage-analysis-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}

@media (max-width: 620px) {
  .usage-token-grid,
  .usage-quality-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .usage-trend-panel__chart {
    padding: 14px 12px 10px;
  }
}
</style>
