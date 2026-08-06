<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { ChevronRight, Database } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { groupOptionsQueryOptions } from '@/app/resources/groups'
import {
  usageQueryOptions,
  type UsageAggregateDto,
  type UsageBreakdownOrder,
  type UsageFilters,
  type UsageRange,
} from '@/app/resources/usage'
import { monitorLocation } from '@/app/route-locations'
import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import TrendChart from '@/components/charts/TrendChart.vue'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import DataTable from '@/components/ui/DataTable.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatEstimatedCost, formatInteger, formatTokens } from '@/lib/format'

import MonitorSectionHeading from './MonitorSectionHeading.vue'
import {
  applyUsageFilterDraft,
  createUsageFilterDraft,
  parseAppliedUsageFilters,
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
const filterOpen = ref(false)
const appliedFilters = computed(() => parseAppliedUsageFilters(route.query))
const draft = ref<UsageFilterDraft>(createUsageFilterDraft(appliedFilters.value))
const filterErrors = ref<UsageFilterErrors>({})

const groupsQuery = useQuery(groupOptionsQueryOptions(client))
const usageQuery = useQuery(usageQueryOptions(client, appliedFilters))
const report = computed(() => usageQuery.data.value)
const hasData = computed(() => (report.value?.summary.request_count ?? 0) > 0)
const orderOptions = computed(() => [
  { value: 'requests', label: t('monitor.usage.breakdown.orderRequests') },
  { value: 'cost', label: t('monitor.usage.breakdown.orderCost') },
])

watch(
  [
    () => appliedFilters.value.range,
    () => appliedFilters.value.group_id,
    () => appliedFilters.value.model,
    () => appliedFilters.value.breakdown_order,
  ],
  () => {
    draft.value = createUsageFilterDraft(appliedFilters.value)
    filterErrors.value = {}
  },
)

function rangeLabel(range: UsageRange): string {
  return t(`monitor.usage.filters.ranges.${range}`)
}

function granularityLabel(): string {
  return report.value?.granularity === 'hour'
    ? t('monitor.usage.trend.hourly')
    : t('monitor.usage.trend.daily')
}

function formatCost(aggregate: UsageAggregateDto): string {
  return formatEstimatedCost(aggregate.estimated_cost_nano_usd, locale.value)
}

function groupName(groupID: number): string {
  if (groupID === 0) return t('monitor.usage.breakdown.unattributedGroup')
  return (
    groupsQuery.data.value?.find((group) => group.id === groupID)?.name ??
    t('monitor.usage.filters.deletedOrUnknown', { id: groupID })
  )
}

function groupMeta(groupID: number): string {
  return groupID === 0 ? t('monitor.usage.breakdown.unattributedMeta') : `Group #${groupID}`
}

function modelLabel(model: string): string {
  return model === '' ? t('monitor.usage.breakdown.unknownModel') : model
}

function updateDraftField(field: keyof UsageFilterDraft, value: string): void {
  draft.value = { ...draft.value, [field]: value }
}

function setFilterOpen(open: boolean): void {
  filterOpen.value = open
  if (!open) {
    draft.value = createUsageFilterDraft(appliedFilters.value)
    filterErrors.value = {}
  }
}

function openFilters(): void {
  draft.value = createUsageFilterDraft(appliedFilters.value)
  filterErrors.value = {}
  filterOpen.value = true
}

async function applyFilters(): Promise<void> {
  const errors = validateUsageFilterDraft(draft.value)
  filterErrors.value = errors
  if (Object.keys(errors).length > 0) return
  await navigate(applyUsageFilterDraft(draft.value))
  filterOpen.value = false
}

async function resetFilters(): Promise<void> {
  await navigate({
    range: appliedFilters.value.range,
    breakdown_order: appliedFilters.value.breakdown_order,
  })
  filterOpen.value = false
}

async function updateBreakdownOrder(value: string): Promise<void> {
  if (value !== 'requests' && value !== 'cost') return
  await navigate({ ...appliedFilters.value, breakdown_order: value as UsageBreakdownOrder })
}

async function navigate(filters: UsageFilters): Promise<void> {
  await router.push(monitorLocation(usageMonitorQuery(filters)))
}

defineExpose({ openFilters })
</script>

<template>
  <div class="usage-tab">
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

      <UsageSummary :range-label="rangeLabel(report.range)" :summary="report.summary" />

      <InlineFeedback
        v-if="
          report.collection_health.dropped_total > 0 ||
          report.collection_health.write_failure_total > 0
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
            :title="t('monitor.usage.trend.title')"
            :description="t('monitor.usage.trend.description')"
            :meta="`${rangeLabel(report.range)} · ${granularityLabel()}`"
          />
          <div class="usage-trend-panel__chart">
            <TrendChart
              :series="report.series"
              :title="t('monitor.usage.trend.title')"
              :description="t('monitor.usage.trend.accessibleDescription')"
              :empty-label="t('monitor.usage.trend.empty')"
              :request-label="t('monitor.usage.columns.requests')"
              :failure-label="t('monitor.usage.columns.failure')"
              :range-start="report.from_ms"
              :range-end="report.to_ms"
              :locale="locale"
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

        <section class="usage-breakdown" aria-labelledby="usage-breakdown-title">
          <MonitorSectionHeading
            id="usage-breakdown-title"
            :title="t('monitor.usage.breakdown.title')"
            :description="t('monitor.usage.breakdown.description')"
            :meta="
              t('monitor.usage.breakdown.count', {
                count: formatInteger(report.breakdown_count, locale),
              })
            "
          >
            <template #actions>
              <StatusBadge v-if="report.breakdown_truncated" tone="warning" size="compact">
                {{ t('monitor.usage.breakdown.truncatedShort') }}
              </StatusBadge>
              <AppSelect
                :model-value="report.breakdown_order"
                :label="t('monitor.usage.breakdown.orderLabel')"
                :options="orderOptions"
                size="compact"
                @update:model-value="updateBreakdownOrder"
              />
            </template>
          </MonitorSectionHeading>

          <LedgerRecordList
            :label="t('monitor.usage.breakdown.caption')"
            :row-count="report.breakdown.length + 1"
            grid-class="usage-breakdown-grid"
          >
            <template #header>
              <span role="columnheader">{{ t('monitor.usage.columns.group') }}</span>
              <span role="columnheader">{{ t('monitor.usage.columns.upstreamModel') }}</span>
              <span role="columnheader">{{ t('monitor.usage.columns.requests') }}</span>
              <span role="columnheader">{{ t('monitor.usage.columns.totalTokens') }}</span>
              <span role="columnheader">{{ t('monitor.usage.columns.estimatedCost') }}</span>
              <span role="columnheader">{{ t('monitor.usage.columns.quality') }}</span>
              <span role="columnheader">{{ t('monitor.usage.columns.actions') }}</span>
            </template>

            <details
              v-for="(row, index) in report.breakdown"
              :key="`${row.group_id}:${row.model}`"
              class="usage-breakdown-record"
            >
              <summary
                class="ledger-record-list__record usage-breakdown-record__summary"
                role="row"
                :aria-rowindex="index + 2"
              >
                <div class="ledger-record-list__cell usage-breakdown-record__identity" role="cell">
                  <strong>{{ groupName(row.group_id) }}</strong>
                  <small>{{ groupMeta(row.group_id) }}</small>
                </div>
                <div class="ledger-record-list__cell usage-breakdown-record__model" role="cell">
                  <span class="usage-cell-label">{{
                    t('monitor.usage.columns.upstreamModel')
                  }}</span>
                  <code>{{ modelLabel(row.model) }}</code>
                </div>
                <div class="ledger-record-list__cell usage-breakdown-record__number" role="cell">
                  <span class="usage-cell-label">{{ t('monitor.usage.columns.requests') }}</span>
                  <span>{{ formatInteger(row.request_count, locale) }}</span>
                </div>
                <div class="ledger-record-list__cell usage-breakdown-record__number" role="cell">
                  <span class="usage-cell-label">{{ t('monitor.usage.columns.totalTokens') }}</span>
                  <span>{{ formatTokens(row.total_tokens, locale) }}</span>
                </div>
                <div class="ledger-record-list__cell usage-breakdown-record__cost" role="cell">
                  <span class="usage-cell-label">{{
                    t('monitor.usage.columns.estimatedCost')
                  }}</span>
                  <span>{{ formatCost(row) }}</span>
                  <small
                    v-if="
                      row.usage_missing_count > 0 ||
                      row.partial_count > 0 ||
                      row.unpriced_request_count > 0 ||
                      row.pricing_partial_count > 0
                    "
                  >
                    {{ t('monitor.usage.cost.knownPlusUnknownShort') }}
                  </small>
                </div>
                <div class="ledger-record-list__cell usage-breakdown-record__quality" role="cell">
                  <span class="usage-cell-label">{{ t('monitor.usage.columns.quality') }}</span>
                  <span>
                    {{
                      t('monitor.usage.columns.qualityUsage', {
                        missing: formatInteger(row.usage_missing_count, locale),
                        partial: formatInteger(row.partial_count, locale),
                      })
                    }}
                  </span>
                  <small>
                    {{
                      t('monitor.usage.columns.qualityPricing', {
                        unpriced: formatInteger(row.unpriced_request_count, locale),
                        partial: formatInteger(row.pricing_partial_count, locale),
                      })
                    }}
                  </small>
                </div>
                <div class="ledger-record-list__cell usage-breakdown-record__action" role="cell">
                  <ChevronRight :size="17" aria-hidden="true" />
                </div>
              </summary>

              <dl class="usage-breakdown-record__detail">
                <div>
                  <dt>{{ t('monitor.usage.columns.success') }}</dt>
                  <dd>{{ formatInteger(row.success_count, locale) }}</dd>
                </div>
                <div>
                  <dt>{{ t('monitor.usage.columns.failure') }}</dt>
                  <dd>{{ formatInteger(row.failure_count, locale) }}</dd>
                </div>
                <div>
                  <dt>{{ t('monitor.usage.tokens.uncachedInput') }}</dt>
                  <dd>{{ formatTokens(row.uncached_input_tokens, locale) }}</dd>
                </div>
                <div>
                  <dt>{{ t('monitor.usage.tokens.cacheRead') }}</dt>
                  <dd>{{ formatTokens(row.cache_read_tokens, locale) }}</dd>
                </div>
                <div>
                  <dt>{{ t('monitor.usage.tokens.cacheWrite5m') }}</dt>
                  <dd>{{ formatTokens(row.cache_write_5m_tokens, locale) }}</dd>
                </div>
                <div>
                  <dt>{{ t('monitor.usage.tokens.cacheWrite1h') }}</dt>
                  <dd>{{ formatTokens(row.cache_write_1h_tokens, locale) }}</dd>
                </div>
                <div>
                  <dt>{{ t('monitor.usage.tokens.cacheWriteUnknown') }}</dt>
                  <dd>{{ formatTokens(row.cache_write_unknown_tokens, locale) }}</dd>
                </div>
                <div>
                  <dt>{{ t('monitor.usage.tokens.output') }}</dt>
                  <dd>{{ formatTokens(row.output_tokens, locale) }}</dd>
                </div>
              </dl>
            </details>
          </LedgerRecordList>
        </section>

        <details class="usage-buckets">
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
      :groups-failed="groupsQuery.isError.value"
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
.usage-breakdown {
  display: grid;
  min-width: 0;
}

.usage-tab {
  gap: 22px;
}

.usage-analysis-section,
.usage-breakdown {
  gap: 12px;
}

.usage-trend-panel {
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-surface);
}

