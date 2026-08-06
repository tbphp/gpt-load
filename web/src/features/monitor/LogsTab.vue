<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { ArrowDown, ArrowRight, ArrowUp, CircleHelp, Info, Layers, Search } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { accessKeyOptionsQueryOptions } from '@/app/resources/access-keys'
import { groupOptionsQueryOptions } from '@/app/resources/groups'
import {
  requestLogQueryOptions,
  type RequestLogFilters,
  type RequestLogItemDto,
  type RequestLogPageSize,
} from '@/app/resources/request-logs'
import { monitorLocation } from '@/app/route-locations'
import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import IconButton from '@/components/ui/IconButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PaginationBar from '@/components/ui/PaginationBar.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatEstimatedCost } from '@/lib/format'

import {
  applyLogFilterDraft,
  createLogFilterDraft,
  defaultRequestLogFilters,
  parseAppliedLogFilters,
  serializeAppliedLogFilters,
  validateLogFilterDraft,
  type LogFilterDraft,
  type LogFilterErrors,
} from './log-filters'
import { formatCacheHitRate } from '@/lib/cache-rate'
import {
  formatLogDuration,
  formatLogOutputRate,
  formatLogTokenCount,
  hasRequestLogCache,
  requestLogCostDisplayState,
  requestLogUsageDisplayState,
} from './log-format'
import LogDetailDrawer from './LogDetailDrawer.vue'
import LogsFilterForm from './LogsFilterForm.vue'
import { parseSelectedRequestID } from './monitor-route'

const client = useApiClient()
const route = useRoute()
const router = useRouter()
const { locale, t } = useI18n()
const logPageSizes = [20, 50, 100] as const
const appliedFilters = computed(() => parseAppliedLogFilters(route.query))
const selectedRequestID = computed(() => parseSelectedRequestID(route.query))
const draft = ref(createLogFilterDraft(appliedFilters.value))
const filterErrors = ref<LogFilterErrors>({})
const cursorHistory = ref<Array<string | undefined>>([undefined])
const pageIndex = ref(0)
const paginationPending = ref(false)
const pageTransitionOrigin = ref<number | null>(null)
const currentCursor = computed(() => cursorHistory.value[pageIndex.value])
let detailFocusTimer: number | undefined

const groupsQuery = useQuery(groupOptionsQueryOptions(client))
const accessKeyOptionsQuery = useQuery(accessKeyOptionsQueryOptions(client))
const logsQuery = useQuery(requestLogQueryOptions(client, appliedFilters, currentCursor))
const logs = computed(() => logsQuery.data.value?.items ?? [])
const currentPage = computed(() => pageIndex.value + 1)
const paginationBusy = computed(() => paginationPending.value || logsQuery.isFetching.value)
const filterSignature = computed(() =>
  JSON.stringify(serializeAppliedLogFilters(appliedFilters.value)),
)
const advancedFilterKeys: readonly (keyof RequestLogFilters)[] = [
  'upstream_model',
  'access_key_id',
  'request_id',
  'protocol',
  'stream',
  'final_status_code',
  'usage_state',
  'cost_state',
  'pricing_completeness',
  'cache_present',
  'upstream_key_id',
  'attempt_status_code',
  'failure_category',
  'error_code',
  'retry_state',
  'retry_count_min',
  'retry_count_max',
  'first_response_min_ms',
  'first_response_max_ms',
  'duration_min_ms',
  'duration_max_ms',
  'input_tokens_min',
  'input_tokens_max',
  'output_tokens_min',
  'output_tokens_max',
  'cost_min_nano_usd',
  'cost_max_nano_usd',
]
const advancedCount = computed(
  () => advancedFilterKeys.filter((key) => appliedFilters.value[key] !== undefined).length,
)
const hasNonTimeFilters = computed(() =>
  Object.keys(appliedFilters.value).some(
    (key) => key !== 'from_ms' && key !== 'to_ms' && key !== 'limit',
  ),
)
const appliedChips = computed(() => {
  const filters = appliedFilters.value
  const values: Array<{ key: string; label: string }> = []
  if (filters.from_ms !== undefined && filters.to_ms !== undefined) {
    values.push({
      key: 'time',
      label: `${formatDateFilter(filters.from_ms)} → ${formatDateFilter(filters.to_ms)}`,
    })
  }
  if (filters.group_id !== undefined) {
    const group = groupsQuery.data.value?.find(({ id }) => id === filters.group_id)
    values.push({
      key: 'group_id',
      label: t('monitor.logs.filters.appliedGroup', {
        value: group?.name ?? `#${filters.group_id}`,
      }),
    })
  }
  if (filters.status !== undefined) {
    values.push({
      key: 'status',
      label: t('monitor.logs.filters.appliedStatus', {
        value: t(`monitor.logs.status.${filters.status}`),
      }),
    })
  }
  if (filters.client_model !== undefined) {
    values.push({
      key: 'client_model',
      label: advancedChipLabel('client_model', filters.client_model),
    })
  }
  for (const key of advancedFilterKeys) {
    const value = filters[key]
    if (value === undefined) continue
    values.push({ key, label: advancedChipLabel(key, value) })
  }
  return values
})

