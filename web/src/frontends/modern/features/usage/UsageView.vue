<script setup lang="ts">
import { useMobileLayout } from '@modern/app/use-mobile-layout'
import { SlidersHorizontal } from '@lucide/vue'
import { keepPreviousData, useQuery } from '@tanstack/vue-query'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  getUsage,
  usageFilterKeys,
  usageMetrics,
  usageDimensions,
  type UsageFilterKey,
  type UsageItem,
  type UsageDimension,
} from '@modern/api/usage'
import { getGroupWorkspace, groupQueryKey } from '@modern/api/groups'
import { getGroupChannels } from '@modern/api/group-create'
import { getLogAccessKeys } from '@modern/api/logs'
import { getGroupCredentials } from '@modern/api/group-detail'
import { useAuthSession } from '@modern/features/auth/auth-session'
import { useApiClient } from '@shared/http/client-context'
import { usePageRefresh } from '@modern/app/page-refresh'
import { useMessageSource } from '@modern/app/messages'
import { useURLState } from '@modern/app/url-state'
import { resolveTimeRange, timeRangeQuery } from '@modern/app/time-range'
import { channelSearchOption } from '@modern/components/channel-options'
import {
  AppButton,
  AppFilterSheet,
  AppIconButton,
  AppSelect,
  AppBadge,
  AppChannelIcon,
  AppCollectionState,
  AppDateTimeRangePicker,
  AppFilterSummary,
  AppModelSelect,
  AppPanel,
  AppSearchSelect,
  AppSegmentedControl,
  AppTooltip,
} from '@modern/components/ui'
import type { SearchSelectOption } from '@modern/components/ui'
import {
  formatLocalDateTime,
  parseLocalDateTime,
  type DateRangePreset,
} from '@modern/components/ui/date-time'
import { dateFormatter } from '@modern/components/ui/intl-formatters'
import { formatCompactNumber } from '@modern/components/ui/format'
import {
  parseUsageState,
  serializeUsageState,
  usageStateKeys,
  trendMetrics,
  validUsageFilter,
} from './usage-state'
import UsageTrend from './UsageTrend.vue'
import UsageRank from './UsageRank.vue'
import UsageComposition from './UsageComposition.vue'
import UsageMetrics from './UsageMetrics.vue'

