<script setup lang="ts">
import { BadgeDollarSign, ChevronRight } from 'lucide-vue-next'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { getSettings } from '@/api/control/settings'
import { controlQueryKeys } from '@/app/query-keys'
import PageHeader from '@/components/ui/PageHeader.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import AppearanceSection from './AppearanceSection.vue'
import LogsMaintenanceSection from './LogsMaintenanceSection.vue'
import RequestForwardingSection from './RequestForwardingSection.vue'
import SystemInfoSection from './SystemInfoSection.vue'

const client = useApiClient()
const queryClient = useQueryClient()
const { locale, t } = useI18n()
const settingsQueryKey = computed(() => controlQueryKeys.settings(locale.value))
const settingsQuery = useQuery({
  queryKey: settingsQueryKey,
  queryFn: ({ signal }) => getSettings(client, signal),
  gcTime: 0,
})

onBeforeUnmount(() => {
  queryClient.removeQueries({ queryKey: settingsQueryKey.value, exact: true })
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
      <RequestForwardingSection :resource="settingsQuery.data.value" />
      <LogsMaintenanceSection :resource="settingsQuery.data.value" />
    </template>

    <SurfaceCard class="settings-card model-prices-entry">
      <div class="model-prices-entry__copy">
        <span class="model-prices-entry__icon">
          <BadgeDollarSign :size="18" aria-hidden="true" />
        </span>
        <div>
          <h2>{{ t('modelPrices.settingsEntry.title') }}</h2>
          <p>{{ t('modelPrices.settingsEntry.description') }}</p>
        </div>
      </div>
      <RouterLink
        class="model-prices-entry__link"
        data-test="model-prices-entry"
        to="/settings/model-prices"
      >
        {{ t('modelPrices.settingsEntry.open') }}
        <ChevronRight :size="16" aria-hidden="true" />
      </RouterLink>
    </SurfaceCard>

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
.model-prices-entry,
.model-prices-entry__copy,
.model-prices-entry__link {
  display: flex;
  align-items: center;
}
.model-prices-entry {
  justify-content: space-between;
  gap: var(--space-4);
}
.model-prices-entry__copy {
  min-width: 0;
  gap: var(--space-3);
}
.model-prices-entry__icon {
  display: inline-flex;
  width: 36px;
  height: 36px;
  flex: 0 0 36px;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-control);
  background: var(--color-primary-soft);
  color: var(--color-primary);
}
.model-prices-entry__copy h2,
.model-prices-entry__copy p {
  margin: 0;
}
.model-prices-entry__copy p {
  color: var(--color-text-muted);
}
.model-prices-entry__link {
  min-height: 44px;
  flex: 0 0 auto;
  justify-content: center;
  gap: var(--space-1);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-control);
  padding: var(--space-2) var(--space-3);
  font-weight: 650;
  cursor: pointer;
  transition:
    border-color var(--duration-fast) ease,
    color var(--duration-fast) ease,
    background-color var(--duration-fast) ease;
}
.model-prices-entry__link:hover {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
  color: var(--color-primary);
}
@media (max-width: 640px) {
  .settings :deep(.settings-card) {
    padding: var(--space-4);
  }
  .model-prices-entry {
    align-items: stretch;
    flex-direction: column;
  }
  .model-prices-entry__link {
    width: 100%;
  }
}
@media (prefers-reduced-motion: reduce) {
  .model-prices-entry__link {
    transition: none;
  }
}
</style>