watch(filterSignature, () => {
  draft.value = createLogFilterDraft(appliedFilters.value)
  filterErrors.value = {}
  cursorHistory.value = [undefined]
  pageIndex.value = 0
  paginationPending.value = false
  pageTransitionOrigin.value = null
})

watch(
  () => logsQuery.dataUpdatedAt.value,
  (updatedAt, previousUpdatedAt) => {
    if (updatedAt <= 0 || updatedAt === previousUpdatedAt) return
    paginationPending.value = false
    pageTransitionOrigin.value = null
  },
)

watch(
  () => logsQuery.isError.value,
  (failed) => {
    if (!failed || pageTransitionOrigin.value === null) return
    const origin = pageTransitionOrigin.value
    if (pageIndex.value > origin) cursorHistory.value = cursorHistory.value.slice(0, origin + 1)
    pageIndex.value = origin
    paginationPending.value = false
    pageTransitionOrigin.value = null
  },
)

function formatDateFilter(value: number): string {
  const date = new Date(value)
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(
    date.getMinutes(),
  )}:${pad(date.getSeconds())}`
}

function advancedChipLabel(key: keyof RequestLogFilters, value: unknown): string {
  if (key === 'access_key_id') {
    const accessKey = accessKeyOptionsQuery.data.value?.find(({ id }) => id === value)
    return t('monitor.logs.filters.appliedAccessKey', {
      value: accessKey?.name ?? `#${value}`,
    })
  }
  if (key === 'client_model') return t('monitor.logs.filters.appliedClientModel', { value })
  if (key === 'upstream_model') return t('monitor.logs.filters.appliedUpstreamModel', { value })
  if (key === 'request_id') return t('monitor.logs.filters.appliedRequestId', { value })
  if (key === 'protocol') return String(value)
  if (key === 'failure_category') return t(`monitor.logs.failureCategory.${String(value)}`)
  if (key === 'retry_state') return t(`monitor.logs.filters.retryState.${String(value)}`)
  if (key === 'usage_state') {
    return t('monitor.logs.filters.appliedUsageState', {
      value: t(`monitor.logs.filters.usageState.${String(value)}`),
    })
  }
  if (key === 'cost_state') {
    return t('monitor.logs.filters.appliedCostState', {
      value: t(`monitor.logs.filters.costState.${String(value)}`),
    })
  }
  if (key === 'pricing_completeness') {
    return t('monitor.logs.filters.appliedCompleteness', {
      value: t(`monitor.logs.filters.completeness.${String(value)}`),
    })
  }
  const labelKeys: Partial<Record<keyof RequestLogFilters, string>> = {
    stream: 'stream',
    final_status_code: 'finalStatusCode',
    usage_state: 'usageStateLabel',
    cost_state: 'costStateLabel',
    pricing_completeness: 'completenessLabel',
    cache_present: 'cachePresent',
    upstream_key_id: 'upstreamKey',
    attempt_status_code: 'attemptStatusCode',
    error_code: 'errorCode',
  }
  const rangeKey = key.replace(/_nano_usd$/u, '_usd')
  const label = labelKeys[key]
    ? t(`monitor.logs.filters.${labelKeys[key]}`)
    : t(`monitor.logs.filters.rangeFields.${rangeKey}`)
  const display =
    typeof value === 'boolean' ? t(value ? 'monitor.logs.yes' : 'monitor.logs.no') : value
  return `${label} ${String(display)}`
}