const { t, locale } = useI18n()
const router = useRouter()
const isMobile = useMobileLayout()
const mobileFilters = ref(false)
const mobileSection = ref('trend')
const mobileDimension = ref<UsageDimension>('model')
const client = useApiClient()
const session = useAuthSession()
const admin = computed(() => session.state.principalType === 'admin')
const state = useURLState(
  usageStateKeys,
  (query) => parseUsageState(query, admin.value),
  serializeUsageState,
)
const query = useQuery(
  computed(() => {
    const filters = { ...state.value.filters },
      range = { ...state.value.range }
    return {
      queryKey: ['modern', 'usage', admin.value, filters, range],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        getUsage(client, { ...filters, ...resolveTimeRange(range) }, signal),
      placeholderData: keepPreviousData,
    }
  }),
)
const report = computed(() => query.data.value)
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
  () => new Map(groups.data.value?.items.map((row) => [String(row.id), row])),
)
const channelMap = computed(() => new Map(channels.data.value?.map((row) => [row.id, row])))
const credentialLabels = ref(new Map<string, string>())
function label(key: UsageFilterKey, value: string): string {
  if (key === 'group_id')
    return groupMap.value.get(value)?.name ?? (groups.data.value ? t('logs.deleted') : '—')
  if (key === 'channel_id')
    return channelMap.value.get(value)?.name ?? (channels.data.value ? t('logs.deleted') : '—')
  if (key === 'access_key_id')
    return (
      keys.data.value?.find((row) => String(row.id) === value)?.name ??
      (keys.data.value ? t('logs.deleted') : '—')
    )
  if (key === 'credential_id')
    return credentialLabels.value.get(value) ?? t('logs.selectedCredential')
  return value
}
function options(key: UsageFilterKey, values: SearchSelectOption[], all: string) {
  const selected = state.value.filters[key]
  return [
    { value: '', label: all },
    ...values,
    ...(selected && !values.some((row) => row.value === selected)
      ? [{ value: selected, label: label(key, selected) }]
      : []),
  ]
}
const groupOptions = computed(() =>
  options(
    'group_id',
    (groups.data.value?.items ?? []).map((row) => ({
      value: String(row.id),
      label: row.name,
      keywords: [row.channelID, row.channelName],
    })),
    t('logs.allGroups'),
  ),
)
const channelOptions = computed(() =>
  options(
    'channel_id',
    (channels.data.value ?? []).map(channelSearchOption),
    t('logs.allChannels'),
  ),
)
const keyOptions = computed(() =>
  options(
    'access_key_id',
    (keys.data.value ?? []).map((row) => ({
      value: String(row.id),
      label: row.name,
      keywords: [row.suffix],
    })),
    t('logs.allAccessKeys'),
  ),
)
const models = computed(() => [
  ...new Set(
    usageMetrics.flatMap(
      (metric) =>
        report.value?.distributions.model?.[metric].items
          .map((row) => row.model)
          .filter((model): model is string => Boolean(model)) ?? [],
    ),
  ),
])
const loadCredentials = computed(() => {
  const group = Number(state.value.filters.group_id)
  return async (q: string, signal: AbortSignal) => {
    if (!group) return [{ value: '', label: t('logs.all') }]
    const page = await getGroupCredentials(
      client,
      group,
      { q, page: 1, pageSize: 100, sort: 'name', status: '', proxy: '', reset: '' },
      signal,
    )
    const values = page.items.map((row) => ({
      value: String(row.id),
      label: row.label,
    }))
    credentialLabels.value = new Map([
      ...credentialLabels.value,
      ...values.map((row): [string, string] => [row.value, row.label]),
    ])
    return [{ value: '', label: t('logs.all') }, ...values]
  }
})
const from = ref(''),
  to = ref(''),
  preset = ref<DateRangePreset>(),
  model = ref('')
