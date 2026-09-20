<script setup lang="ts">
import { Eye, ScrollText } from '@lucide/vue'
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import {
  getLogs,
  getLogAccessKeys,
  logsKey,
  type LogFilterName,
  type LogQuery,
} from '@modern/api/logs'
import { getGroupWorkspace, groupQueryKey } from '@modern/api/groups'
import { getGroupChannels } from '@modern/api/group-create'
import { useMessageSource } from '@modern/app/messages'
import { usePageRefresh } from '@modern/app/page-refresh'
import { resolveTimeRange } from '@modern/app/time-range'
import { protocolLabel } from '@modern/i18n/protocols'
import { useURLState } from '@modern/app/url-state'
import { useAuthSession } from '@modern/features/auth/auth-session'
import {
  AppButton,
  AppCollectionState,
  AppFilterSummary,
  AppIconButton,
  AppIcon,
  AppOverflowText,
  AppListFrame,
  AppPagination,
  AppSegmentedControl,
} from '@modern/components/ui'
import type { DateRangePreset } from '@modern/components/ui/date-time'
import { useApiClient } from '@shared/http/client-context'
import LogColumnPicker from './LogColumnPicker.vue'
import LogDetailPanel from './LogDetailPanel.vue'
import LogFilterLink from './LogFilterLink.vue'
import LogFilters from './LogFilters.vue'
import LogTableCell from './LogTableCell.vue'
import { useLogColumns } from './log-columns'
import {
  logFilterOptions,
  logStateKeys,
  nanoToUSD,
  parseLogState,
  serializeLogState,
  type LogRouteState,
} from './log-filters'
import { logTime } from './log-display'

const router = useRouter()
const client = useApiClient()
const cache = useQueryClient()
const session = useAuthSession()
const { t, te, locale } = useI18n()
const admin = computed(() => session.state.principalType === 'admin')
const state = useURLState<LogRouteState>(
  logStateKeys,
  (query) => parseLogState(query, admin.value),
  serializeLogState,
)
const columns = useLogColumns(admin.value)
const frame = ref<InstanceType<typeof AppListFrame>>()
const range = ref(resolveTimeRange({ ...state.value.filters, preset: state.value.preset }))
const displayedFilters = computed(() => ({ ...state.value.filters, ...range.value }))
const query = useQuery(
  computed(() => {
    const filters = { ...state.value.filters }
    const preset = state.value.preset
    const page = state.value.page
    return {
      queryKey: [...logsKey, admin.value, filters, preset ?? null, page],
      queryFn: ({ signal }: { signal: AbortSignal }) => {
        // URL 保存相对预设，实际请求（包括页面重新可见）始终按当前时间解析。
        range.value = resolveTimeRange({ ...filters, preset })
        return getLogs(client, { ...filters, ...range.value }, page, signal)
      },
      placeholderData: keepPreviousData,
    }
  }),
)
const groups = useQuery({
  queryKey: groupQueryKey,
  queryFn: ({ signal }) => getGroupWorkspace(client, signal),
  enabled: admin,
})
const channels = useQuery({
  queryKey: ['modern', 'group-channels'],
  queryFn: ({ signal }) => getGroupChannels(client, signal),
  enabled: admin,
})
const keys = useQuery({
  queryKey: ['modern', 'log-access-key-options'],
  queryFn: ({ signal }) => getLogAccessKeys(client, signal),
  enabled: admin,
})
const groupMap = computed(
  () => groups.data.value && new Map(groups.data.value.items.map((row) => [row.id, row])),
)
const channelMap = computed(
  () => channels.data.value && new Map(channels.data.value.map((row) => [row.id, row])),
)
const rows = computed(() => query.data.value?.items ?? [])
const models = computed(() => [
  ...new Set([
    ...(groups.data.value?.items.flatMap((row) => row.modelNames) ?? []),
    ...rows.value.flatMap((row) => (row.client_model ? [row.client_model] : [])),
  ]),
])
const states = computed(() =>
  ['', 'success', 'error', 'incomplete', 'canceled'].map((value) => ({
    value,
    label: t(value ? 'logs.values.' + value : 'logs.all'),
  })),
)
const requestIdentity = computed(() => JSON.stringify([state.value.filters, state.value.page]))