function updateDraftField(field: keyof LogFilterDraft, value: string): void {
  draft.value = { ...draft.value, [field]: value }
}

async function commitFilters(filters: RequestLogFilters): Promise<void> {
  const serialized = serializeAppliedLogFilters(filters)
  const nextSignature = JSON.stringify(serialized)
  draft.value = createLogFilterDraft(filters)
  filterErrors.value = {}

  if (nextSignature === filterSignature.value) {
    await logsQuery.refetch()
    return
  }

  await router.push(monitorLocation(serialized))
}

async function applyFilters(): Promise<void> {
  const errors = validateLogFilterDraft(draft.value)
  filterErrors.value = errors
  if (Object.keys(errors).length > 0) return

  await commitFilters({
    ...applyLogFilterDraft(draft.value),
    limit: appliedFilters.value.limit ?? 20,
  })
}

async function resetFilters(): Promise<void> {
  await commitFilters({
    ...defaultRequestLogFilters(),
    limit: appliedFilters.value.limit ?? 20,
  })
}

function setPageSize(pageSize: RequestLogPageSize): void {
  if (paginationBusy.value) return
  void commitFilters({ ...appliedFilters.value, limit: pageSize })
}

async function removeFilter(key: string): Promise<void> {
  if (key === 'time') return
  const filters = { ...appliedFilters.value }
  delete filters[key as keyof RequestLogFilters]
  await commitFilters(filters)
}

function nextPage(): void {
  if (paginationBusy.value) return
  const cursor = logsQuery.data.value?.next_cursor
  if (!cursor || cursor === currentCursor.value) return
  if (cursorHistory.value.slice(0, pageIndex.value + 1).includes(cursor)) return
  pageTransitionOrigin.value = pageIndex.value
  paginationPending.value = true
  cursorHistory.value = [...cursorHistory.value.slice(0, pageIndex.value + 1), cursor]
  pageIndex.value += 1
}

function previousPage(): void {
  if (paginationBusy.value || pageIndex.value <= 0) return
  pageTransitionOrigin.value = pageIndex.value
  paginationPending.value = true
  pageIndex.value -= 1
}

async function setDetailOpen(requestID: string | undefined, open: boolean): Promise<void> {
  const closingID = selectedRequestID.value
  const query = serializeAppliedLogFilters(appliedFilters.value)
  if (open && requestID) query.selected_request_id = requestID
  await router.push(monitorLocation(query))
  if (open || !closingID) return
  window.clearTimeout(detailFocusTimer)
  detailFocusTimer = window.setTimeout(() => {
    document.getElementById(`log-details-${closingID}`)?.focus()
  }, 30)
}

function accessKeyLabel(log: RequestLogItemDto): string {
  if (log.access_key.deleted) return t('monitor.logs.accessKey.deleted', { id: log.access_key.id })
  return log.access_key.name ?? `#${log.access_key.id}`
}

function groupLabel(log: RequestLogItemDto): string {
  if (log.group_id === null) return '—'
  const group = groupsQuery.data.value?.find(({ id }) => id === log.group_id)
  return group?.name ?? `Group #${log.group_id}`
}

function responseLabel(log: RequestLogItemDto): string {
  if (log.status === 'success') return t('monitor.logs.response.normal')
  if (log.status === 'error') {
    return log.stream && log.status_code === 200
      ? t('monitor.logs.response.streamError')
      : t('monitor.logs.response.errorWithCode', { code: log.status_code })
  }
  return t(`monitor.logs.status.${log.status}`)
}

function statusTone(
  status: RequestLogItemDto['status'],
): 'success' | 'danger' | 'warning' | 'neutral' {
  if (status === 'success') return 'success'
  if (status === 'error') return 'danger'
  if (status === 'incomplete') return 'warning'
  return 'neutral'
}

function responseTooltip(log: RequestLogItemDto): string {
  return [log.error_code, log.error_summary].filter(Boolean).join(' · ')
}

