<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { systemInfoQueryOptions, type SecretSource } from '@/app/resources/system-info'
import CopyButton from '@/components/ui/CopyButton.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

const client = useApiClient()
const { t } = useI18n()
const infoQuery = useQuery(systemInfoQueryOptions(client))

function sourceLabel(source: SecretSource): string {
  return t(`settings.system.sources.${source}`)
}
</script>

<template>
  <section id="settings-system" class="settings-section" tabindex="-1">
    <header class="settings-section__heading">
      <h2>{{ t('settings.system.title') }}</h2>
      <p>{{ t('settings.system.description') }}</p>
    </header>

    <QueryFeedback
      v-if="infoQuery.isPending.value"
      state="loading"
      :message="t('settings.system.loading')"
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
      <dl class="settings-system__grid">
        <div>
          <dt>{{ t('settings.system.version') }}</dt>
          <dd class="settings-system__mono">{{ infoQuery.data.value.version }}</dd>
        </div>
        <div>
          <dt>{{ t('settings.system.deployment') }}</dt>
          <dd class="settings-system__badges">
            <StatusBadge size="compact">{{ t('settings.system.single') }}</StatusBadge>
            <StatusBadge size="compact">{{ t('settings.system.sqlite') }}</StatusBadge>
            <StatusBadge size="compact">{{ t('settings.system.singleBinary') }}</StatusBadge>
          </dd>
        </div>
        <div>
          <dt>{{ t('settings.system.dataDir') }}</dt>
          <dd class="settings-system__path settings-system__mono">
            <span>{{ infoQuery.data.value.data_dir }}</span>
            <CopyButton
              :value="infoQuery.data.value.data_dir"
              :label="t('settings.system.copyPath')"
              :success-label="t('common.copied')"
              :failure-label="t('common.copyFailed')"
            />
          </dd>
        </div>
        <div>
          <dt>{{ t('settings.system.authKey') }}</dt>
          <dd class="settings-system__source">
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
        <div>
          <dt>{{ t('settings.system.encryption') }}</dt>
          <dd class="settings-system__source">
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
      <p class="settings-system__security-note">{{ t('settings.system.securityNote') }}</p>
    </template>
  </section>
</template>

<style scoped>
.settings-section,
.settings-system__grid,
.settings-system__grid > div,
.settings-system__source {
  display: grid;
}

.settings-section {
  gap: var(--space-4);
  scroll-margin-top: 76px;
}

.settings-section__heading h2,
.settings-section__heading p,
.settings-system__grid,
.settings-system__grid dd,
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

.settings-system__grid {
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-4);
}

.settings-system__grid > div {
  min-width: 0;
  gap: var(--space-2);
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-3);
}

.settings-system__grid dt {
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  font-weight: 650;
}

.settings-system__grid dd,
.settings-system__source {
  min-width: 0;
  gap: var(--space-2);
}

.settings-system__badges {
  display: flex;
  flex-wrap: wrap;
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

@media (max-width: 640px) {
  .settings-system__grid {
    grid-template-columns: 1fr;
  }
}
</style>
