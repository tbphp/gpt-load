<script setup lang="ts">
import { ListFilter, RefreshCw } from '@lucide/vue'
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { lazySurface } from '@/app/async-surface'
import { monitorLocation } from '@/app/route-locations'
import type { UsageReportDto } from '@/app/resources/usage'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppDateTimeRangePicker from '@/components/ui/AppDateTimeRangePicker.vue'
import AppTabs, { type AppTabItem } from '@/components/ui/AppTabs.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import {
  localDateTimeInput,
  parseLocalDateTime,
  resolveDateTimePreset,
  type DateTimePreset,
} from '@/lib/time'
import { useAuthSession } from '@/features/auth/auth-session'

import HealthTab from './HealthTab.vue'
import { parseAppliedLogFilters } from './log-filters'
import {
  normalizeAccessKeyMonitorQuery,
  normalizeMonitorQuery,
  normalizeMonitorTab,
  logsMonitorQuery,
  parseUsageMonitorState,
  sameMonitorQuery,
  scopeAccessKeyLogFilters,
  scopeAccessKeyUsageFilters,
  usageMonitorQuery,
} from './monitor-route'
import { parseAppliedUsageFilters } from './usage-filters'

const InspectorTab = lazySurface(() => import('./InspectorTab.vue'))
const LogsTab = lazySurface(() => import('./LogsTab.vue'))
const UsageTab = lazySurface(() => import('./UsageTab.vue'))

const route = useRoute()
const session = useAuthSession()
const router = useRouter()
const { t } = useI18n()
const healthTab = ref<InstanceType<typeof HealthTab> | null>(null)
const logsTab = ref<{
  openFilters: () => void
  refresh: () => Promise<void>
  filterCount: number
} | null>(null)
const usageTab = ref<{
  openFilters: () => void
  refresh: () => Promise<void>
  navigationReport?: UsageReportDto
  navigationPending: boolean
} | null>(null)
const healthRefreshPending = ref(false)
const logsRefreshPending = ref(false)
const usageRefreshPending = ref(false)
const isAccessKey = computed(() => session.state.principalType === 'access_key')
const canonicalQuery = computed(() =>
  isAccessKey.value
    ? normalizeAccessKeyMonitorQuery(route.query)
    : normalizeMonitorQuery(route.query),
)
const activeTab = computed(() => normalizeMonitorTab(canonicalQuery.value.tab))
const dataTab = computed(() => (activeTab.value === 'logs' ? logsTab.value : usageTab.value))
const dataRefreshPending = computed(() =>
  activeTab.value === 'logs' ? logsRefreshPending.value : usageRefreshPending.value,
)
const isCanonicalQuery = computed(() => sameMonitorQuery(route.query, canonicalQuery.value))
const items = computed<AppTabItem[]>(() => {
  const shared = [
    { value: 'usage', label: t('monitor.tabs.usage') },
    {
      value: 'logs',
      label: t('monitor.tabs.logs'),
      disabled: activeTab.value === 'usage' && usageTab.value?.navigationPending,
    },
  ]
  return isAccessKey.value
    ? shared
    : [
        { value: 'health', label: t('monitor.tabs.health') },
        ...shared,
        { value: 'inspector', label: t('monitor.tabs.inspector') },
      ]
})
const routeUsageFilters = computed(() => parseAppliedUsageFilters(route.query))
const routeLogFilters = computed(() => parseAppliedLogFilters(route.query))
const routeTimeFilters = computed(() =>
  activeTab.value === 'logs' ? routeLogFilters.value : routeUsageFilters.value,
)
const resolvedTimeRange = ref<{ from_ms: number; to_ms: number; preset?: DateTimePreset }>()