function modelMappingTooltip(log: RequestLogItemDto): string {
  return t('monitor.logs.modelMapping', {
    client: log.client_model ?? '—',
    upstream: log.upstream_model ?? '—',
  })
}

function cacheTooltip(log: RequestLogItemDto): string {
  const details = [
    [t('monitor.logs.tokens.cacheRead'), log.cache_read_tokens],
    [t('monitor.logs.tokens.cacheWrite5m'), log.cache_write_5m_tokens],
    [t('monitor.logs.tokens.cacheWrite1h'), log.cache_write_1h_tokens],
    [t('monitor.logs.tokens.cacheWrite'), log.cache_write_unknown_tokens],
  ]
    .filter(([, value]) => value !== '0')
    .map(([label, value]) => `${label} ${formatLogTokenCount(value, locale.value)}`)
  details.push(
    `${t('monitor.logs.tokens.cacheHitRate')} ${formatCacheHitRate(
      log.cache_read_tokens,
      log.input_tokens,
      locale.value,
    )}`,
  )
  return details.join('\n')
}

function timingPrimary(log: RequestLogItemDto): string {
  if (!log.stream || log.first_response_ms === null) return formatLogDuration(log.duration_ms)
  return `${formatLogDuration(log.first_response_ms)} / ${formatLogDuration(log.duration_ms)}`
}

function costLabel(log: RequestLogItemDto): string {
  const state = requestLogCostDisplayState(log)
  if (state === 'complete' || state === 'partial') {
    return formatEstimatedCost(log.estimated_cost_nano_usd, locale.value)
  }
  return '—'
}
</script>