.usage-trend-panel :deep(.monitor-section-heading) {
  border-radius: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 9px 14px;
}

.usage-trend-panel__chart {
  padding: 18px 20px 14px;
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

.usage-breakdown-grid {
  --ledger-record-list-grid: minmax(170px, 1.2fr) minmax(120px, 0.85fr) 82px 92px 122px
    minmax(160px, 1.1fr) 34px;
  --ledger-record-list-column-gap: 13px;

  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  padding-inline: 14px;
}

.usage-breakdown-record {
  display: grid;
  min-width: 0;
  grid-column: 1 / -1;
  grid-template-columns: subgrid;
}

.usage-breakdown-record__summary {
  --ledger-record-list-record-min-height: 72px;
  --ledger-record-list-record-padding: 12px 0;

  grid-template-columns: subgrid;
  cursor: pointer;
  list-style: none;
}

.usage-breakdown-record__summary::-webkit-details-marker {
  display: none;
}

.usage-breakdown-record[open] .usage-breakdown-record__summary {
  background: var(--color-surface-sunken);
}

.usage-breakdown-record__identity,
.usage-breakdown-record__cost,
.usage-breakdown-record__quality {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  flex-direction: column;
  gap: 4px;
}

.usage-breakdown-record__identity strong,
.usage-breakdown-record__model code {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.usage-breakdown-record__identity strong {
  font-size: var(--text-sm);
  font-weight: 620;
}

.usage-breakdown-record__identity small,
.usage-breakdown-record__cost small,
.usage-breakdown-record__quality small {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.usage-breakdown-record__identity small,
.usage-breakdown-record__model code,
.usage-breakdown-record__number,
.usage-breakdown-record__cost > span:not(.usage-cell-label) {
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
}

.usage-breakdown-record__model code,
.usage-breakdown-record__number,
.usage-breakdown-record__cost > span:not(.usage-cell-label) {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.usage-breakdown-record__cost small {
  color: var(--color-warning);
}

.usage-breakdown-record__quality {
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  line-height: 1.45;
}

.usage-breakdown-record__action {
  display: grid;
  width: 28px;
  height: 28px;
  place-items: center;
  justify-self: end;
  border-radius: var(--radius-control);
  color: var(--color-text-muted);
  transition:
    transform var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard);
}

.usage-breakdown-record__summary:hover .usage-breakdown-record__action {
  background: var(--color-surface);
}

.usage-breakdown-record[open] .usage-breakdown-record__action {
  transform: rotate(90deg);
}

.usage-cell-label {
  display: none;
  color: var(--color-text-faint);
  font-family: var(--font-sans);
  font-size: var(--text-label-xs);
}

.usage-breakdown-record__detail {
  display: grid;
  grid-column: 1 / -1;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  margin: 0 -14px;
  border-top: 1px solid var(--color-border-subtle);
  background: var(--color-border-subtle);
  gap: 1px;
}

.usage-breakdown-record__detail > div {
  min-width: 0;
  background: var(--color-surface-raised);
  padding: 10px 14px;
}

.usage-breakdown-record__detail dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.usage-breakdown-record__detail dd {
  margin: 4px 0 0;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-variant-numeric: tabular-nums;
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

@media (max-width: 860px) {
  .usage-breakdown-grid {
    padding-inline: 0;
  }

  .usage-breakdown-record {
    display: block;
    grid-column: 1;
  }

  .usage-breakdown-record__summary {
    --ledger-record-list-card-grid: repeat(2, minmax(0, 1fr));

    position: relative;
    padding-right: 52px;
  }

  .usage-breakdown-record__identity {
    grid-column: 1 / -1;
  }

  .usage-breakdown-record__action {
    position: absolute;
    top: 13px;
    right: 13px;
  }

  .usage-cell-label {
    display: block;
  }

  .usage-breakdown-record__detail {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    margin: 0;
    border: 1px solid var(--color-border-subtle);
    border-top: 0;
    border-radius: 0 0 var(--radius-control) var(--radius-control);
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

@media (max-width: 520px) {
  .usage-breakdown-record__summary {
    --ledger-record-list-card-grid: minmax(0, 1fr);
  }

  .usage-breakdown-record__identity {
    grid-column: 1;
  }

  .usage-breakdown-record__detail {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
