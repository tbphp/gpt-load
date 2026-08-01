<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { Database, TriangleAlert } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { groupOptionsQueryOptions } from '@/app/resources/groups'
import { modelPricesLocation, monitorLocation } from '@/app/route-locations'
import { usageQueryOptions, type UsageAggregateDto, type UsageFilters } from '@/app/resources/usage'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import TrendChart from '@/components/charts/TrendChart.vue'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import DataTable from '@/components/ui/DataTable.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'
import { formatEstimatedCost } from '@/lib/format'

import {
  applyUsageFilterDraft,
  createUsageFilterDraft,
  parseAppliedUsageFilters,
  resetUsageFilterDraft,
  validateUsageFilterDraft,
  type UsageFilterDraft,
  type UsageFilterErrors,
} from './usage-filters'
import { usageMonitorQuery } from './monitor-route'
import UsageFilterForm from './UsageFilterForm.vue'
import UsageSummary from './UsageSummary.vue'

const client = useApiClient()
const route = useRoute()
const router = useRouter()
const { locale, t } = useI18n()
const appliedFilters = computed(() => parseAppliedUsageFilters(route.query))
const draft = ref<UsageFilterDraft>(createUsageFilterDraft(appliedFilters.value))
const filterErrors = ref<UsageFilterErrors>({})

const groupsQuery = useQuery(groupOptionsQueryOptions(client))
const usageQuery = useQuery(usageQueryOptions(client, appliedFilters))
const report = computed(() => usageQuery.data.value)
const hasData = computed(() => (report.value?.summary.request_count ?? 0) > 0)
const draftDirty = computed(() => {
  const appliedDraft = createUsageFilterDraft(appliedFilters.value)
  return (
    draft.value.range !== appliedDraft.range ||
    draft.value.group_id !== appliedDraft.group_id ||
    draft.value.model !== appliedDraft.model
  )
})
const lastRefreshedAt = computed(() =>
  usageQuery.dataUpdatedAt.value > 0 ? usageQuery.dataUpdatedAt.value : null,
)
const unsavedChanges = useUnsavedChanges(draftDirty)

watch(
  [
    () => appliedFilters.value.range,
    () => appliedFilters.value.group_id,
    () => appliedFilters.value.model,
  ],
  () => {
    draft.value = createUsageFilterDraft(appliedFilters.value)
    filterErrors.value = {}
  },
)

function formatCount(value: number): string {
  return new Intl.NumberFormat(locale.value).format(value)
}

function formatAggregateEstimatedCost(aggregate: UsageAggregateDto): string {
  const cost = formatEstimatedCost(aggregate.estimated_cost_nano_usd, locale.value)
  if (aggregate.unpriced_request_count > 0) {
    return t('monitor.usage.cost.knownPlusUnknown', {
      cost,
    })
  }
  return cost
}

function groupLabel(groupID: number): string {
  if (groupID === 0) return t('monitor.usage.breakdown.unattributedGroup')
  const known = groupsQuery.data.value?.find((group) => group.id === groupID)
  if (known) return `${known.name} · #${groupID}`
  return t('monitor.usage.filters.deletedOrUnknown', { id: groupID })
}

function modelLabel(model: string): string {
  return model === '' ? t('monitor.usage.breakdown.unknownModel') : model
}

function updateDraftField(field: keyof UsageFilterDraft, value: string): void {
  draft.value = { ...draft.value, [field]: value }
}

async function applyFilters(): Promise<void> {
  const errors = validateUsageFilterDraft(draft.value)
  filterErrors.value = errors
  if (Object.keys(errors).length > 0) return
  await unsavedChanges.runWithoutPrompt(() => navigate(applyUsageFilterDraft(draft.value)))
}

function resetFilters(): void {
  draft.value = resetUsageFilterDraft()
  filterErrors.value = {}
}

async function navigate(filters: UsageFilters): Promise<void> {
  await router.push(monitorLocation(usageMonitorQuery(filters)))
}
</script>