let modelTimer: ReturnType<typeof setTimeout> | undefined
watch(
  () => state.value.filters.upstream_model,
  (value) => {
    clearTimeout(modelTimer)
    model.value = value ?? ''
  },
  { immediate: true },
)
watch(
  () => [state.value.range, report.value?.from_ms, report.value?.to_ms],
  () => {
    const range = resolveTimeRange(state.value.range)
    from.value = formatLocalDateTime(Number(range.from_ms))
    to.value = formatLocalDateTime(Number(range.to_ms))
    preset.value = state.value.range.preset
  },
  { immediate: true },
)
onScopeDispose(() => clearTimeout(modelTimer))
const modelError = computed(() =>
  model.value && !validUsageFilter('upstream_model', model.value.trim())
    ? t('logs.errors.invalidText')
    : undefined,
)
function setFilter(key: UsageFilterKey, value: string): void {
  const filters = { ...state.value.filters }
  if (value) filters[key] = value
  else delete filters[key]
  if (key === 'group_id') delete filters.credential_id
  state.value = { ...state.value, filters }
}
function setModel(value: string): void {
  model.value = value
  clearTimeout(modelTimer)
  if (!modelError.value)
    modelTimer = setTimeout(() => setFilter('upstream_model', value.trim()), 200)
}
function applyDate(): void {
  const start = parseLocalDateTime(from.value),
    end = parseLocalDateTime(to.value)
  if (!start || !end || start >= end) return
  const range = preset.value
    ? { preset: preset.value }
    : { from_ms: String(start.getTime()), to_ms: String(end.getTime()) }
  if (JSON.stringify(range) === JSON.stringify(state.value.range)) {
    // 再次点击当前快捷日期，也要按当前时间重新查询。
    void query.refetch()
  } else {
    state.value = { ...state.value, range }
  }
}
function reset(key?: string): void {
  if (!key) {
    state.value = { ...state.value, filters: {}, range: { preset: '24h' } }
    return
  }
  if (key === 'time') state.value = { ...state.value, range: { preset: '24h' } }
  else setFilter(key as UsageFilterKey, '')
}
const filterSummary = computed(() => [
  ...(state.value.range.preset !== '24h'
    ? [
        {
          key: 'time',
          label: t('logs.timeRange'),
          value: state.value.range.preset
            ? t('ui.date.ranges.' + state.value.range.preset)
            : `${from.value} – ${to.value}`,
        },
      ]
    : []),
  ...usageFilterKeys.flatMap((key) =>
    state.value.filters[key]
      ? [{ key, label: t('usage.filters.' + key), value: label(key, state.value.filters[key]!) }]
      : [],
  ),
])
// 自动刷新只更新报告，保留筛选、图表选项和滚动位置，不刷新筛选候选项。
const requestIdentity = computed(() => JSON.stringify([state.value.filters, state.value.range]))
const silentRequest = ref<string>()
const fetching = computed(
  () => query.isFetching.value && silentRequest.value !== requestIdentity.value,
)
async function autoRefresh(): Promise<boolean> {
  const identity = requestIdentity.value
  silentRequest.value = identity
  try {
    return !(await query.refetch({ cancelRefetch: false })).isError
  } finally {
    if (silentRequest.value === identity) silentRequest.value = undefined
  }
}
async function refresh(): Promise<void> {
  await Promise.all([
    query.refetch(),
    ...(admin.value ? [groups.refetch(), channels.refetch(), keys.refetch()] : []),
  ])
}
usePageRefresh({
  refresh,
  pending: () => fetching.value,
  updatedAt: () => report.value?.observed_at_ms,
  autoRefresh,
})
useMessageSource(() =>
  query.isError.value && report.value
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
const compact = (value: number) => formatCompactNumber(value, locale.value)
const trendOptions = computed(() =>
  trendMetrics.map((value) => ({ value, label: t('usage.' + value) })),
)
const rankOptions = computed(() =>
  usageMetrics.map((value) => ({ value, label: t('usage.' + value) })),
)
const dimensions = computed(() =>
  usageDimensions.filter(
    (dimension) => report.value?.distributions[dimension] && (admin.value || dimension === 'model'),
  ),
)
function itemFilter(dimension: UsageDimension, item: UsageItem) {
  return dimension === 'model'
    ? { upstream_model: item.model }
    : dimension === 'group'
      ? { group_id: String(item.group_id), credential_id: undefined }
      : { access_key_id: String(item.access_key_id) }
}
function drill(dimension: UsageDimension, item: UsageItem): void {
  state.value = {
    ...state.value,
    filters: Object.fromEntries(
      Object.entries({ ...state.value.filters, ...itemFilter(dimension, item) }).filter(
        ([, value]) => value,
      ),
    ),
  }
}
function logs(dimension: UsageDimension, item: UsageItem): void {
  void router.push({
    name: 'modern-logs',
    query: {
      ...state.value.filters,
      ...timeRangeQuery(state.value.range),
      ...itemFilter(dimension, item),
    },
  })
}
const period = computed(() => {
  if (!report.value) return ''
  const format = dateFormatter(locale.value, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  })
  return `${format.format(report.value.from_ms)} – ${format.format(report.value.to_ms)}`
})
const bucketLabel = computed(() => {
  const minutes = (report.value?.bucket_width_ms ?? 0) / 60000
  return minutes >= 1440
    ? t('usage.bucketDays', { count: minutes / 1440 })
    : minutes >= 60
      ? t('usage.bucketHours', { count: minutes / 60 })
      : t('usage.bucket', { minutes })
})
</script>