<template>
  <div class="logs-tab">
    <LogsFilterForm
      :draft="draft"
      :errors="filterErrors"
      :groups="groupsQuery.data.value ?? []"
      :access-keys="accessKeyOptionsQuery.data.value ?? []"
      :groups-failed="groupsQuery.isError.value"
      :access-keys-failed="accessKeyOptionsQuery.isError.value"
      :applied-chips="appliedChips"
      :advanced-count="advancedCount"
      @update-field="updateDraftField"
      @remove-filter="removeFilter"
      @apply="applyFilters"
      @reset="resetFilters"
    />

    <InlineFeedback
      v-if="groupsQuery.isError.value || accessKeyOptionsQuery.isError.value"
      tone="warning"
    >
      {{ t('monitor.logs.options.partialFailed') }}
    </InlineFeedback>
    <QueryFeedback
      v-if="logsQuery.isPending.value"
      state="loading"
      :message="t('monitor.logs.loading')"
    />
    <QueryFeedback
      v-else-if="logsQuery.isError.value && !logsQuery.data.value"
      state="error"
      :message="t('monitor.logs.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="logsQuery.refetch()"
    />
    <template v-else>
      <QueryFeedback
        v-if="logsQuery.isError.value"
        state="stale"
        :message="t('monitor.logs.stale')"
        :retry-label="t('common.retry')"
        @retry="logsQuery.refetch()"
      />
      <LedgerRecordList
        v-if="logs.length"
        grid-class="logs-list"
        :label="t('monitor.logs.caption')"
        :row-count="logs.length + 1"
        :scroll-hint="t('monitor.scrollHint')"
      >
        <template #header>
          <span role="columnheader">{{ t('monitor.logs.columns.time') }}</span>
          <span role="columnheader">{{ t('monitor.logs.columns.route') }}</span>
          <span role="columnheader">{{ t('monitor.logs.columns.modelProtocol') }}</span>
          <span role="columnheader">{{ t('monitor.logs.columns.response') }}</span>
          <span role="columnheader">{{ t('monitor.logs.columns.cost') }}</span>
          <span role="columnheader">{{ t('monitor.logs.columns.tokens') }}</span>
          <span role="columnheader">{{ t('monitor.logs.columns.timing') }}</span>
          <span role="columnheader">{{ t('monitor.logs.columns.actions') }}</span>
        </template>

        <article
          v-for="(log, index) in logs"
          :key="log.request_id"
          class="ledger-record-list__record logs-list__record"
          role="row"
          :aria-rowindex="index + 2"
        >
          <div
            class="ledger-record-list__cell logs-list__cell logs-list__time"
            role="cell"
            :data-label="t('monitor.logs.columns.time')"
          >
            <AppDateTime :instant="log.completed_at_ms" :locale="locale" precision="second" />
          </div>
          <div
            class="ledger-record-list__cell logs-list__cell"
            role="cell"
            :data-label="t('monitor.logs.columns.route')"
          >
            <span>{{ accessKeyLabel(log) }}</span>
            <small>{{ groupLabel(log) }}</small>
          </div>
          <div
            class="ledger-record-list__cell logs-list__cell"
            role="cell"
            :data-label="t('monitor.logs.columns.modelProtocol')"
          >
            <span class="logs-list__inline">
              <code>{{ log.client_model ?? '—' }}</code>
              <AppTooltip
                v-if="log.upstream_model && log.upstream_model !== log.client_model"
                :content="modelMappingTooltip(log)"
              >
                <button
                  type="button"
                  class="logs-list__hint"
                  :aria-label="t('monitor.logs.modelMappingLabel')"
                >
                  <Info :size="13" aria-hidden="true" />
                </button>
              </AppTooltip>
            </span>
            <small>{{ log.protocol }}</small>
          </div>
          <div
            class="ledger-record-list__cell logs-list__cell"
            role="cell"
            :data-label="t('monitor.logs.columns.response')"
          >
            <AppTooltip v-if="responseTooltip(log)" :content="responseTooltip(log)">
              <span
                ><StatusBadge :tone="statusTone(log.status)" size="compact">{{
                  responseLabel(log)
                }}</StatusBadge></span
              >
            </AppTooltip>
            <StatusBadge v-else :tone="statusTone(log.status)" size="compact">{{
              responseLabel(log)
            }}</StatusBadge>
            <small v-if="log.attempt_count > 1">
              {{ t('monitor.logs.attemptCount', { count: log.attempt_count }) }}
            </small>
          </div>
          <div
            class="ledger-record-list__cell logs-list__cell"
            role="cell"
            :data-label="t('monitor.logs.columns.cost')"
          >
            <span
              :class="{
                'logs-list__state--warning': requestLogCostDisplayState(log) !== 'complete',
              }"
              >{{ costLabel(log) }}</span
            >
          </div>
          <div
            class="ledger-record-list__cell logs-list__cell"
            role="cell"
            :data-label="t('monitor.logs.columns.tokens')"
          >
            <span v-if="requestLogUsageDisplayState(log) === 'reported'" class="logs-list__tokens">
              <ArrowDown :size="12" aria-hidden="true" />{{
                formatLogTokenCount(log.input_tokens, locale)
              }}
              <span>/</span>
              <ArrowUp :size="12" aria-hidden="true" />{{
                formatLogTokenCount(log.output_tokens, locale)
              }}
              <AppTooltip v-if="hasRequestLogCache(log)" :content="cacheTooltip(log)">
                <button
                  type="button"
                  class="logs-list__hint"
                  :aria-label="t('monitor.logs.tokens.cacheDetails')"
                >
                  <Layers :size="13" aria-hidden="true" />
                </button>
              </AppTooltip>
              <AppTooltip
                v-if="log.usage_state === 'partial'"
                :content="t('monitor.logs.tokens.partial')"
              >
                <button
                  type="button"
                  class="logs-list__hint"
                  :aria-label="t('monitor.logs.tokens.partial')"
                >
                  <CircleHelp :size="13" aria-hidden="true" />
                </button>
              </AppTooltip>
            </span>
            <span v-else class="logs-list__state--warning">—</span>
          </div>
          <div
            class="ledger-record-list__cell logs-list__cell"
            role="cell"
            :data-label="t('monitor.logs.columns.timing')"
          >
            <span>{{ timingPrimary(log) }}</span>
            <small v-if="formatLogOutputRate(log, locale) !== '—'">{{
              formatLogOutputRate(log, locale)
            }}</small>
          </div>
          <div
            class="ledger-record-list__cell logs-list__action"
            role="cell"
            :data-label="t('monitor.logs.columns.actions')"
          >
            <IconButton
              :id="`log-details-${log.request_id}`"
              variant="ghost"
              size="compact"
              :label="t('monitor.logs.details')"
              @click="setDetailOpen(log.request_id, true)"
            >
              <ArrowRight :size="16" aria-hidden="true" />
            </IconButton>
          </div>
        </article>
      </LedgerRecordList>
      <EmptyState
        v-else
        variant="ledger"
        :title="
          t(hasNonTimeFilters ? 'monitor.logs.empty.filteredTitle' : 'monitor.logs.empty.title')
        "
        :description="
          t(
            hasNonTimeFilters
              ? 'monitor.logs.empty.filteredDescription'
              : 'monitor.logs.empty.description',
          )
        "
      >
        <template #icon><Search :size="20" /></template>
      </EmptyState>
      <PaginationBar
        cursor
        :page="currentPage"
        :page-size="appliedFilters.limit ?? 20"
        :page-sizes="logPageSizes"
        show-page-size
        appearance="detail"
        :has-previous="pageIndex > 0"
        :has-next="Boolean(logsQuery.data.value?.next_cursor)"
        :pending="paginationBusy"
        @previous="previousPage"
        @next="nextPage"
        @update:page-size="setPageSize"
      />
    </template>

    <LogDetailDrawer
      :open="Boolean(selectedRequestID)"
      :request-id="selectedRequestID"
      @update:open="setDetailOpen(undefined, $event)"
    />
  </div>