// 快捷范围只在进入页面、切换快捷项或显式刷新时解析，筛选和翻页共用同一区间。
watch(
  () => {
    if (activeTab.value !== 'logs' && activeTab.value !== 'usage') return undefined
    const filters = routeTimeFilters.value
    return filters.preset ?? `${filters.from_ms}:${filters.to_ms}`
  },
  (selection) => {
    if (selection === undefined) {
      resolvedTimeRange.value = undefined
      return
    }
    const { from_ms, to_ms, preset } = routeTimeFilters.value
    if (preset && resolvedTimeRange.value?.preset === preset) return
    resolvedTimeRange.value = { from_ms, to_ms, preset }
  },
  { immediate: true },
)

const usageFilters = computed(() => {
  const filters = { ...routeUsageFilters.value, ...resolvedTimeRange.value }
  return isAccessKey.value ? scopeAccessKeyUsageFilters(filters) : filters
})
const logFilters = computed(() => {
  const filters = { ...routeLogFilters.value, ...resolvedTimeRange.value }
  return isAccessKey.value ? scopeAccessKeyLogFilters(filters) : filters
})
const timeFilters = computed(() =>
  activeTab.value === 'logs' ? logFilters.value : usageFilters.value,
)
const timeDraft = ref<{ from: string; to: string; preset?: DateTimePreset }>({
  from: '',
  to: '',
})
const timeValues = computed(() => {
  const draft = timeDraft.value
  const current = timeFilters.value
  // 回拨时同一本地时间对应两个时刻，未改动的端点保留原始毫秒值。
  return {
    from:
      draft.from !== '' && draft.from === localDateTimeInput(current.from_ms)
        ? current.from_ms
        : parseLocalDateTime(draft.from)?.getTime(),
    to:
      draft.to !== '' && draft.to === localDateTimeInput(current.to_ms)
        ? current.to_ms
        : parseLocalDateTime(draft.to)?.getTime(),
  }
})
const timeErrors = computed(() => {
  const { from, to } = timeValues.value
  return {
    from: from === undefined ? t('monitor.logs.errors.dateTime') : undefined,
    to:
      to === undefined
        ? t('monitor.logs.errors.dateTime')
        : from !== undefined && to <= from
          ? t('monitor.logs.errors.range')
          : undefined,
  }
})
watch(
  () => [
    activeTab.value,
    timeFilters.value.from_ms,
    timeFilters.value.to_ms,
    timeFilters.value.preset,
  ],
  resetTimeDraft,
  { immediate: true },
)
const usageFilterCount = computed(
  () =>
    Number(!isAccessKey.value && usageFilters.value.access_key_id !== undefined) +
    Number(!isAccessKey.value && usageFilters.value.group_id !== undefined) +
    Number(!isAccessKey.value && usageFilters.value.channel_id !== undefined) +
    Number(!isAccessKey.value && usageFilters.value.credential_id !== undefined) +
    Number(usageFilters.value.upstream_model !== undefined),
)
const filterCount = computed(() =>
  activeTab.value === 'logs' ? (logsTab.value?.filterCount ?? 0) : usageFilterCount.value,
)

watch(
  () => route.query,
  (query) => {
    const normalized = canonicalQuery.value
    if (!sameMonitorQuery(query, normalized)) {
      void router.replace(monitorLocation(normalized))
    }
  },
  { immediate: true },
)

function selectTab(value: string): void {
  const tab = normalizeMonitorTab(value)
  if (isAccessKey.value && tab !== 'usage' && tab !== 'logs') return
  if (tab === activeTab.value) return
  if (activeTab.value === 'usage' && tab === 'logs') {
    if (usageTab.value?.navigationPending) return
    const report = usageTab.value?.navigationReport
    const filters = usageFilters.value
    // 显式起止时间已经确定，报告不可用时仍可保持同一查询区间。
    const logRange = report ?? filters
    void router.push(
      monitorLocation(
        logsMonitorQuery(
          {
            limit: 20,
            from_ms: logRange.from_ms,
            to_ms: logRange.to_ms,
            preset: filters.preset,
            access_key_id: filters.access_key_id,
            group_id: filters.group_id,
            channel_id: filters.channel_id,
            credential_id: filters.credential_id,
            upstream_model: filters.upstream_model,
          },
          {
            filtersOpen: false,
            cursorHistory: [],
          },
        ),
      ),
    )
    return
  }
  if (activeTab.value === 'logs' && tab === 'usage') {
    void router.push(
      monitorLocation(usageMonitorQuery(parseAppliedUsageFilters({ ...logFilters.value }))),
    )
    return
  }
  void router.push(monitorLocation({ tab }))
}

