<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import {
  Activity,
  CircleDollarSign,
  Database,
  Gauge,
  RefreshCw,
  TriangleAlert,
} from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { listGroups } from '@/api/control/groups'
import { getUsageReport, type UsageAggregateDto, type UsageFilters } from '@/api/control/usage'
import { controlQueryKeys } from '@/app/query-keys'
import AppButton from '@/components/ui/AppButton.vue'
import DataTable from '@/components/ui/DataTable.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import FormField from '@/components/ui/FormField.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'
import { formatEstimatedUSD } from '@/features/usage/estimated-cost'

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
import UsageSparkline from './UsageSparkline.vue'

const client = useApiClient()
const route = useRoute()
const router = useRouter()
const { locale, t } = useI18n()
const appliedFilters = computed(() => parseAppliedUsageFilters(route.query))
const draft = ref<UsageFilterDraft>(createUsageFilterDraft(appliedFilters.value))
const filterErrors = ref<UsageFilterErrors>({})

const groupsQuery = useQuery({
  queryKey: controlQueryKeys.groups.list(),
  queryFn: ({ signal }) => listGroups(client, signal),
})
const usageQuery = useQuery({
  queryKey: computed(() => controlQueryKeys.usage.report(appliedFilters.value)),
  queryFn: ({ signal }) => getUsageReport(client, appliedFilters.value, signal),
})
const report = computed(() => usageQuery.data.value)
const hasData = computed(() => (report.value?.summary.request_count ?? 0) > 0)

watch([() => appliedFilters.value.group_id, () => appliedFilters.value.model], () => {
  draft.value = createUsageFilterDraft(appliedFilters.value)
  filterErrors.value = {}
})

function formatCount(value: number): string {
  return new Intl.NumberFormat(locale.value).format(value)
}

function formatEstimatedCost(aggregate: UsageAggregateDto): string {
  if (aggregate.unpriced_request_count > 0) {
    return t('monitor.usage.cost.knownPlusUnknown', {
      cost: formatEstimatedUSD(aggregate.estimated_cost_usd, locale.value),
    })
  }
  return formatEstimatedUSD(aggregate.estimated_cost_usd, locale.value)
}

function groupLabel(groupID: number): string {
  const known = groupsQuery.data.value?.find((group) => group.id === groupID)
  if (known) return `${known.name} · #${groupID}`
  return t('monitor.usage.filters.deletedOrUnknown', { id: groupID })
}

function filterError(field: keyof UsageFilterDraft): string | undefined {
  const key = filterErrors.value[field]
  return key ? t(key) : undefined
}

async function changeRange(event: Event): Promise<void> {
  const range = (event.target as HTMLSelectElement).value === '30d' ? '30d' : '24h'
  await navigate({ ...appliedFilters.value, range })
}

async function applyFilters(): Promise<void> {
  const errors = validateUsageFilterDraft(draft.value)
  filterErrors.value = errors
  if (Object.keys(errors).length > 0) return
  await navigate(applyUsageFilterDraft(appliedFilters.value.range, draft.value))
}

async function resetFilters(): Promise<void> {
  draft.value = resetUsageFilterDraft()
  filterErrors.value = {}
  await navigate({ range: appliedFilters.value.range })
}

async function navigate(filters: UsageFilters): Promise<void> {
  await router.push({ name: 'monitor', query: usageMonitorQuery(filters) })
}
</script>

