<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { lazySurface } from '@/app/async-surface'
import AppTabs, { type AppTabItem } from '@/components/ui/AppTabs.vue'
import PageHeader from '@/components/ui/PageHeader.vue'

import { normalizeMonitorQuery, normalizeMonitorTab, sameMonitorQuery } from './monitor-route'

const HealthTab = lazySurface(() => import('./HealthTab.vue'))
const InspectorTab = lazySurface(() => import('./InspectorTab.vue'))
const LogsTab = lazySurface(() => import('./LogsTab.vue'))
const UsageTab = lazySurface(() => import('./UsageTab.vue'))

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const activeTab = computed(() => normalizeMonitorTab(route.query.tab))
const canonicalQuery = computed(() => normalizeMonitorQuery(route.query))
const isCanonicalQuery = computed(() => sameMonitorQuery(route.query, canonicalQuery.value))
const items = computed<AppTabItem[]>(() => [
  { value: 'health', label: t('monitor.tabs.health'), testId: 'monitor-tab-health' },
  { value: 'logs', label: t('monitor.tabs.logs'), testId: 'monitor-tab-logs' },
  { value: 'inspector', label: t('monitor.tabs.inspector'), testId: 'monitor-tab-inspector' },
  { value: 'usage', label: t('monitor.tabs.usage'), testId: 'monitor-tab-usage' },
])

watch(
  () => route.query,
  (query) => {
    const normalized = canonicalQuery.value
    if (!sameMonitorQuery(query, normalized)) {
      void router.replace({ name: 'monitor', query: normalized })
    }
  },
  { immediate: true },
)

function selectTab(value: string): void {
  const tab = normalizeMonitorTab(value)
  if (tab === activeTab.value) return
  void router.push({ name: 'monitor', query: { tab } })
}
</script>

<template>
  <div class="monitor-page">
    <PageHeader
      :eyebrow="t('monitor.currentState')"
      :title="t('monitor.title')"
      :description="t('monitor.description')"
    />
    <AppTabs
      :model-value="activeTab"
      :label="t('monitor.tabs.label')"
      :items="items"
      @update:model-value="selectTab"
    >
      <template v-if="isCanonicalQuery">
        <div v-if="activeTab === 'health'" class="monitor-panel" data-test="monitor-health-slot">
          <HealthTab />
        </div>
        <div v-else-if="activeTab === 'logs'" class="monitor-panel" data-test="monitor-logs-slot">
          <LogsTab />
        </div>
        <div v-else-if="activeTab === 'usage'" class="monitor-panel" data-test="monitor-usage-slot">
          <UsageTab />
        </div>
        <div v-else class="monitor-panel" data-test="monitor-inspector-slot">
          <InspectorTab />
        </div>
      </template>
    </AppTabs>
  </div>
</template>

<style scoped>
.monitor-page {
  display: grid;
  min-width: 0;
  gap: var(--space-6);
}

.monitor-panel {
  min-width: 0;
}
</style>