<template>
  <div class="modern-usage-workspace">
    <form
      class="modern-usage-filters"
      role="search"
      :aria-label="t('usage.search')"
      @submit.prevent
    >
      <AppDateTimeRangePicker
        v-model:from="from"
        v-model:to="to"
        :preset="preset"
        :label="t('logs.timeRange')"
        size="md"
        label-hidden
        class="modern-usage-date"
        @update:preset="preset = $event"
        @apply="applyDate"
      />
      <AppIconButton
        v-if="isMobile"
        :icon="SlidersHorizontal"
        :label="t('ui.mobile.filters')"
        @click="mobileFilters = true"
      />
      <AppFilterSheet
        v-model:open="mobileFilters"
        :mobile="isMobile"
        :title="t('ui.mobile.filters')"
      >
        <AppSearchSelect
          v-if="admin"
          :model-value="state.filters.group_id ?? ''"
          :options="groupOptions"
          :label="t('usage.filters.group_id')"
          :label-hidden="!isMobile"
          :loading="groups.isFetching.value"
          class="modern-usage-choice"
          @update:model-value="setFilter('group_id', $event)"
        >
          <template #option="{ option }">
            <AppChannelIcon
              v-if="groupMap.get(option.value)"
              :icon="groupMap.get(option.value)!.channelIcon"
              :name="groupMap.get(option.value)!.channelName"
              :mark="groupMap.get(option.value)!.channelMark"
              size="sm"
            />
            <span>{{ option.label }}</span>
          </template>
        </AppSearchSelect>
        <AppSearchSelect
          v-if="admin"
          :key="state.filters.group_id || 'all'"
          :model-value="state.filters.credential_id ?? ''"
          :options="[{ value: '', label: t('usage.allCredentials') }]"
          :load-options="loadCredentials"
          :selected-option="
            state.filters.credential_id
              ? {
                  value: state.filters.credential_id,
                  label: label('credential_id', state.filters.credential_id),
                }
              : undefined
          "
          :label="t('usage.filters.credential_id')"
          :label-hidden="!isMobile"
          :placeholder="
            t(state.filters.group_id ? 'usage.allCredentials' : 'logs.selectGroupFirst')
          "
          :disabled="!state.filters.group_id"
          class="modern-usage-choice"
          @update:model-value="setFilter('credential_id', $event)"
        />
        <AppSearchSelect
          v-if="admin"
          :model-value="state.filters.channel_id ?? ''"
          :options="channelOptions"
          :label="t('usage.filters.channel_id')"
          :label-hidden="!isMobile"
          class="modern-usage-choice"
          @update:model-value="setFilter('channel_id', $event)"
        >
          <template #option="{ option }">
            <AppChannelIcon
              v-if="channelMap.get(option.value)"
              :icon="channelMap.get(option.value)!.icon"
              :name="channelMap.get(option.value)!.name"
              :mark="channelMap.get(option.value)!.mark"
              size="sm"
            />
            <span>{{ option.label }}</span>
          </template>
        </AppSearchSelect>
        <AppSearchSelect
          v-if="admin"
          :model-value="state.filters.access_key_id ?? ''"
          :options="keyOptions"
          :label="t('usage.filters.access_key_id')"
          :label-hidden="!isMobile"
          :loading="keys.isFetching.value"
          class="modern-usage-choice"
          @update:model-value="setFilter('access_key_id', $event)"
        />
        <AppModelSelect
          :model-value="model"
          :models="models"
          :label="t('usage.filters.upstream_model')"
          :error="modelError"
          :label-hidden="!isMobile"
          fuzzy
          class="modern-usage-model"
          @update:model-value="setModel"
        />
      </AppFilterSheet>
    </form>
    <div class="modern-usage-filterbar">
      <AppTooltip
        v-if="
          report &&
          (report.collectionIncomplete ||
            report.summary.usage_missing_count ||
            report.summary.partial_count)
        "
        :label="
          t('usage.incompleteHint', {
            missing: compact(report.summary.usage_missing_count),
            partial: compact(report.summary.partial_count),
          })
        "
      >
        <AppBadge tone="warning" size="xs" tabindex="0">{{ t('usage.incomplete') }}</AppBadge>
      </AppTooltip>
      <AppFilterSummary :items="filterSummary" @remove="reset" @reset="reset()" />
    </div>
    <AppCollectionState
      v-if="!report"
      :loading="query.isPending.value"
      :error="query.isError.value"
      :title="t(query.isError.value ? 'usage.failed' : 'usage.loading')"
    >
      <AppButton v-if="query.isError.value" @click="refresh">{{ t('ui.retry') }}</AppButton>
    </AppCollectionState>
    <div
      v-else
      class="modern-usage-report"
      :aria-busy="query.isFetching.value"
      :class="{ 'is-updating': query.isPlaceholderData.value }"
    >
      <UsageMetrics :report="report" :mobile="isMobile" />
      <AppSegmentedControl
        v-if="isMobile"
        v-model="mobileSection"
        :label="t('pages.usage.title')"
        :options="[
          { value: 'trend', label: t('ui.mobile.trend') },
          { value: 'sources', label: t('ui.mobile.sources') },
          { value: 'composition', label: t('ui.mobile.composition') },
        ]"
      />
      <div v-show="!isMobile || mobileSection !== 'sources'" class="modern-usage-visuals">
        <AppPanel v-show="!isMobile || mobileSection === 'trend'" :title="t('usage.trend')" compact>
          <template #actions>
            <AppSelect
              v-if="isMobile"
              :model-value="state.trend"
              :options="trendOptions"
              :label="t('usage.trend')"
              label-hidden
              @update:model-value="
                state = {
                  ...state,
                  trend: trendMetrics.find((value) => value === $event) ?? 'requests',
                }
              "
            />
            <AppSegmentedControl
              v-else
              :model-value="state.trend"
              :options="trendOptions"
              :label="t('usage.trend')"
              size="sm"
              appearance="field"
              @update:model-value="
                state = {
                  ...state,
                  trend: trendMetrics.find((value) => value === $event) ?? 'requests',
                }
              "
            />
          </template>
          <div class="modern-usage-chart-context">
            <span>{{ period }}</span>
            <span>{{ bucketLabel }}</span>
          </div>
          <UsageTrend :report="report" :metric="state.trend" />
        </AppPanel>
        <UsageComposition
          v-show="!isMobile || mobileSection === 'composition'"
          :summary="report.summary"
        />
      </div>
      <AppPanel
        v-show="!isMobile || mobileSection === 'sources'"
        :title="t('usage.sources')"
        compact
        class="modern-usage-sources"
      >
        <template #actions>
          <AppSelect
            v-if="isMobile"
            :model-value="state.metric"
            :options="rankOptions"
            :label="t('usage.rankMetric')"
            label-hidden
            @update:model-value="
              state = {
                ...state,
                metric: usageMetrics.find((value) => value === $event) ?? 'requests',
              }
            "
          />
          <AppSegmentedControl
            v-else
            :model-value="state.metric"
            :options="rankOptions"
            :label="t('usage.rankMetric')"
            size="sm"
            appearance="field"
            @update:model-value="
              state = {
                ...state,
                metric: usageMetrics.find((value) => value === $event) ?? 'requests',
              }
            "
          />
        </template>
        <AppSelect
          v-if="isMobile"
          v-model="mobileDimension"
          :label="t('usage.sources')"
          :options="dimensions.map((value) => ({ value, label: t('usage.dimensions.' + value) }))"
        />
        <div class="modern-usage-ranks">
          <div
            v-for="dimension in dimensions"
            v-show="!isMobile || dimension === mobileDimension"
            :key="dimension"
            class="modern-usage-rank-cell"
          >
            <UsageRank
              :dimension="dimension"
              :distribution="report.distributions[dimension]![state.metric]"
              :metric="state.metric"
              :cost-unavailable="
                report.summary.estimated_cost_nano_usd === '0' &&
                (report.summary.unpriced_request_count > 0 ||
                  report.summary.pricing_partial_count > 0)
              "
              :groups="groups.data.value?.items"
              :access-keys="keys.data.value"
              :disabled="query.isPlaceholderData.value"
              @select="drill(dimension, $event)"
              @logs="logs(dimension, $event)"
            />
          </div>
        </div>
      </AppPanel>
    </div>
  </div>
