<script setup lang="ts">
import { RefreshCw } from '@lucide/vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { RequestCancelledError } from '@/api/errors'
import { useApiClient } from '@/api/client-context'
import { useStableLoading } from '@/app/loading-state'
import { controlQueryKeys } from '@/app/query-keys'
import {
  systemInfoQueryOptions,
  type DatabaseDriver,
  type SecretSource,
} from '@/app/resources/system-info'
import { getSystemUpdate, type ReleaseUpdateDto } from '@/app/resources/system-update'
import AppButton from '@/components/ui/AppButton.vue'
import CopyButton from '@/components/ui/CopyButton.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import Surface from '@/components/ui/Surface.vue'

const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const infoQuery = useQuery(systemInfoQueryOptions(client))
const initialLoading = useStableLoading(
  () => infoQuery.isPending.value && infoQuery.data.value === undefined,
)
const infoRefreshing = computed(
  () => infoQuery.data.value !== undefined && infoQuery.isFetching.value,
)
type UpdateCheckState =
  | { kind: 'checking' }
  | { kind: 'latest' }
  | { kind: 'available'; update: ReleaseUpdateDto }
  | { kind: 'failed' }

const updateCheck = ref<UpdateCheckState | null>(null)
const updateCheckPending = ref(false)
const updateCheckTone = computed<'info' | 'success' | 'warning' | 'danger'>(() => {
  switch (updateCheck.value?.kind) {
    case 'latest':
      return 'success'
    case 'available':
      return 'warning'
    case 'failed':
      return 'danger'
    default:
      return 'info'
  }
})

function sourceLabel(source: SecretSource): string {
  return t(`settings.system.sources.${source}`)
}

function databaseLabel(database: DatabaseDriver): string {
  return t(`settings.system.databases.${database}`)
}

async function checkForUpdate(): Promise<void> {
  if (updateCheckPending.value) return
  updateCheckPending.value = true
  updateCheck.value = { kind: 'checking' }
  try {
    const result = await getSystemUpdate(client, undefined, true)
    queryClient.setQueryData(controlQueryKeys.systemUpdate(), result)
    updateCheck.value = result.update
      ? { kind: 'available', update: result.update }
      : { kind: 'latest' }
  } catch (error) {
    if (error instanceof RequestCancelledError) {
      updateCheck.value = null
      return
    }
    // 后端失败时会清空更新结果；同步前端查询缓存，避免继续显示旧提示。
    queryClient.setQueryData(controlQueryKeys.systemUpdate(), { update: null })
    updateCheck.value = { kind: 'failed' }
  } finally {
    updateCheckPending.value = false
  }
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
            <dd class="settings-system__version">
              <span class="settings-system__mono">{{ infoQuery.data.value.version }}</span>
              <div class="settings-system__update-controls">
                <AppButton
                  variant="secondary"
                  size="compact"
                  :busy="updateCheckPending"
                  @click="checkForUpdate"
                >
                  <RefreshCw
                    :size="14"
                    aria-hidden="true"
                    :class="{ 'settings-system__refresh-icon--spinning': updateCheckPending }"
                  />
                  {{ t('settings.system.checkUpdate') }}
                </AppButton>
                <span
                  v-if="updateCheck"
                  class="settings-system__update-result"
                  :class="`settings-system__update-result--${updateCheckTone}`"
                  :role="updateCheck.kind === 'failed' ? 'alert' : 'status'"
                  :aria-live="updateCheck.kind === 'failed' ? 'assertive' : 'polite'"
                  aria-atomic="true"
                >
                  <template v-if="updateCheck.kind === 'checking'">
                    {{ t('settings.system.checkingUpdate') }}
                  </template>
                  <template v-else-if="updateCheck.kind === 'latest'">
                    {{ t('settings.system.latestVersion') }}
                  </template>
                  <template v-else-if="updateCheck.kind === 'available'">
                    {{
                      t('settings.system.updateAvailable', { version: updateCheck.update.version })
                    }}
                    <a
                      class="settings-system__update-link"
                      :href="updateCheck.update.release_url"
                      target="_blank"
                      rel="noopener noreferrer"
                    >
                      {{ t('settings.system.viewRelease') }}
                    </a>
                  </template>
                  <template v-else>
                    {{ t('settings.system.checkUpdateFailed') }}
                  </template>
                </span>
              </div>
            </dd>
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
  font-size: var(--text-body);
  font-weight: 650;
}

.settings-section__heading p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

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
  border-bottom: 1px dashed var(--color-border-subtle);
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

.settings-system__version {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
}

.settings-system__update-controls {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.settings-system__update-result {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  min-width: 0;
}

.settings-system__update-result--info {
  color: var(--color-action);
}

.settings-system__update-result--success {
  color: var(--color-success);
}

.settings-system__update-result--warning {
  color: var(--color-warning);
}

.settings-system__update-result--danger {
  color: var(--color-danger);
}

.settings-system__update-link {
  color: currentColor;
  font-weight: 650;
  text-decoration: underline;
  text-underline-offset: 2px;
}

.settings-system__refresh-icon--spinning {
  animation: settings-system-refresh-spin 900ms linear infinite;
}

@keyframes settings-system-refresh-spin {
  to {
    transform: rotate(360deg);
  }
}

@media (prefers-reduced-motion: reduce) {
  .settings-system__refresh-icon--spinning {
    animation: none;
  }
}

@media (max-width: 760px) {
  .settings-system__row {
    grid-template-columns: 115px minmax(0, 1fr);
  }
}
</style>