<template>
  <div class="usage-tab">
    <UsageFilterForm
      :draft="draft"
      :errors="filterErrors"
      :groups="groupsQuery.data.value ?? []"
      :groups-failed="groupsQuery.isError.value"
      :fetching="usageQuery.isFetching.value"
      @update-field="updateDraftField"
      @apply="applyFilters"
      @reset="resetFilters"
      @refresh="usageQuery.refetch()"
    />

    <section class="usage-applied">
      <strong>{{ t('monitor.usage.filters.applied') }}</strong>
      <span class="usage-filter-chip">
        {{
          appliedFilters.range === '30d'
            ? t('monitor.usage.filters.range30d')
            : t('monitor.usage.filters.range24h')
        }}
      </span>
      <span class="usage-filter-chip">
        {{
          appliedFilters.group_id === undefined
            ? t('monitor.usage.filters.anyGroup')
            : groupLabel(appliedFilters.group_id)
        }}
      </span>
      <span class="usage-filter-chip">
        {{ appliedFilters.model ?? t('monitor.usage.filters.anyModel') }}
      </span>
    </section>
    <InlineFeedback v-if="draftDirty" tone="info">
      {{ t('monitor.usage.filters.dirty') }}
    </InlineFeedback>

    <InlineFeedback v-if="groupsQuery.isError.value" tone="warning">
      {{ t('monitor.usage.options.groupsFailed') }}
    </InlineFeedback>

    <SurfaceCard class="usage-scope">
      <Database :size="20" aria-hidden="true" />
      <div>
        <strong>{{ t('monitor.usage.scope.title') }}</strong>
        <p>{{ t('monitor.usage.scope.description') }}</p>
      </div>
    </SurfaceCard>

    <QueryFeedback
      v-if="usageQuery.isPending.value"
      state="loading"
      :message="t('monitor.usage.loading')"
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

      <section class="usage-freshness">
        <p>
          <strong>{{ t('monitor.usage.filters.observedAt') }}</strong>
          <AppDateTime :instant="report.observed_at_ms" :locale="locale" />
        </p>
        <p v-if="lastRefreshedAt">
          <strong>{{ t('monitor.usage.filters.refreshedAt') }}</strong>
          <AppDateTime :instant="lastRefreshedAt" :locale="locale" />
        </p>
      </section>

      <EmptyState
        v-if="!hasData"
        :title="t('monitor.usage.empty.title')"
        :description="t('monitor.usage.empty.description')"
      />
      <template v-else>
        <UsageSummary :observed-at-ms="report.observed_at_ms" :summary="report.summary" />

        <section class="usage-section" aria-labelledby="usage-quality-title">
          <div class="usage-heading">
            <div>
              <h2 id="usage-quality-title">{{ t('monitor.usage.quality.title') }}</h2>
              <p>{{ t('monitor.usage.quality.windowDescription') }}</p>
            </div>
          </div>
          <div class="usage-quality-grid">
            <SurfaceCard>
              <StatusBadge :tone="report.summary.usage_missing_count ? 'warning' : 'neutral'">
                {{ t('monitor.usage.quality.missing') }}
              </StatusBadge>
              <strong>{{ formatCount(report.summary.usage_missing_count) }}</strong>
            </SurfaceCard>
            <SurfaceCard>
              <StatusBadge :tone="report.summary.partial_count ? 'warning' : 'neutral'">
                {{ t('monitor.usage.quality.partial') }}
              </StatusBadge>
              <strong>{{ formatCount(report.summary.partial_count) }}</strong>
            </SurfaceCard>
            <SurfaceCard>
              <StatusBadge :tone="report.summary.unpriced_request_count ? 'warning' : 'neutral'">
                {{ t('monitor.usage.quality.unpriced') }}
              </StatusBadge>
              <strong>{{ formatCount(report.summary.unpriced_request_count) }}</strong>
            </SurfaceCard>
          </div>
          <InlineFeedback tone="info">
            {{ t('monitor.usage.quality.overlap') }}
          </InlineFeedback>
          <InlineFeedback v-if="report.summary.partial_count > 0" tone="warning">
            {{ t('monitor.usage.quality.partialExplanation') }}
          </InlineFeedback>
          <InlineFeedback v-if="report.summary.unpriced_request_count > 0" tone="warning">
            {{ t('monitor.usage.quality.unpricedExplanation') }}
          </InlineFeedback>
          <p class="usage-note">
            {{ t('monitor.usage.quality.aggregation') }}
            <RouterLink
              v-if="report.summary.unpriced_request_count > 0"
              :to="modelPricesLocation()"
            >
              {{ t('monitor.usage.quality.openPrices') }}
            </RouterLink>
          </p>
        </section>
      </template>

      <section class="usage-section" aria-labelledby="usage-process-health-title">
        <div class="usage-heading">
          <div>
            <h2 id="usage-process-health-title">{{ t('monitor.usage.process.title') }}</h2>
            <p>{{ t('monitor.usage.process.description') }}</p>
          </div>
        </div>
        <div class="usage-process-grid">
          <SurfaceCard>
            <StatusBadge :tone="report.collection_health.dropped_total ? 'danger' : 'neutral'">
              {{ t('monitor.usage.quality.dropped') }}
            </StatusBadge>
            <strong>{{ formatCount(report.collection_health.dropped_total) }}</strong>
          </SurfaceCard>
          <SurfaceCard>
            <StatusBadge
              :tone="report.collection_health.write_failure_total ? 'danger' : 'neutral'"
            >
              {{ t('monitor.usage.quality.writeFailures') }}
            </StatusBadge>
            <strong>{{ formatCount(report.collection_health.write_failure_total) }}</strong>
          </SurfaceCard>
          <SurfaceCard>
            <span>{{ t('monitor.usage.process.lastWriteFailure') }}</span>
            <strong>
              <AppDateTime
                v-if="report.collection_health.last_write_failure_at_ms !== null"
                :instant="report.collection_health.last_write_failure_at_ms"
                :locale="locale"
              />
              <template v-else>{{ t('monitor.usage.process.never') }}</template>
            </strong>
          </SurfaceCard>
        </div>
        <InlineFeedback v-if="report.collection_health.dropped_total > 0" tone="danger">
          {{ t('monitor.usage.process.droppedWarning') }}
        </InlineFeedback>
        <InlineFeedback v-if="report.collection_health.write_failure_total > 0" tone="danger">
          {{ t('monitor.usage.process.writeFailureWarning') }}
        </InlineFeedback>
        <InlineFeedback
          v-if="
            report.collection_health.dropped_total === 0 &&
            report.collection_health.write_failure_total === 0
          "
          tone="info"
        >
          {{ t('monitor.usage.process.clear') }}
        </InlineFeedback>
      </section>

      <template v-if="hasData">
        <section class="usage-section" aria-labelledby="usage-trend-title">
          <div class="usage-heading">
            <div>
              <h2 id="usage-trend-title">{{ t('monitor.usage.trend.title') }}</h2>
              <p>{{ t('monitor.usage.trend.description') }}</p>
            </div>
          </div>
          <SurfaceCard>
            <TrendChart
              :series="report.series"
              :title="t('monitor.usage.trend.title')"
              :description="t('monitor.usage.trend.accessibleDescription')"
              :empty-label="t('monitor.usage.trend.empty')"
              :request-label="t('monitor.usage.columns.requests')"
              :failure-label="t('monitor.usage.columns.failure')"
              :range-start="report.from_ms"
              :range-end="report.to_ms"
            />
          </SurfaceCard>
        </section>

        <section class="usage-section" aria-labelledby="usage-series-title">
          <div class="usage-heading">
            <div>
              <h2 id="usage-series-title">{{ t('monitor.usage.series.title') }}</h2>
              <p>{{ t('monitor.usage.series.description') }}</p>
            </div>
          </div>
          <DataTable :caption="t('monitor.usage.series.caption')" dense>
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
                <td>
                  <AppDateTime :instant="bucket.bucket_start_ms" :locale="locale" />
                  <span>–</span>
                  <AppDateTime :instant="bucket.bucket_end_ms" :locale="locale" />
                </td>
                <td>{{ formatCount(bucket.request_count) }}</td>
                <td>{{ formatCount(bucket.success_count) }}</td>
                <td>{{ formatCount(bucket.failure_count) }}</td>
                <td>{{ formatCount(bucket.total_tokens) }}</td>
                <td>
                  {{ formatAggregateEstimatedCost(bucket) }}
                </td>
                <td>
                  {{
                    t('monitor.usage.columns.qualityCounts', {
                      missing: formatCount(bucket.usage_missing_count),
                      partial: formatCount(bucket.partial_count),
                      unpriced: formatCount(bucket.unpriced_request_count),
                    })
                  }}
                </td>
              </tr>
            </tbody>
          </DataTable>
        </section>

        <section class="usage-section" aria-labelledby="usage-breakdown-title">
          <div class="usage-heading">
            <div>
              <h2 id="usage-breakdown-title">{{ t('monitor.usage.breakdown.title') }}</h2>
              <p>{{ t('monitor.usage.breakdown.description') }}</p>
            </div>
          </div>
          <InlineFeedback v-if="report.breakdown_truncated" tone="warning">
            <TriangleAlert :size="16" aria-hidden="true" />
            {{ t('monitor.usage.breakdown.truncated') }}
          </InlineFeedback>
          <DataTable :caption="t('monitor.usage.breakdown.caption')" dense>
            <thead>
              <tr>
                <th scope="col">{{ t('monitor.usage.columns.group') }}</th>
                <th scope="col">{{ t('monitor.usage.columns.upstreamModel') }}</th>
                <th scope="col">{{ t('monitor.usage.columns.requests') }}</th>
                <th scope="col">{{ t('monitor.usage.tokens.uncachedInput') }}</th>
                <th scope="col">{{ t('monitor.usage.tokens.cacheRead') }}</th>
                <th scope="col">{{ t('monitor.usage.tokens.cacheWrite5m') }}</th>
                <th scope="col">{{ t('monitor.usage.tokens.cacheWrite1h') }}</th>
                <th scope="col">{{ t('monitor.usage.tokens.output') }}</th>
                <th scope="col">{{ t('monitor.usage.columns.totalTokens') }}</th>
                <th scope="col">{{ t('monitor.usage.columns.estimatedCost') }}</th>
                <th scope="col">{{ t('monitor.usage.columns.quality') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in report.breakdown" :key="`${row.group_id}:${row.model}`">
                <td>
                  {{ groupLabel(row.group_id) }}
                </td>
                <td>
                  <code>{{ modelLabel(row.model) }}</code>
                </td>
                <td>{{ formatCount(row.request_count) }}</td>
                <td>{{ formatCount(row.uncached_input_tokens) }}</td>
                <td>{{ formatCount(row.cache_read_tokens) }}</td>
                <td>{{ formatCount(row.cache_write_5m_tokens) }}</td>
                <td>{{ formatCount(row.cache_write_1h_tokens) }}</td>
                <td>{{ formatCount(row.output_tokens) }}</td>
                <td>{{ formatCount(row.total_tokens) }}</td>
                <td>
                  {{ formatAggregateEstimatedCost(row) }}
                </td>
                <td>
                  {{
                    t('monitor.usage.columns.qualityCounts', {
                      missing: formatCount(row.usage_missing_count),
                      partial: formatCount(row.partial_count),
                      unpriced: formatCount(row.unpriced_request_count),
                    })
                  }}
                </td>
              </tr>
            </tbody>
          </DataTable>
        </section>
      </template>
    </template>
  </div>
</template>

<style scoped>
.usage-tab,
.usage-section {
  display: grid;
  min-width: 0;
  gap: var(--space-4);
}

.usage-scope,
.usage-applied,
.usage-freshness {
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-surface);
  padding: var(--space-4);
  box-shadow: var(--shadow-card);
}

.usage-scope,
.usage-heading,
.usage-applied,
.usage-freshness {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-3);
}