function applyFilters(input: LogQuery, preset: DateRangePreset | undefined): void {
  const filters = { ...input }
  if (preset) {
    delete filters.from_ms
    delete filters.to_ms
  }
  if (
    JSON.stringify(filters) === JSON.stringify(state.value.filters) &&
    preset === state.value.preset
  )
    return
  state.value = { ...state.value, filters, preset, page: 1 }
  frame.value?.scrollToTop()
}
function submitFilters(filters: LogQuery, preset: DateRangePreset | undefined): void {
  const previous = state.value
  applyFilters(filters, preset)
  if (state.value === previous) void query.refetch()
}
function filterStatus(value: string): void {
  const filters = { ...state.value.filters }
  if (value) filters.status = value
  else delete filters.status
  applyFilters(filters, state.value.preset)
}
function filterFromRow(input: LogQuery): void {
  const filters = { ...state.value.filters, ...input }
  if (input.group_id && input.group_id !== state.value.filters.group_id && !input.credential_id)
    delete filters.credential_id
  applyFilters(filters, state.value.preset)
}
function setMore(value: boolean): void {
  state.value = { ...state.value, more: value }
}
function showDetail(id = ''): void {
  state.value = { ...state.value, detail: id }
}
function changePage(value: number): void {
  if (query.isFetching.value) return
  state.value = { ...state.value, page: value }
  frame.value?.scrollToTop()
}
function pageSize(value: number): void {
  applyFilters({ ...state.value.filters, limit: String(value) }, state.value.preset)
}
function reset(key?: string): void {
  if (!key) {
    applyFilters({ limit: state.value.filters.limit }, '24h')
    return
  }
  if (key === 'time') {
    applyFilters(state.value.filters, '24h')
    return
  }
  const filters = { ...state.value.filters }
  delete filters[key as LogFilterName]
  if (key === 'group_id') delete filters.credential_id
  applyFilters(filters, state.value.preset)
}
function filterValue(key: string, value: string): string {
  if (key === 'group_id')
    return groupMap.value?.get(Number(value))?.name ?? (groupMap.value ? t('logs.deleted') : '—')
  if (key === 'access_key_id') {
    const key = keys.data.value?.find((row) => String(row.id) === value)
    const loggedKey = rows.value.find((row) => String(row.access_key.id) === value)?.access_key
    if (key || (loggedKey && !loggedKey.deleted))
      return key?.name || loggedKey?.name || t('logs.unavailableAccessKey')
    return keys.data.value || loggedKey?.deleted ? t('logs.deleted') : '—'
  }
  if (key === 'credential_id')
    return (
      rows.value.find((row) => String(row.credential_id) === value)?.credential_name ||
      t('logs.selectedCredential')
    )
  if (key === 'channel_id')
    return channelMap.value?.get(value)?.name ?? (channelMap.value ? t('logs.deleted') : '—')
  if (key === 'protocol') return protocolLabel(value, t)
  if (key.startsWith('cost_') && key.endsWith('_nano_usd')) return '$' + nanoToUSD(value)
  if ((key === 'stream' || key === 'cache_present') && (value === 'true' || value === 'false'))
    return t(value === 'true' ? 'logs.yes' : 'logs.no')
  return logFilterOptions[key as LogFilterName] && te('logs.values.' + value)
    ? t('logs.values.' + value)
    : value
}
const summary = computed(() => [
  ...(state.value.preset !== '24h'
    ? [
        {
          key: 'time',
          label: t('logs.timeRange'),
          value: state.value.preset
            ? t('ui.date.ranges.' + state.value.preset)
            : `${logTime(Number(state.value.filters.from_ms), locale.value)} – ${logTime(Number(state.value.filters.to_ms), locale.value)}`,
        },
      ]
    : []),
  ...Object.entries(state.value.filters)
    .filter(([key, value]) => value && !['from_ms', 'to_ms', 'limit'].includes(key))
    .map(([key, value]) => ({
      key,
      label: t('logs.filters.' + key),
      value: filterValue(key, value!),
    })),
])
async function refresh(): Promise<void> {
  await Promise.allSettled([
    query.refetch({ cancelRefetch: false }),
    cache.refetchQueries({ queryKey: ['modern', 'log-detail'], type: 'active' }),
    ...(admin.value ? [groups.refetch(), channels.refetch(), keys.refetch()] : []),
  ])
}
onMounted(() => {
  void router.replace({ query: serializeLogState(state.value) })
})
usePageRefresh({
  refresh,
  pending: () => query.isFetching.value,
  updatedAt: () => query.dataUpdatedAt.value || undefined,
})
useMessageSource(() =>
  query.isError.value && query.data.value
    ? { tone: 'warning', text: t('logs.stale'), action: { label: t('ui.retry'), run: refresh } }
    : undefined,
)
useMessageSource(() =>
  admin.value && (groups.isError.value || channels.isError.value || keys.isError.value)
    ? {
        tone: 'warning',
        text: t('logs.catalogFailed'),
        action: { label: t('ui.retry'), run: refresh },
      }
    : undefined,
)
useMessageSource(() =>
  columns.failed.value ? { tone: 'warning', text: t('logs.columnsNotSaved') } : undefined,
)
</script>