<template>
  <div class="usage-tab">
    <form
      class="usage-filter-form"
      data-test="usage-filter-form"
      :aria-label="t('monitor.usage.filters.label')"
      @submit.prevent="applyFilters"
    >
      <div class="usage-filter-grid">
        <FormField id="usage-range" :label="t('monitor.usage.filters.range')">
          <select
            id="usage-range"
            data-test="usage-range"
            :value="appliedFilters.range"
            @change="changeRange"
          >
            <option value="24h">{{ t('monitor.usage.filters.range24h') }}</option>
            <option value="30d">{{ t('monitor.usage.filters.range30d') }}</option>
          </select>
        </FormField>
        <FormField
          id="usage-group"
          :label="t('monitor.usage.filters.group')"
          :description="
            groupsQuery.isError.value
              ? t('monitor.usage.filters.groupIdHelp')
              : t('monitor.usage.filters.groupHelp')
          "
          :error="filterError('group_id')"
        >
          <template #default="{ describedBy }">
            <input
              v-if="groupsQuery.isError.value"
              id="usage-group"
              v-model="draft.group_id"
              data-test="usage-group"
              type="text"
              inputmode="numeric"
              autocomplete="off"
              :aria-describedby="describedBy"
              :aria-invalid="filterError('group_id') ? 'true' : undefined"
            />
            <select
              v-else
              id="usage-group"
              v-model="draft.group_id"
              data-test="usage-group"
              :aria-describedby="describedBy"
              :aria-invalid="filterError('group_id') ? 'true' : undefined"
            >
              <option value="">{{ t('monitor.usage.filters.anyGroup') }}</option>
              <option
                v-if="
                  draft.group_id &&
                  !groupsQuery.data.value?.some((group) => String(group.id) === draft.group_id)
                "
                :value="draft.group_id"
              >
                {{
                  t('monitor.usage.filters.deletedOrUnknown', {
                    id: draft.group_id,
                  })
                }}
              </option>
              <option
                v-for="group in groupsQuery.data.value ?? []"
                :key="group.id"
                :value="String(group.id)"
              >
                {{ group.name }} · #{{ group.id }}
              </option>
            </select>
          </template>
        </FormField>
        <FormField
          id="usage-model"
          :label="t('monitor.usage.filters.model')"
          :description="t('monitor.usage.filters.modelHelp')"
          :error="filterError('model')"
        >
          <template #default="{ describedBy }">
            <input
              id="usage-model"
              v-model="draft.model"
              data-test="usage-model"
              type="text"
              autocomplete="off"
              :aria-describedby="describedBy"
              :aria-invalid="filterError('model') ? 'true' : undefined"
            />
          </template>
        </FormField>
      </div>
      <div class="usage-filter-actions">
        <AppButton
          data-test="usage-refresh"
          type="button"
          variant="secondary"
          @click="usageQuery.refetch()"
        >
          <RefreshCw :size="16" aria-hidden="true" />{{ t('monitor.usage.filters.refresh') }}
        </AppButton>
        <AppButton type="submit">{{ t('monitor.usage.filters.apply') }}</AppButton>
        <AppButton data-test="usage-reset" variant="ghost" @click="resetFilters">
          {{ t('monitor.usage.filters.reset') }}
        </AppButton>
      </div>
    </form>

    <InlineFeedback
      v-if="groupsQuery.isError.value"
      data-test="usage-group-options-failed"
      tone="warning"
    >
      {{ t('monitor.usage.options.groupsFailed') }}
    </InlineFeedback>

    <SurfaceCard class="usage-scope" data-test="usage-scope">
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
        data-test="usage-stale"
        state="stale"
        :message="t('monitor.usage.stale')"
        :retry-label="t('common.retry')"
        @retry="usageQuery.refetch()"
      />

      <EmptyState
        v-if="!hasData"
        data-test="usage-empty"
        :title="t('monitor.usage.empty.title')"
        :description="t('monitor.usage.empty.description')"
      />
      <template v-else>
        <section class="usage-section" aria-labelledby="usage-kpi-title">
          <div class="usage-heading">
            <div>
              <h2 id="usage-kpi-title">{{ t('monitor.usage.kpi.title') }}</h2>
              <p>{{ t('monitor.usage.kpi.description', { time: report.observed_at }) }}</p>
            </div>
          </div>
          <div class="usage-kpi-grid">
            <SurfaceCard class="usage-kpi">
              <Activity :size="20" aria-hidden="true" />
              <span>{{ t('monitor.usage.kpi.requests') }}</span>
              <strong>{{ formatCount(report.summary.request_count) }}</strong>
            </SurfaceCard>
            <SurfaceCard class="usage-kpi" data-test="usage-kpi-outcomes">
              <Gauge :size="20" aria-hidden="true" />
              <span>{{ t('monitor.usage.kpi.outcomes') }}</span>
              <strong>
                {{
                  t('monitor.usage.kpi.outcomeCounts', {
                    success: formatCount(report.summary.success_count),
                    failure: formatCount(report.summary.failure_count),
                  })
                }}
              </strong>
            </SurfaceCard>
            <SurfaceCard class="usage-kpi" data-test="usage-kpi-total-tokens">
              <Database :size="20" aria-hidden="true" />
              <span>{{ t('monitor.usage.kpi.totalTokens') }}</span>
              <strong>{{ formatCount(report.summary.total_tokens) }}</strong>
            </SurfaceCard>
            <SurfaceCard class="usage-kpi" data-test="usage-kpi-cost">
              <CircleDollarSign :size="20" aria-hidden="true" />
              <span>{{ t('monitor.usage.kpi.estimatedCost') }}</span>
              <strong>{{ formatEstimatedCost(report.summary) }}</strong>
            </SurfaceCard>
          </div>
          <dl
            class="usage-token-definition"
            data-test="usage-summary-token-definition"
            :aria-label="t('monitor.usage.tokens.title')"
          >
            <div>
              <dt>{{ t('monitor.usage.tokens.uncachedInput') }}</dt>
              <dd>{{ formatCount(report.summary.uncached_input_tokens) }}</dd>
            </div>
            <div>
              <dt>{{ t('monitor.usage.tokens.cacheRead') }}</dt>
              <dd>{{ formatCount(report.summary.cache_read_tokens) }}</dd>
            </div>
            <div>
              <dt>{{ t('monitor.usage.tokens.cacheWrite5m') }}</dt>
              <dd>{{ formatCount(report.summary.cache_write_5m_tokens) }}</dd>
            </div>
            <div>
              <dt>{{ t('monitor.usage.tokens.cacheWrite1h') }}</dt>
              <dd>{{ formatCount(report.summary.cache_write_1h_tokens) }}</dd>
            </div>
            <div>
              <dt>{{ t('monitor.usage.tokens.output') }}</dt>
              <dd>{{ formatCount(report.summary.output_tokens) }}</dd>
            </div>
          </dl>
        </section>

        <section
          class="usage-section"
          data-test="usage-window-quality"
          aria-labelledby="usage-quality-title"
        >
          <div class="usage-heading">
            <div>
              <h2 id="usage-quality-title">{{ t('monitor.usage.quality.title') }}</h2>
              <p>{{ t('monitor.usage.quality.windowDescription') }}</p>
            </div>
          </div>
          <div class="usage-quality-grid">
            <SurfaceCard data-test="usage-quality-missing">
              <StatusBadge :tone="report.summary.usage_missing_count ? 'warning' : 'neutral'">
                {{ t('monitor.usage.quality.missing') }}
              </StatusBadge>
              <strong>{{ formatCount(report.summary.usage_missing_count) }}</strong>
            </SurfaceCard>
            <SurfaceCard data-test="usage-quality-partial">
              <StatusBadge :tone="report.summary.partial_count ? 'warning' : 'neutral'">
                {{ t('monitor.usage.quality.partial') }}
              </StatusBadge>
              <strong>{{ formatCount(report.summary.partial_count) }}</strong>
            </SurfaceCard>
            <SurfaceCard data-test="usage-quality-unpriced">
              <StatusBadge :tone="report.summary.unpriced_request_count ? 'warning' : 'neutral'">
                {{ t('monitor.usage.quality.unpriced') }}
              </StatusBadge>
              <strong>{{ formatCount(report.summary.unpriced_request_count) }}</strong>
            </SurfaceCard>
          </div>
          <InlineFeedback data-test="usage-quality-overlap" tone="info">
            {{ t('monitor.usage.quality.overlap') }}
          </InlineFeedback>
          <InlineFeedback
            v-if="report.summary.partial_count > 0"
            data-test="usage-partial-explanation"
            tone="warning"
          >
            {{ t('monitor.usage.quality.partialExplanation') }}
          </InlineFeedback>
          <InlineFeedback
            v-if="report.summary.unpriced_request_count > 0"
            data-test="usage-unpriced-explanation"
            tone="warning"
          >
            {{ t('monitor.usage.quality.unpricedExplanation') }}
          </InlineFeedback>
          <p class="usage-note" data-test="usage-aggregation-note">
            {{ t('monitor.usage.quality.aggregation') }}
            <RouterLink
              v-if="report.summary.unpriced_request_count > 0"
              data-test="usage-prices-link"
              to="/settings/model-prices"
            >
              {{ t('monitor.usage.quality.openPrices') }}
            </RouterLink>
          </p>
        </section>
      </template>

      <section
        class="usage-section"
        data-test="usage-process-health"
        aria-labelledby="usage-process-health-title"
      >
        <div class="usage-heading">
          <div>
            <h2 id="usage-process-health-title">{{ t('monitor.usage.process.title') }}</h2>
            <p>{{ t('monitor.usage.process.description') }}</p>
          </div>
        </div>
        <div class="usage-process-grid">
          <SurfaceCard data-test="usage-quality-dropped">
            <StatusBadge :tone="report.collection_health.dropped_total ? 'danger' : 'neutral'">
              {{ t('monitor.usage.quality.dropped') }}
            </StatusBadge>
            <strong>{{ formatCount(report.collection_health.dropped_total) }}</strong>
          </SurfaceCard>
          <SurfaceCard data-test="usage-quality-write-failures">
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
              <time
                v-if="report.collection_health.last_write_failure_at"
                :datetime="report.collection_health.last_write_failure_at"
              >
                {{ report.collection_health.last_write_failure_at }}
              </time>
              <template v-else>{{ t('monitor.usage.process.never') }}</template>
            </strong>
          </SurfaceCard>
        </div>
        <InlineFeedback
          v-if="report.collection_health.dropped_total > 0"
          data-test="usage-process-dropped-warning"
          tone="danger"
        >
          {{ t('monitor.usage.process.droppedWarning') }}
        </InlineFeedback>
        <InlineFeedback
          v-if="report.collection_health.write_failure_total > 0"
          data-test="usage-process-write-failure-warning"
          tone="danger"
        >
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
            <UsageSparkline
              :range="{
                from: report.from,
                to: report.to,
                granularity: report.granularity,
              }"
              :series="report.series"
              :title="t('monitor.usage.trend.title')"
              :description="t('monitor.usage.trend.accessibleDescription')"
              :empty-label="t('monitor.usage.trend.empty')"
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
          <DataTable
            data-test="usage-series-table"
            :caption="t('monitor.usage.series.caption')"
            dense
          >
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
              <tr v-for="(bucket, index) in report.series" :key="bucket.bucket_start">
                <td>
                  <time :datetime="bucket.bucket_start">{{ bucket.bucket_start }}</time>
                  <span>–</span>
                  <time :datetime="bucket.bucket_end">{{ bucket.bucket_end }}</time>
                </td>
                <td>{{ formatCount(bucket.request_count) }}</td>
                <td>{{ formatCount(bucket.success_count) }}</td>
                <td>{{ formatCount(bucket.failure_count) }}</td>
                <td>{{ formatCount(bucket.total_tokens) }}</td>
                <td :data-test="`usage-series-cost-${index}`">
                  {{ formatEstimatedCost(bucket) }}
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
          <InlineFeedback
            v-if="report.breakdown_truncated"
            data-test="usage-breakdown-truncated"
            tone="warning"
          >
            <TriangleAlert :size="16" aria-hidden="true" />
            {{ t('monitor.usage.breakdown.truncated') }}
          </InlineFeedback>
          <DataTable
            data-test="usage-breakdown-table"
            :caption="t('monitor.usage.breakdown.caption')"
            dense
          >
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
              <tr v-for="(row, index) in report.breakdown" :key="`${row.group_id}:${row.model}`">
                <td>
                  {{ groupLabel(row.group_id) }}
                </td>
                <td>
                  <code>{{ row.model }}</code>
                </td>
                <td>{{ formatCount(row.request_count) }}</td>
                <td>{{ formatCount(row.uncached_input_tokens) }}</td>
                <td>{{ formatCount(row.cache_read_tokens) }}</td>
                <td>{{ formatCount(row.cache_write_5m_tokens) }}</td>
                <td>{{ formatCount(row.cache_write_1h_tokens) }}</td>
                <td>{{ formatCount(row.output_tokens) }}</td>
                <td>{{ formatCount(row.total_tokens) }}</td>
                <td :data-test="`usage-breakdown-cost-${index}`">
                  {{ formatEstimatedCost(row) }}
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

