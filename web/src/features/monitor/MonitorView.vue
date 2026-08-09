<script setup lang="ts">
import { ListFilter, RefreshCw } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { lazySurface } from '@/app/async-surface'
import { monitorLocation } from '@/app/route-locations'
import { usageRanges } from '@/app/resources/usage'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppTabs, { type AppTabItem } from '@/components/ui/AppTabs.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import { isTimeRange } from '@/lib/time'
import { useAuthSession } from '@/features/auth/auth-session'

import HealthTab from './HealthTab.vue'
import {
  normalizeAccessKeyMonitorQuery,
  normalizeMonitorQuery,
  normalizeMonitorTab,
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
const usageTab = ref<{ openFilters: () => void; refresh: () => Promise<void> } | null>(null)
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
    { value: 'logs', label: t('monitor.tabs.logs') },
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
const usageRangeOptions = computed(() =>
  usageRanges.map((value) => ({
    value,
    label: t(`monitor.usage.filters.ranges.${value}`),
  })),
)
const usageFilterCount = computed(
  () =>
    Number(!isAccessKey.value && usageFilters.value.group_id !== undefined) +
    Number(usageFilters.value.model !== undefined),
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

function selectUsageRange(value: string): void {
  if (!isTimeRange(value)) return
  const state = parseUsageMonitorState(route.query)
  void router.push(
    monitorLocation(
      usageMonitorQuery(
        { ...usageFilters.value, range: value },
        {
          filtersOpen: false,
          expandedBreakdowns: [],
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
            <AppSelect
              :model-value="usageFilters.range"
              :label="t('monitor.usage.filters.range')"
              :options="usageRangeOptions"
              size="compact"
              @update:model-value="selectUsageRange"
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
}

@media (max-width: 560px) {
  .monitor-usage-actions {
    gap: var(--space-1);
  }
}

@media (prefers-reduced-motion: reduce) {
  .monitor-refresh-icon--spinning {
    animation: none;
  }
}
</style>
