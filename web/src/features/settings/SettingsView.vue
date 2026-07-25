<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { getSettings } from '@/api/control/settings'
import { controlQueryKeys } from '@/app/query-keys'
import PageHeader from '@/components/ui/PageHeader.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'

import AppearanceSection from './AppearanceSection.vue'
import LogsMaintenanceSection from './LogsMaintenanceSection.vue'
import RequestForwardingSection from './RequestForwardingSection.vue'
import SystemInfoSection from './SystemInfoSection.vue'

const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const settingsQuery = useQuery({
  queryKey: controlQueryKeys.settings(),
  queryFn: ({ signal }) => getSettings(client, signal),
  gcTime: 0,
})

onBeforeUnmount(() => {
  queryClient.removeQueries({ queryKey: controlQueryKeys.settings(), exact: true })
})
</script>

<template>
  <section class="settings" aria-labelledby="settings-title">
    <PageHeader
      id="settings-title"
      :title="t('settings.title')"
      :description="t('settings.description')"
    />

    <AppearanceSection />

    <QueryFeedback
      v-if="settingsQuery.isPending.value"
      state="loading"
      :message="t('settings.loading')"
    />
    <QueryFeedback
      v-else-if="settingsQuery.isError.value && !settingsQuery.data.value"
      state="error"
      :message="t('settings.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="settingsQuery.refetch()"
    />
    <template v-else-if="settingsQuery.data.value">
      <QueryFeedback
        v-if="settingsQuery.isError.value"
        state="stale"
        :message="t('settings.stale')"
        :retry-label="t('common.retry')"
        @retry="settingsQuery.refetch()"
      />
      <RequestForwardingSection :settings="settingsQuery.data.value" />
      <LogsMaintenanceSection :settings="settingsQuery.data.value" />
    </template>

    <SystemInfoSection />
  </section>
</template>

<style scoped>
.settings {
  display: grid;
  min-width: 0;
  gap: var(--space-5);
}
.settings :deep(.settings-card) {
  min-width: 0;
  padding: var(--space-5);
}
@media (max-width: 640px) {
  .settings :deep(.settings-card) {
    padding: var(--space-4);
  }
}
</style>
