<script setup lang="ts">
import { RefreshCw } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { lazySurface } from '@/app/async-surface'
import { monitorLocation } from '@/app/route-locations'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppTabs, { type AppTabItem } from '@/components/ui/AppTabs.vue'
import PageHeader from '@/components/ui/PageHeader.vue'

import HealthTab from './HealthTab.vue'
import { normalizeMonitorQuery, normalizeMonitorTab, sameMonitorQuery } from './monitor-route'

const InspectorTab = lazySurface(() => import('./InspectorTab.vue'))
const LogsTab = lazySurface(() => import('./LogsTab.vue'))
const UsageTab = lazySurface(() => import('./UsageTab.vue'))

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const healthTab = ref<InstanceType<typeof HealthTab> | null>(null)
const healthRefreshPending = ref(false)
const activeTab = computed(() => normalizeMonitorTab(route.query.tab))
const canonicalQuery = computed(() => normalizeMonitorQuery(route.query))
const isCanonicalQuery = computed(() => sameMonitorQuery(route.query, canonicalQuery.value))
const items = computed<AppTabItem[]>(() => [
  { value: 'health', label: t('monitor.tabs.health') },
  { value: 'logs', label: t('monitor.tabs.logs') },
  { value: 'usage', label: t('monitor.tabs.usage') },
  { value: 'inspector', label: t('monitor.tabs.inspector') },
])

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
        </template>

        <template v-if="isCanonicalQuery">
          <div v-if="activeTab === 'health'" class="monitor-panel">
            <HealthTab ref="healthTab" />
          </div>
          <div v-else-if="activeTab === 'logs'" class="monitor-panel">
            <LogsTab />
          </div>
          <div v-else-if="activeTab === 'usage'" class="monitor-panel">
            <UsageTab />
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

@media (prefers-reduced-motion: reduce) {
  .monitor-refresh-icon--spinning {
    animation: none;
  }
}
</style>
