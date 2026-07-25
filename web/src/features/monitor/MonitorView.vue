<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import AppTabs, { type AppTabItem } from '@/components/ui/AppTabs.vue'
import PageHeader from '@/components/ui/PageHeader.vue'

import HealthTab from './HealthTab.vue'
import LogsTab from './LogsTab.vue'
import { normalizeMonitorQuery, normalizeMonitorTab, sameMonitorQuery } from './monitor-route'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const activeTab = computed(() => normalizeMonitorTab(route.query.tab))
const items = computed<AppTabItem[]>(() => [
  { value: 'health', label: t('monitor.tabs.health'), testId: 'monitor-tab-health' },
  { value: 'logs', label: t('monitor.tabs.logs'), testId: 'monitor-tab-logs' },
  { value: 'inspector', label: t('monitor.tabs.inspector'), testId: 'monitor-tab-inspector' },
])

watch(
  () => route.query,
  (query) => {
    const normalized = normalizeMonitorQuery(query)
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
      <div v-if="activeTab === 'health'" data-test="monitor-health-slot">
        <HealthTab />
      </div>
      <div v-else-if="activeTab === 'logs'" data-test="monitor-logs-slot">
        <LogsTab />
      </div>
      <div v-else data-test="monitor-inspector-slot" />
    </AppTabs>
  </div>
</template>

<style scoped>
.monitor-page {
  display: grid;
  gap: var(--space-6);
}
</style>