.usage-filter-form,
.usage-scope {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-card);
  background: var(--color-surface);
  padding: var(--space-4);
  box-shadow: var(--shadow-card);
}

.usage-filter-form {
  display: grid;
  gap: var(--space-4);
}

.usage-filter-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(180px, 1fr));
  gap: var(--space-3);
}

.usage-filter-form input,
.usage-filter-form select {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 8px 10px;
  font: inherit;
}

.usage-filter-actions,
.usage-scope,
.usage-heading {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-3);
}

.usage-scope {
  align-items: flex-start;
}

.usage-scope > svg {
  flex: 0 0 auto;
  color: var(--color-primary);
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

.usage-kpi-grid,
.usage-quality-grid,
.usage-process-grid,
.usage-token-definition {
  display: grid;
  gap: var(--space-3);
}

.usage-kpi-grid {
  grid-template-columns: repeat(4, minmax(0, 1fr));
}

.usage-quality-grid {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.usage-process-grid {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.usage-token-definition {
  grid-template-columns: repeat(5, minmax(0, 1fr));
  margin: 0;
}

.usage-token-definition > div {
  border-top: 1px solid var(--color-border);
  padding-top: var(--space-2);
}

.usage-token-definition dt,
.usage-process-grid span {
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}

.usage-token-definition dd {
  margin: var(--space-1) 0 0;
  font-weight: 700;
}

.usage-kpi,
.usage-quality-grid > :deep(.surface-card),
.usage-process-grid > :deep(.surface-card) {
  display: grid;
  min-width: 0;
  gap: var(--space-2);
}

.usage-kpi > svg {
  color: var(--color-primary);
}

.usage-kpi > span {
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}

.usage-kpi strong,
.usage-quality-grid strong,
.usage-process-grid strong {
  font-size: 1.25rem;
  overflow-wrap: anywhere;
}

.usage-note a {
  color: var(--color-primary);
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
  .usage-kpi-grid,
  .usage-quality-grid,
  .usage-process-grid,
  .usage-token-definition {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 720px) {
  .usage-filter-grid,
  .usage-kpi-grid,
  .usage-quality-grid,
  .usage-process-grid,
  .usage-token-definition {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