<template>
  <div class="modern-logs-workspace">
    <LogFilters
      :filters="displayedFilters"
      :preset="state.preset"
      :more="state.more"
      :admin="admin"
      :groups="groups.data.value?.items"
      :channels="channels.data.value"
      :access-keys="keys.data.value"
      :models="models"
      :groups-loading="admin && groups.isFetching.value"
      :keys-loading="admin && keys.isFetching.value"
      @change="submitFilters"
      @more="setMore"
    >
      <template #actions
        ><LogColumnPicker
          :columns="columns.available"
          :selected="columns.selected.value"
          @toggle="columns.toggle"
          @reset="columns.reset"
          @all="columns.showAll"
      /></template>
    </LogFilters>
    <div class="modern-log-statusbar">
      <AppSegmentedControl
        :model-value="state.filters.status ?? ''"
        :options="states"
        :label="t('logs.filters.status')"
        @update:model-value="filterStatus"
      />
      <div class="modern-log-filter-summary">
        <AppFilterSummary :items="summary" @remove="reset" @reset="reset()" />
      </div>
    </div>
    <AppListFrame
      ref="frame"
      class="modern-log-table"
      role="table"
      :aria-colcount="columns.cells.value.length + 1"
      :label="t('logs.title')"
      :scroll-key="requestIdentity"
      :loading="query.isFetching.value && Boolean(query.data.value)"
      :style="columns.style.value"
    >
      <template #header>
        <div class="modern-log-row modern-log-table-head" role="row">
          <div
            v-for="cell in columns.cells.value"
            :key="cell.id"
            role="columnheader"
            class="modern-log-column-heading"
            :class="{ 'is-numeric': cell.numeric }"
          >
            <AppOverflowText
              :text="
                t(
                  cell.fields.length > 1
                    ? 'logs.columnGroups.' + cell.fields[0]
                    : 'logs.columns.' + cell.fields[0],
                )
              "
            />
          </div>
          <div class="modern-log-row-action" role="columnheader" :aria-label="t('logs.details')">
            <span class="modern-log-action-heading"
              ><AppIcon :icon="Eye" size="sm" :label="t('logs.details')"
            /></span>
          </div>
        </div>
      </template>
      <AppCollectionState v-if="query.isPending.value" :title="t('collection.loading')" loading />
      <AppCollectionState v-else-if="!query.data.value" :title="t('logs.loadFailed')" error
        ><AppButton @click="query.refetch()">{{ t('ui.retry') }}</AppButton></AppCollectionState
      >
      <AppCollectionState
        v-else-if="!rows.length"
        :title="t('logs.empty')"
        :description="t('logs.emptyHelp')"
        :icon="ScrollText"
        ><AppButton v-if="summary.length" @click="reset()">{{
          t('collection.reset')
        }}</AppButton></AppCollectionState
      >
      <article
        v-for="row in rows"
        v-else
        :key="row.request_id"
        role="row"
        class="modern-log-row modern-log-record"
        :class="{ 'is-selected': state.detail === row.request_id }"
      >
        <div
          v-for="cell in columns.forRow(row)"
          :key="cell.id"
          role="cell"
          :aria-colindex="cell.index + 1"
          :aria-colspan="cell.span > 1 ? cell.span : undefined"
          class="modern-log-cell"
          :class="{ 'is-numeric': cell.numeric }"
          :style="cell.span > 1 ? { gridColumn: 'span ' + cell.span } : undefined"
        >
          <div v-if="cell.error" class="modern-log-error-summary">
            <LogFilterLink
              v-if="row.error_code"
              :label="t('logs.filterByValue', { value: row.error_code })"
              @click="filterFromRow({ error_code: row.error_code })"
            >
              <AppOverflowText class="modern-log-error-code" :text="row.error_code" />
            </LogFilterLink>
            <AppOverflowText v-else class="modern-log-error-code" text="—" />
            <AppOverflowText :text="row.error_summary || '—'" tabindex="0" />
          </div>
          <LogTableCell
            v-else
            :row="row"
            :fields="cell.fields"
            :peer="cell.peer"
            :admin="admin"
            :groups="groupMap"
            :channels="channelMap"
            @open="showDetail(row.request_id)"
            @filter="filterFromRow"
          />
        </div>
        <div
          class="modern-log-row-action"
          role="cell"
          :aria-colindex="columns.cells.value.length + 1"
        >
          <AppIconButton
            class="modern-log-detail-action"
            :icon="Eye"
            :label="t('logs.details')"
            size="xs"
            @click="showDetail(row.request_id)"
          />
        </div>
      </article>
      <template #footer
        ><AppPagination
          mode="total"
          :page="state.page"
          :page-size="Number(state.filters.limit)"
          :total="query.data.value?.pagination.total_items"
          :pending="query.isFetching.value"
          @update:page="changePage"
          @update:page-size="pageSize"
      /></template>
    </AppListFrame>
  </div>
  <LogDetailPanel
    v-if="state.detail"
    :id="state.detail"
    :key="state.detail"
    :admin="admin"
    :groups="groupMap"
    :channels="channelMap"
    :from="range.from_ms"
    :to="range.to_ms"
    :preset="state.preset"
    @close="showDetail()"
  />
