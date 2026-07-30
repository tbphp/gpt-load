<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import AppTabs, { type AppTabItem } from '@/components/ui/AppTabs.vue'

import { normalizeGroupQuery, normalizeGroupTab, type GroupTab } from './group-route'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const activeTab = computed(() => normalizeGroupTab(route.query.tab))
const items = computed<AppTabItem[]>(() => [
  { value: 'keys', label: t('group.tabs.keys'), testId: 'group-tab-keys' },
  { value: 'models', label: t('group.tabs.models'), testId: 'group-tab-models' },
  { value: 'settings', label: t('group.tabs.settings'), testId: 'group-tab-settings' },
])

watch(
  () => route.query,
  (query) => {
    const canonical = normalizeGroupQuery(query)
    const canonicalKeys = Object.keys(canonical)
    const isCanonical =
      Object.keys(query).length === canonicalKeys.length &&
      canonicalKeys.every((key) => query[key] === canonical[key])
    if (!isCanonical) {
      void router.replace({
        name: 'group-detail',
        params: { id: route.params.id },
        query: canonical,
      })
    }
  },
  { immediate: true },
)

function selectTab(value: string): void {
  const tab = normalizeGroupTab(value)
  if (tab === activeTab.value) return
  void router.push({
    name: 'group-detail',
    params: { id: route.params.id },
    query: normalizeGroupQuery({ ...route.query, tab }),
  })
}
</script>

<template>
  <AppTabs
    :model-value="activeTab"
    :label="t('group.tabs.label')"
    :items="items"
    @update:model-value="selectTab($event as GroupTab)"
  >
    <slot />
  </AppTabs>
</template>