</template>

<style scoped>
.modern-usage-workspace {
  display: flex;
  flex: 1;
  min-height: 0;
  min-width: 0;
  flex-direction: column;
}
.modern-usage-filters {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-3);
  padding: var(--modern-space-5) 0 var(--modern-space-3);
}
.modern-usage-date {
  flex: 2 1 240px;
  min-width: 0;
}
.modern-usage-model {
  flex: 1.3 1 200px;
  min-width: 0;
}
.modern-usage-choice {
  flex: 1 1 180px;
  min-width: 0;
}
.modern-usage-choice + .modern-usage-choice {
  flex-basis: 144px;
}
.modern-usage-filterbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-3);
}
.modern-usage-filterbar {
  flex: none;
  justify-content: space-between;
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  background: var(--modern-canvas);
  padding-bottom: var(--modern-space-3);
}
.modern-usage-filterbar > :last-child {
  margin-left: auto;
}
.modern-usage-report {
  display: grid;
  flex: 1;
  align-content: start;
  gap: var(--modern-page-gap);
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding-block: var(--modern-space-4) var(--modern-space-6);
  scrollbar-gutter: var(--modern-scrollbar-gutter);
}
.modern-usage-report.is-updating {
  opacity: var(--modern-opacity-quiet);
}
.modern-usage-visuals {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(300px, 1fr);
  gap: var(--modern-space-4);
  align-items: stretch;
}
.modern-usage-chart-context {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--modern-space-2);
  margin-bottom: var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
}
.modern-usage-sources {
  container: usage-sources / inline-size;
  min-width: 0;
}
.modern-usage-ranks {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  align-items: stretch;
}
.modern-usage-rank-cell {
  min-width: 0;
  padding-inline: var(--modern-space-5);
}
.modern-usage-rank-cell:first-child {
  padding-left: 0;
}
.modern-usage-rank-cell:last-child {
  padding-right: 0;
}
.modern-usage-rank-cell + .modern-usage-rank-cell {
  border-left: var(--modern-line-width) solid var(--modern-border);
}
.modern-usage-ranks > :only-child {
  grid-column: 1 / -1;
  padding-inline: 0;
}
@media (max-width: 1150px) {
  .modern-usage-visuals {
    grid-template-columns: minmax(0, 1fr);
  }
}
@media (max-width: 760px) {
  .modern-usage-workspace {
    flex: none;
  }
  .modern-usage-report {
    flex: none;
    overflow: visible;
  }
  .modern-usage-filters {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--modern-space-2);
    padding-top: var(--modern-space-3);
  }
  .modern-usage-date,
  .modern-usage-model {
    grid-column: 1 / -1;
  }
}
/* 排行按实际内容宽度换列，展开侧栏时也能保留足够的名称空间。 */
@container usage-sources (max-width: 1080px) {
  .modern-usage-ranks {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .modern-usage-rank-cell:nth-child(2) {
    padding-right: 0;
  }
  .modern-usage-rank-cell:nth-child(3) {
    grid-column: 1 / -1;
    margin-top: var(--modern-space-4);
    padding: var(--modern-space-4) 0 0;
    border-top: var(--modern-line-width) solid var(--modern-border);
    border-left: 0;
  }
}
@container usage-sources (max-width: 700px) {
  .modern-usage-ranks {
    grid-template-columns: minmax(0, 1fr);
  }
  .modern-usage-rank-cell {
    padding-inline: 0;
  }
  .modern-usage-rank-cell + .modern-usage-rank-cell {
    margin-top: var(--modern-space-4);
    padding-top: var(--modern-space-4);
    border-top: var(--modern-line-width) solid var(--modern-border);
    border-left: 0;
  }
}
@media (max-width: 760px) {
  .modern-usage-filters {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
  }
  .modern-usage-date {
    grid-column: auto;
  }
  .modern-usage-visuals,
  .modern-usage-ranks {
    display: block;
  }
  .modern-usage-report {
    grid-template-columns: minmax(0, 1fr);
    min-width: 0;
  }
  .modern-usage-rank-cell + .modern-usage-rank-cell {
    margin-top: 0;
    padding-top: 0;
    border-top: 0;
  }
}
</style>
