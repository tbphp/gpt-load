<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { useStableLoading } from '@/app/loading-state'
import {
  systemInfoQueryOptions,
  type DatabaseDriver,
  type SecretSource,
} from '@/app/resources/system-info'
import CopyButton from '@/components/ui/CopyButton.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import Surface from '@/components/ui/Surface.vue'

const client = useApiClient()
const { t } = useI18n()
const infoQuery = useQuery(systemInfoQueryOptions(client))
const initialLoading = useStableLoading(
  () => infoQuery.isPending.value && infoQuery.data.value === undefined,
)
const infoRefreshing = computed(
  () => infoQuery.data.value !== undefined && infoQuery.isFetching.value,
)

function sourceLabel(source: SecretSource): string {
  return t(`settings.system.sources.${source}`)
}

function databaseLabel(database: DatabaseDriver): string {
  return t(`settings.system.databases.${database}`)
}
</script>

<template>
  <section id="settings-system" class="settings-section" tabindex="-1">
    <header class="settings-section__heading">
      <h2>{{ t('settings.system.title') }}</h2>
      <p>{{ t('settings.system.description') }}</p>
    </header>

    <AsyncRefreshIndicator :active="infoRefreshing" :label="t('settings.system.loading')" />

    <SkeletonSurface
      v-if="(infoQuery.isPending.value && !infoQuery.data.value) || initialLoading"
      variant="panel"
      min-height="320px"
      :concealed="!initialLoading"
      :label="t('settings.system.loading')"
    />
    <QueryFeedback
      v-else-if="infoQuery.isError.value && !infoQuery.data.value"
      state="error"
      :message="t('settings.system.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="infoQuery.refetch()"
    />
    <template v-else-if="infoQuery.data.value">
      <QueryFeedback
        v-if="infoQuery.isError.value"
        state="stale"
        :message="t('settings.system.stale')"
        :retry-label="t('common.retry')"
        @retry="infoQuery.refetch()"
      />
      <Surface variant="sunken" :padded="false" class="settings-system__panel">
        <dl class="settings-system__definition">
          <div class="settings-system__row">
            <dt>{{ t('settings.system.version') }}</dt>
            <dd class="settings-system__mono">{{ infoQuery.data.value.version }}</dd>
          </div>

          <div class="settings-system__row">
            <dt>{{ t('settings.system.deployment') }}</dt>
            <dd class="settings-system__inline">
              <StatusBadge size="compact">{{ t('settings.system.single') }}</StatusBadge>
              <StatusBadge size="compact">{{
                databaseLabel(infoQuery.data.value.deployment.database)
              }}</StatusBadge>
              <StatusBadge size="compact">{{ t('settings.system.singleBinary') }}</StatusBadge>
            </dd>
          </div>

          <div class="settings-system__row">
            <dt>{{ t('settings.system.dataDir') }}</dt>
            <dd class="settings-system__mono">{{ infoQuery.data.value.data_dir }}</dd>
          </div>

          <div class="settings-system__row">
            <dt>{{ t('settings.system.authKey') }}</dt>
            <dd class="settings-system__inline">
              <StatusBadge size="compact">{{
                sourceLabel(infoQuery.data.value.auth_key.source)
              }}</StatusBadge>
              <span
                v-if="infoQuery.data.value.auth_key.path"
                class="settings-system__path settings-system__mono"
              >
                <span>{{ infoQuery.data.value.auth_key.path }}</span>
                <CopyButton
                  :value="infoQuery.data.value.auth_key.path"
                  :label="t('settings.system.copyPath')"
                  :success-label="t('common.copied')"
                  :failure-label="t('common.copyFailed')"
                />
              </span>
            </dd>
          </div>

          <div class="settings-system__row">
            <dt>{{ t('settings.system.encryption') }}</dt>
            <dd class="settings-system__inline">
              <StatusBadge tone="success" size="compact">{{
                t('settings.system.enabled')
              }}</StatusBadge>
              <StatusBadge size="compact">{{
                sourceLabel(infoQuery.data.value.encryption.source)
              }}</StatusBadge>
              <span
                v-if="infoQuery.data.value.encryption.path"
                class="settings-system__path settings-system__mono"
              >
                <span>{{ infoQuery.data.value.encryption.path }}</span>
                <CopyButton
                  :value="infoQuery.data.value.encryption.path"
                  :label="t('settings.system.copyPath')"
                  :success-label="t('common.copied')"
                  :failure-label="t('common.copyFailed')"
                />
              </span>
            </dd>
          </div>
        </dl>
      </Surface>
      <p class="settings-system__security-note">{{ t('settings.system.securityNote') }}</p>
    </template>
  </section>
</template>

<style scoped>
.settings-section {
  display: grid;
}

.settings-section {
  gap: var(--space-4);
  scroll-margin-top: 76px;
}

.settings-section__heading h2,
.settings-section__heading p,
.settings-system__definition,
.settings-system__definition dd,
.settings-system__security-note {
  margin: 0;
}

.settings-section__heading h2 {
  font-size: var(--text-sm);
  font-weight: 650;
}

.settings-section__heading p,
.settings-system__security-note {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}

.settings-section > .settings-system__panel {
  border-radius: var(--radius-control);
  padding: var(--space-4);
}

.settings-system__definition {
  display: grid;
}

.settings-system__row {
  display: grid;
  grid-template-columns: minmax(120px, 160px) minmax(0, 1fr);
  column-gap: var(--space-4);
}

.settings-system__definition dt,
.settings-system__definition dd {
  min-width: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 10px 0;
}

.settings-system__definition dt {
  display: flex;
  align-items: center;
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  font-weight: 650;
}

.settings-system__definition dd {
  display: flex;
  align-items: center;
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  overflow-wrap: anywhere;
}

.settings-system__row:last-child dt,
.settings-system__row:last-child dd {
  border-bottom: 0;
}

.settings-system__inline {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.settings-system__path {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-2);
}

.settings-system__path > span:first-child {
  overflow-wrap: anywhere;
}

.settings-system__mono {
  font-family: var(--font-mono);
}

@media (max-width: 760px) {
  .settings-system__row {
    grid-template-columns: 115px minmax(0, 1fr);
  }
}
</style>