async function refreshHealth(): Promise<void> {
  if (!healthTab.value || healthRefreshPending.value) return
  healthRefreshPending.value = true
  try {
    await healthTab.value.refresh()
  } finally {
    healthRefreshPending.value = false
  }
}

async function refreshData(): Promise<void> {
  const current = dataTab.value
  const pending = activeTab.value === 'logs' ? logsRefreshPending : usageRefreshPending
  if (!current || pending.value) return
  pending.value = true
  try {
    const preset = timeFilters.value.preset
    if (preset) {
      const interval = resolveDateTimePreset(preset, Math.floor(Date.now() / 1000) * 1000)
      if (interval.to_ms > interval.from_ms) {
        resolvedTimeRange.value = { ...interval, preset }
        await nextTick()
      }
    }
    await current.refresh()
  } finally {
    pending.value = false
  }
}

function selectTimeShortcut(preset: DateTimePreset, from: number, to: number): void {
  if (to <= from) return
  applyTimeRange(from, to, preset)
}

function resetTimeDraft(): void {
  timeDraft.value = {
    from: localDateTimeInput(timeFilters.value.from_ms),
    to: localDateTimeInput(timeFilters.value.to_ms),
    preset: timeFilters.value.preset,
  }
}

function applyCustomTime(): void {
  const { from, to } = timeValues.value
  if (from === undefined || to === undefined || to <= from) return
  applyTimeRange(from, to)
}

function applyTimeRange(from: number, to: number, preset?: DateTimePreset): void {
  resolvedTimeRange.value = { from_ms: from, to_ms: to, preset }
  if (activeTab.value === 'logs') {
    void router.push(
      monitorLocation(logsMonitorQuery({ ...logFilters.value, from_ms: from, to_ms: to, preset })),
    )
    return
  }
  const state = parseUsageMonitorState(route.query)
  void router.push(
    monitorLocation(
      usageMonitorQuery(
        { ...usageFilters.value, from_ms: from, to_ms: to, preset },
        {
          filtersOpen: false,
          seriesExpanded: false,
          metric: state.metric,
        },
      ),
    ),
  )
}
</script>

