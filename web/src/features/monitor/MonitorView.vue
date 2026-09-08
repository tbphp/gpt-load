<script setup lang="ts">
import { ListFilter, RefreshCw } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
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
import { localDateTimeInput, parseLocalDateTime, type DateTimePreset } from '@/lib/time'
import { parseAppliedLogFilters } from './log-filters'
import { useAuthSession } from '@/features/auth/auth-session'

import HealthTab from './HealthTab.vue'
import {
  normalizeAccessKeyMonitorQuery,
  normalizeMonitorQuery,
  normalizeMonitorTab,
  logsMonitorQuery,
  parseLogsMonitorState,
  parseUsageMonitorState,
  sameMonitorQuery,
  scopeAccessKeyUsageFilters,
} from './monitor-route'
import { usageMonitorQuery } from './monitor-route'
import { parseAppliedUsageFilters } from './usage-filters'

const InspectorTab = lazySurface(() => import('./InspectorTab.vue'))
const LogsTab = lazySurface(() => import('./LogsTab.vue'))
const UsageTab = lazySurface(() => import('./UsageTab.vue'))

const route = useRoute()
const session = useAuthSession()
const router = useRouter()
const { t } = useI18n()
const healthTab = ref<InstanceType<typeof HealthTab> | null>(null)
const usageTab = ref<{
  openFilters: () => void
  refresh: () => Promise<void>
  navigationReport?: UsageReportDto
  navigationPending: boolean
} | null>(null)
const healthRefreshPending = ref(false)
const usageRefreshPending = ref(false)
const isAccessKey = computed(() => session.state.principalType === 'access_key')
const canonicalQuery = computed(() =>
  isAccessKey.value
    ? normalizeAccessKeyMonitorQuery(route.query)
    : normalizeMonitorQuery(route.query),
)
const activeTab = computed(() => normalizeMonitorTab(canonicalQuery.value.tab))
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
const usageFilters = computed(() => {
  const filters = parseAppliedUsageFilters(route.query)
  return isAccessKey.value ? scopeAccessKeyUsageFilters(filters) : filters
})
const usageTimeDraft = ref<{ from: string; to: string; preset?: DateTimePreset }>({
  from: '',
  to: '',
})
const usageTimeValues = computed(() => {
  const draft = usageTimeDraft.value
  const current = usageFilters.value
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
const usageTimeErrors = computed(() => {
  const { from, to } = usageTimeValues.value
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
  () => [usageFilters.value.from_ms, usageFilters.value.to_ms, usageFilters.value.preset],
  resetUsageTimeDraft,
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
            access_key_id: filters.access_key_id,
            group_id: filters.group_id,
            channel_id: filters.channel_id,
            credential_id: filters.credential_id,
            upstream_model: filters.upstream_model,
          },
          {
            filtersOpen: false,
            cursorHistory: [],
            usagePreset: filters.preset,
          },
        ),
      ),
    )
    return
  }
  if (activeTab.value === 'logs' && tab === 'usage') {
    const filters = parseAppliedLogFilters(route.query)
    const previous = parseLogsMonitorState(route.query)
    void router.push(
      monitorLocation(
        usageMonitorQuery(parseAppliedUsageFilters({ ...filters, preset: previous.usagePreset })),
      ),
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

async function refreshUsage(): Promise<void> {
  if (!usageTab.value || usageRefreshPending.value) return
  usageRefreshPending.value = true
  try {
    await usageTab.value.refresh()
  } finally {
    usageRefreshPending.value = false
  }
}

function selectUsageShortcut(preset: DateTimePreset, from: number, to: number): void {
  if (to <= from) return
  applyUsageTimeRange(from, to, preset)
}

function resetUsageTimeDraft(): void {
  usageTimeDraft.value = {
    from: localDateTimeInput(usageFilters.value.from_ms),
    to: localDateTimeInput(usageFilters.value.to_ms),
    preset: usageFilters.value.preset,
  }
}

function applyUsageCustomTime(): void {
  const { from, to } = usageTimeValues.value
  if (from === undefined || to === undefined || to <= from) return
  applyUsageTimeRange(from, to, usageTimeDraft.value.preset)
}

function applyUsageTimeRange(from: number, to: number, preset?: DateTimePreset): void {
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
        :class="{ 'monitor-tabs--usage': activeTab === 'usage' }"
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
          <div v-else-if="activeTab === 'usage'" class="monitor-usage-actions">
            <AppDateTimeRangePicker
              v-model:from="usageTimeDraft.from"
              v-model:to="usageTimeDraft.to"
              v-model:preset="usageTimeDraft.preset"
              :applied-from="localDateTimeInput(usageFilters.from_ms)"
              :applied-to="localDateTimeInput(usageFilters.to_ms)"
              :label="t('monitor.usage.filters.range')"
              :from-label="t('monitor.logs.filters.from')"
              :to-label="t('monitor.logs.filters.to')"
              :from-error="usageTimeErrors.from"
              :to-error="usageTimeErrors.to"
              :rolling-end-offset-ms="0"
              :apply-label="t('monitor.usage.filters.apply')"
              :apply-disabled="Boolean(usageTimeErrors.from || usageTimeErrors.to)"
              @shortcut="selectUsageShortcut"
              @apply="applyUsageCustomTime"
              @open="resetUsageTimeDraft"
            />
            <AppButton variant="secondary" size="compact" @click="usageTab?.openFilters()">
              <ListFilter :size="14" aria-hidden="true" />
              {{ t('monitor.usage.filters.button') }}
              <span v-if="usageFilterCount > 0" class="monitor-filter-count">
                {{ usageFilterCount }}
              </span>
            </AppButton>
            <AppButton
              class="monitor-refresh"
              variant="secondary"
              size="compact"
              :busy="usageRefreshPending"
              @click="refreshUsage"
            >
              <RefreshCw
                :class="{ 'monitor-refresh-icon--spinning': usageRefreshPending }"
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
            <LogsTab />
          </div>
          <div v-else-if="activeTab === 'usage'" class="monitor-panel">
            <UsageTab ref="usageTab" />
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

.monitor-usage-actions {
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

  .monitor-tabs--usage :deep(.app-tabs__bar) {
    flex-wrap: wrap;
  }

  .monitor-tabs--usage :deep(.app-tabs__list),
  .monitor-tabs--usage :deep(.app-tabs__actions) {
    width: 100%;
  }
}

@media (max-width: 560px) {
  .monitor-usage-actions {
    width: 100%;
    flex-wrap: wrap;
    gap: var(--space-1);
  }

  .monitor-usage-actions :deep(.app-popover) {
    flex-basis: 100%;
  }
}

@media (prefers-reduced-motion: reduce) {
  .monitor-refresh-icon--spinning {
    animation: none;
  }
}
</style>
