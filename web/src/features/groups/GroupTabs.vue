<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { groupDetailLocation } from '@/app/route-locations'
import AppTabs, { type AppTabItem } from '@/components/ui/AppTabs.vue'

import { normalizeGroupQuery, normalizeGroupTab, type GroupTab } from './group-route'

const props = defineProps<{ credentialCount: number; modelCount: number }>()
const route = useRoute()
const router = useRouter()
const { n, t } = useI18n()
const activeTab = computed(() => normalizeGroupTab(route.query.tab))
const items = computed<AppTabItem[]>(() => [
  { value: 'credentials', label: t('group.tabs.credentials'), count: n(props.credentialCount) },
  { value: 'models', label: t('group.tabs.models'), count: n(props.modelCount) },
  { value: 'settings', label: t('group.tabs.settings') },
])

watch(
  () => route.query,
  (query) => {
    const canonical = normalizeGroupQuery(query)
    const keys = Object.keys(canonical)
    if (
      Object.keys(query).length !== keys.length ||
      keys.some((key) => query[key] !== canonical[key])
    ) {
      void router.replace(groupDetailLocation(String(route.params.id), canonical))
    }
  },
  { immediate: true },
)

function selectTab(value: string): void {
  const tab = normalizeGroupTab(value)
  if (tab === activeTab.value) return
  void router.push(groupDetailLocation(String(route.params.id), { tab }))
}
</script>

<template>
  <AppTabs
    :model-value="activeTab"
    :label="t('group.tabs.label')"
    :items="items"
    appearance="detail"
    @update:model-value="selectTab($event as GroupTab)"
    ><slot
  /></AppTabs>
</template>