<template>
  <PageFrame aria-labelledby="monitor-title">
    <LedgerSheet class="monitor-page">
      <PageHeader id="monitor-title" :title="t('monitor.title')" />
      <AppTabs
        class="monitor-tabs"
        :class="{ 'monitor-tabs--data': activeTab === 'usage' || activeTab === 'logs' }"
        :model-value="activeTab"
        :label="t('monitor.tabs.label')"
        :items="items"
        appearance="detail"
        @update:model-value="selectTab"
      >
        <template #actions>
          <AppButton
            v-if="activeTab === 'health'"
            class="monitor-refresh"
            variant="secondary"
            size="compact"
            :busy="healthRefreshPending"
            @click="refreshHealth"
          >
            <RefreshCw
              :class="{ 'monitor-refresh-icon--spinning': healthRefreshPending }"
              :size="14"
              aria-hidden="true"
            />
            {{ t('monitor.health.refresh') }}
          </AppButton>
          <div
            v-else-if="activeTab === 'usage' || activeTab === 'logs'"
            class="monitor-data-actions"
          >
            <AppDateTimeRangePicker
              :key="activeTab"
              v-model:from="timeDraft.from"
              v-model:to="timeDraft.to"
              v-model:preset="timeDraft.preset"
              :applied-from="localDateTimeInput(timeFilters.from_ms)"
              :applied-to="localDateTimeInput(timeFilters.to_ms)"
              :applied-preset="timeFilters.preset"
              :label="t('monitor.usage.filters.range')"
              :from-label="t('monitor.logs.filters.from')"
              :to-label="t('monitor.logs.filters.to')"
              :from-error="timeErrors.from"
              :to-error="timeErrors.to"
              :apply-label="t('monitor.usage.filters.apply')"
              :apply-disabled="Boolean(timeErrors.from || timeErrors.to)"
              @shortcut="selectTimeShortcut"
              @apply="applyCustomTime"
              @open="resetTimeDraft"
            />
            <AppButton variant="secondary" size="compact" @click="dataTab?.openFilters()">
              <ListFilter :size="14" aria-hidden="true" />
              {{ t('monitor.usage.filters.button') }}
              <span v-if="filterCount > 0" class="monitor-filter-count">
                {{ filterCount }}
              </span>
            </AppButton>
            <AppButton
              class="monitor-refresh"
              variant="secondary"
              size="compact"
              :busy="dataRefreshPending"
              @click="refreshData"
            >
              <RefreshCw
                :class="{ 'monitor-refresh-icon--spinning': dataRefreshPending }"
                :size="14"
                aria-hidden="true"
              />
              {{ t('monitor.usage.filters.refresh') }}
            </AppButton>
          </div>
        </template>

        <template v-if="isCanonicalQuery">
          <div v-if="activeTab === 'health'" class="monitor-panel">
            <HealthTab ref="healthTab" />
          </div>
          <div v-else-if="activeTab === 'logs'" class="monitor-panel">
            <LogsTab ref="logsTab" :filters="logFilters" />
          </div>
          <div v-else-if="activeTab === 'usage'" class="monitor-panel">
            <UsageTab ref="usageTab" :filters="usageFilters" />
          </div>
          <div v-else class="monitor-panel">
            <InspectorTab />
          </div>
        </template>
      </AppTabs>
    </LedgerSheet>
  </PageFrame>
</template>

<style scoped>
.monitor-page {
  display: grid;
  min-height: 760px;
  min-width: 0;
  align-content: start;
  gap: 0;
}

.monitor-panel {
  min-width: 0;
  padding-top: var(--detail-panel-padding-top);
}

.monitor-tabs :deep(.app-tabs__bar) {
  border-top: 0;
}

.monitor-refresh-icon--spinning {
  animation: monitor-refresh-spin 800ms linear infinite;
}

.monitor-refresh[aria-busy='true'] {
  opacity: 1;
}

.monitor-data-actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.monitor-filter-count {
  display: inline-grid;
  min-width: 17px;
  height: 17px;
  place-items: center;
  border-radius: 999px;
  background: var(--color-action-soft);
  color: var(--color-action);
  padding-inline: 4px;
  font-family: var(--font-mono);
  font-size: 10px;
}

@keyframes monitor-refresh-spin {
  to {
    transform: rotate(360deg);
  }
}

@media (max-width: 800px) {
  .monitor-page {
    min-height: 0;
  }

  .monitor-panel {
    padding-top: var(--detail-panel-padding-top-compact);
  }

  .monitor-tabs--data :deep(.app-tabs__bar) {
    flex-wrap: wrap;
  }

  .monitor-tabs--data :deep(.app-tabs__list),
  .monitor-tabs--data :deep(.app-tabs__actions) {
    width: 100%;
  }
}

@media (max-width: 560px) {
  .monitor-data-actions {
    width: 100%;
    flex-wrap: wrap;
    gap: var(--space-1);
  }

  .monitor-data-actions :deep(.app-popover) {
    flex-basis: 100%;
  }
}

@media (prefers-reduced-motion: reduce) {
  .monitor-refresh-icon--spinning {
    animation: none;
  }
}
</style>
