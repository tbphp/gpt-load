<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { Info, LockKeyhole } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { getSystemInfo, type SecretSource } from '@/app/resources/system-info'
import { controlQueryKeys } from '@/app/query-keys'
import CopyButton from '@/components/ui/CopyButton.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

const client = useApiClient()
const { t } = useI18n()
const infoQuery = useQuery({
  queryKey: controlQueryKeys.systemInfo(),
  queryFn: ({ signal }) => getSystemInfo(client, signal),
})

function sourceLabel(source: SecretSource): string {
  return t(`settings.system.sources.${source}`)
}
</script>

<template>
  <SurfaceCard class="settings-card system-info">
    <header class="settings-card__heading">
      <span class="settings-card__icon"><Info :size="18" aria-hidden="true" /></span>
      <div>
        <h2>{{ t('settings.system.title') }}</h2>
        <p>{{ t('settings.system.description') }}</p>
      </div>
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
      <dl class="system-info__grid">
        <div>
          <dt>{{ t('settings.system.version') }}</dt>
          <dd class="mono">{{ infoQuery.data.value.version }}</dd>
        </div>
        <div>
          <dt>{{ t('settings.system.deployment') }}</dt>
          <dd class="system-info__badges">
            <StatusBadge>{{ t('settings.system.single') }}</StatusBadge>
            <StatusBadge>{{ t('settings.system.sqlite') }}</StatusBadge>
            <StatusBadge>{{ t('settings.system.singleBinary') }}</StatusBadge>
          </dd>
        </div>
        <div>
          <dt>{{ t('settings.system.dataDir') }}</dt>
          <dd class="system-info__path mono">
            <span>{{ infoQuery.data.value.data_dir }}</span>
            <CopyButton
              data-test="copy-data-dir"
              :value="infoQuery.data.value.data_dir"
              :label="t('settings.system.copyPath')"
              :success-label="t('common.copied')"
              :failure-label="t('common.copyFailed')"
            />
          </dd>
        </div>
        <div>
          <dt>{{ t('settings.system.authKey') }}</dt>
          <dd>
            <span>{{ sourceLabel(infoQuery.data.value.auth_key.source) }}</span>
            <span v-if="infoQuery.data.value.auth_key.path" class="system-info__path mono">
              <span>{{ infoQuery.data.value.auth_key.path }}</span>
              <CopyButton
                data-test="copy-auth-key-path"
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
          <dd>
            <span class="system-info__enabled">
              <LockKeyhole :size="16" aria-hidden="true" />{{ t('settings.system.enabled') }}
            </span>
            <span>{{ sourceLabel(infoQuery.data.value.encryption.source) }}</span>
            <span v-if="infoQuery.data.value.encryption.path" class="system-info__path mono">
              <span>{{ infoQuery.data.value.encryption.path }}</span>
              <CopyButton
                data-test="copy-encryption-path"
                :value="infoQuery.data.value.encryption.path"
                :label="t('settings.system.copyPath')"
                :success-label="t('common.copied')"
                :failure-label="t('common.copyFailed')"
              />
            </span>
          </dd>
        </div>
      </dl>
      <p class="system-info__security-note">{{ t('settings.system.securityNote') }}</p>
    </template>
  </SurfaceCard>
</template>

<style scoped>
.settings-card,
.settings-card__heading,
.system-info__grid,
.system-info__grid > div,
.system-info__grid dd {
  display: grid;
}
.settings-card {
  gap: var(--space-4);
}
.settings-card__heading {
  grid-template-columns: auto minmax(0, 1fr);
  gap: var(--space-3);
}
.settings-card__heading h2,
.settings-card__heading p,
.system-info__grid,
.system-info__grid dd,
.system-info__security-note {
  margin: 0;
}
.settings-card__heading h2 {
  font-size: 1rem;
}
.settings-card__heading p,
.system-info__security-note {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
}
.settings-card__icon {
  display: inline-flex;
  width: 36px;
  height: 36px;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-control);
  background: var(--color-primary-soft);
  color: var(--color-primary);
}
.system-info__grid {
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-4);
}
.system-info__grid > div {
  min-width: 0;
  gap: var(--space-2);
  border-top: 1px solid var(--color-border);
  padding-top: var(--space-3);
}
.system-info__grid dt {
  color: var(--color-text-muted);
  font-size: 0.8125rem;
  font-weight: 650;
}
.system-info__grid dd {
  min-width: 0;
  gap: var(--space-2);
}
.system-info__badges {
  display: flex !important;
  flex-wrap: wrap;
}
.system-info__path {
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-2);
}
.system-info__path > span:first-child {
  overflow-wrap: anywhere;
}
.system-info__enabled {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-success);
  font-weight: 650;
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}
.system-info__security-note {
  font-size: 0.8125rem;
}
@media (max-width: 640px) {
  .system-info__grid {
    grid-template-columns: 1fr;
  }
}
</style>