</template>

<style scoped>
.logs-tab {
  display: grid;
  min-width: 0;
  gap: 14px;
}

.logs-list {
  --ledger-record-list-grid: 148px minmax(132px, 0.95fr) minmax(148px, 1.05fr) 112px
    minmax(88px, 0.58fr) minmax(142px, 0.9fr) 126px 34px;
  --ledger-record-list-column-gap: 16px;
  --ledger-record-list-record-min-height: 72px;
  --ledger-record-list-record-padding: 10px 0;
}

.logs-list__cell {
  display: grid;
  min-width: 0;
  gap: 4px;
  color: var(--color-text);
  font-size: var(--text-sm);
  font-weight: 400;
}

.logs-list__cell > span,
.logs-list__cell code {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.logs-list__cell small {
  overflow: hidden;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 400;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.logs-list__time {
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
}

.logs-list__inline,
.logs-list__tokens {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 5px;
}

.logs-list__tokens {
  font-family: var(--font-mono);
  white-space: nowrap;
}

.logs-list__hint {
  display: inline-flex;
  width: 20px;
  height: 20px;
  flex: 0 0 20px;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: var(--radius-tag);
  background: transparent;
  color: var(--color-text-faint);
  padding: 0;
  cursor: help;
}

.logs-list__hint:hover {
  background: var(--color-surface-sunken);
  color: var(--color-text);
}

.logs-list__state--warning {
  color: var(--color-warning);
}

.logs-list__action {
  justify-self: end;
}

.logs-tab :deep(.status-badge) {
  width: max-content;
  font-weight: 400;
}

@media (max-width: 1080px) {
  .logs-list {
    --ledger-record-list-column-gap: 10px;
    --ledger-record-list-grid: 136px minmax(118px, 0.9fr) minmax(130px, 1fr) 104px
      minmax(84px, 0.58fr) minmax(124px, 0.85fr) 116px 32px;
  }
}

@media (max-width: 860px) {
  .logs-list {
    --ledger-record-list-card-grid: minmax(104px, 0.42fr) minmax(0, 1.58fr);
  }

  .logs-list__record {
    gap: 10px 14px;
  }

  .logs-list__cell,
  .logs-list__action {
    display: grid;
    grid-column: 1 / -1;
    grid-template-columns: subgrid;
    align-items: start;
  }

  .logs-list__cell::before,
  .logs-list__action::before {
    content: attr(data-label);
    color: var(--color-text-faint);
    font-size: var(--text-label-xs);
  }

  .logs-list__cell > *,
  .logs-list__action > * {
    grid-column: 2;
  }

  .logs-list__action {
    justify-self: stretch;
  }

  .logs-list__action :deep(.icon-button) {
    justify-self: end;
  }
}
</style>