.usage-scope {
  align-items: flex-start;
}

.usage-applied {
  box-shadow: none;
}

.usage-filter-chip {
  border: 1px solid var(--color-border-subtle);
  border-radius: 999px;
  background: var(--color-surface-sunken);
  padding: var(--space-1) var(--space-2);
  font-size: 0.8125rem;
}

.usage-freshness {
  justify-content: space-between;
  box-shadow: none;
}

.usage-freshness p {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin: 0;
}

.usage-scope > svg {
  flex: 0 0 auto;
  color: var(--color-action);
}

.usage-scope p,
.usage-heading p,
.usage-note {
  margin: var(--space-1) 0 0;
  color: var(--color-text-muted);
}

.usage-heading h2 {
  margin: 0;
  font-size: 1rem;
}

.usage-quality-grid,
.usage-process-grid {
  display: grid;
  gap: var(--space-3);
}

.usage-quality-grid {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.usage-process-grid {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.usage-process-grid span {
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}

.usage-quality-grid > :deep(.surface-card),
.usage-process-grid > :deep(.surface-card) {
  display: grid;
  min-width: 0;
  gap: var(--space-2);
}

.usage-quality-grid strong,
.usage-process-grid strong {
  font-size: 1.25rem;
  overflow-wrap: anywhere;
}

.usage-note a {
  color: var(--color-action);
  font-weight: 650;
}

.usage-tab code,
.usage-tab time {
  font-family: var(--font-mono);
  overflow-wrap: anywhere;
}

.usage-tab td:first-child {
  min-width: 220px;
}

@media (max-width: 1000px) {
  .usage-quality-grid,
  .usage-process-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 720px) {
  .usage-quality-grid,
  .usage-process-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