</template>

<style scoped>
.modern-logs-workspace {
  --modern-log-columns: minmax(0, 1fr) var(--modern-log-action-width);
  --modern-log-width: 100%;
  --modern-log-action-width: calc(
    var(--modern-control-xs) + var(--modern-space-4) + var(--modern-line-width)
  );
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  min-height: 0;
}
.modern-log-statusbar {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  padding-bottom: var(--modern-space-4);
}
.modern-log-statusbar > :first-child {
  flex-shrink: 0;
}
.modern-log-filter-summary {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  min-width: 0;
  max-width: 100%;
  margin-left: auto;
}
.modern-log-filter-summary :deep(.modern-filter-summary) {
  justify-content: flex-end;
}
.modern-log-table {
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  overflow: hidden;
  background: var(--modern-surface);
}
.modern-log-row {
  display: grid;
  grid-template-columns: var(--modern-log-columns);
  align-items: center;
  column-gap: var(--modern-space-3);
  min-width: var(--modern-log-width);
  padding-left: var(--modern-space-3);
  text-align: left;
}
.modern-log-table-head {
  --modern-log-row-surface: var(--modern-subtle);
  min-height: var(--modern-control-sm);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  background: var(--modern-log-row-surface);
  color: var(--modern-control-placeholder);
  font-size: var(--modern-font-size-caption);
  font-weight: var(--modern-weight-medium);
  letter-spacing: var(--modern-tracking-label);
}
.modern-log-column-heading {
  display: flex;
  align-items: center;
  min-width: 0;
  padding-block: var(--modern-space-2);
}
.modern-log-record {
  --modern-log-row-surface: var(--modern-surface);
  min-height: calc(var(--modern-space-12) + var(--modern-space-1));
  padding-block: var(--modern-space-2);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  background: var(--modern-log-row-surface);
  color: var(--modern-text);
  font-size: var(--modern-font-size-secondary);
  font-variant-numeric: tabular-nums;
}
.modern-log-record:nth-child(even) {
  --modern-log-row-surface: var(--modern-subtle);
}
.modern-log-record:last-child {
  border-bottom: 0;
}
.modern-log-record:hover,
.modern-log-record:focus-within {
  --modern-log-row-surface: var(--modern-control-hover);
}
.modern-log-record.is-selected {
  --modern-log-row-surface: var(--modern-accent-soft);
  box-shadow: inset var(--modern-focus-width) 0 0 var(--modern-accent);
}
.modern-log-cell {
  min-width: 0;
  overflow: hidden;
}
.modern-log-error-summary {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-0-5);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-compact);
}
.modern-log-error-code {
  color: var(--modern-danger);
  font-family: var(--modern-font-mono);
  font-weight: var(--modern-weight-medium);
}
.modern-log-error-summary > :focus-visible {
  outline: var(--modern-focus-width) solid var(--modern-accent);
  outline-offset: var(--modern-focus-offset);
}
/* 数字按位比较，列与表头一起右对齐。 */
.modern-log-cell.is-numeric {
  justify-items: end;
  text-align: right;
}
.modern-log-cell.is-numeric :deep(.modern-log-cell-stack) {
  justify-items: end;
}
.modern-log-column-heading.is-numeric {
  justify-content: flex-end;
}
.modern-log-row-action {
  display: flex;
  align-items: center;
  justify-content: center;
  align-self: stretch;
  position: sticky;
  right: 0;
  z-index: var(--modern-layer-raised);
  padding-inline: var(--modern-space-2);
  border-left: var(--modern-line-width) solid var(--modern-border);
  background: var(--modern-log-row-surface);
}
.modern-log-action-heading {
  display: flex;
  align-items: center;
  justify-content: center;
  width: var(--modern-control-xs);
  min-height: var(--modern-control-xs);
}
.modern-log-row-action :deep(.modern-log-detail-action) {
  color: var(--modern-muted);
}
.modern-log-record:hover .modern-log-row-action :deep(.modern-log-detail-action),
.modern-log-record.is-selected .modern-log-row-action :deep(.modern-log-detail-action) {
  color: var(--modern-accent);
}
.modern-log-table :deep(.modern-pagination) {
  padding-inline: var(--modern-space-3);
}
@media (max-width: 760px) {
  .modern-logs-workspace {
    --modern-log-action-width: calc(
      var(--modern-touch-target) + var(--modern-space-4) + var(--modern-line-width)
    );
  }
  .modern-log-action-heading {
    width: var(--modern-touch-target);
  }
}
</style>
