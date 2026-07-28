<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { BadgeDollarSign, ChevronRight } from 'lucide-vue-next'
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import {
  getSettings,
  runtimeSettingKeys,
  type RuntimeSettingKey,
  type TimeoutSettingKey,
} from '@/api/control/settings'
import { controlQueryKeys } from '@/app/query-keys'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import AppearanceSection from './AppearanceSection.vue'
import LogsMaintenanceSection from './LogsMaintenanceSection.vue'
import RequestForwardingSection from './RequestForwardingSection.vue'
import { hasDuplicateHeaderNames, isValidRetention, isValidTimeout } from './settings-patch'
import SystemInfoSection from './SystemInfoSection.vue'
import { useSettingsController } from './use-settings-controller'

const client = useApiClient()
const queryClient = useQueryClient()
const { locale, t } = useI18n()
const settingsQueryKey = computed(() => controlQueryKeys.settings(locale.value))
const settingsQuery = useQuery({
  queryKey: settingsQueryKey,
  queryFn: ({ signal }) => getSettings(client, signal),
  gcTime: 0,
})
const resource = computed(() => settingsQuery.data.value ?? null)
const {
  base,
  draft,
  patch,
  dirty,
  valid: controllerValid,
  pending,
  failed,
  indeterminate,
  reconciling,
  concurrent,
  operationLocked,
  conflicts,
  savedAt,
  updateDraft,
  chooseMine,
  chooseLatest,
  discard,
  saveAll,
  checkResult,
} = useSettingsController(resource)
const headerRulesValid = ref(true)
const valid = computed(
  () =>
    controllerValid.value &&
    (!draft.value?.overrides.has('header_rules') || headerRulesValid.value),
)
const timeoutKeys: TimeoutSettingKey[] = [
  'connect_timeout',
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
]
const changedKeys = computed(
  () =>
    runtimeSettingKeys.filter((key) =>
      Object.prototype.hasOwnProperty.call(patch.value, key),
    ) as RuntimeSettingKey[],
)
const invalidKeys = computed<RuntimeSettingKey[]>(() => {
  const current = draft.value
  if (!current) return []
  return runtimeSettingKeys.filter((key) => {
    if (!current.overrides.has(key)) return false
    if (timeoutKeys.some((timeoutKey) => timeoutKey === key)) {
      return !isValidTimeout(current.values[key as TimeoutSettingKey])
    }
    if (key === 'header_rules') {
      return !headerRulesValid.value || hasDuplicateHeaderNames(current.values.header_rules)
    }
    if (key === 'request_log_retention_days') {
      return !isValidRetention(current.values.request_log_retention_days)
    }
    return false
  })
})
const savedAtLabel = computed(() =>
  savedAt.value
    ? new Intl.DateTimeFormat(locale.value, {
        dateStyle: 'medium',
        timeStyle: 'short',
      }).format(savedAt.value)
    : '',
)

useUnsavedChanges(dirty, { blocked: operationLocked })

function settingLabel(key: RuntimeSettingKey): string {
  if (key === 'request_log_retention_days') return t('settings.logs.retention')
  if (key === 'header_rules') return t('settings.request.headerRules')
  if (key === 'inject_usage_options') return key
  return t(`settings.request.${key}`)
}

function settingTarget(key: RuntimeSettingKey): string {
  return key === 'header_rules' ? 'settings-header-rules' : `settings-value-${key}`
}

async function focusTarget(id: string): Promise<void> {
  await nextTick()
  document.getElementById(id)?.focus()
}

onBeforeUnmount(() => {
  queryClient.removeQueries({ queryKey: ['control', 'settings'] })
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
    <template v-else-if="base && draft">
      <QueryFeedback
        v-if="settingsQuery.isError.value"
        state="stale"
        :message="t('settings.stale')"
        :retry-label="t('common.retry')"
        @retry="settingsQuery.refetch()"
      />

      <nav class="settings-navigation" :aria-label="t('settings.navigation.label')">
        <a
          href="#settings-request-forwarding"
          data-test="settings-nav-request"
          @click.prevent="focusTarget('settings-request-forwarding')"
        >
          {{ t('settings.navigation.request') }}
        </a>
        <a
          href="#settings-logs-maintenance"
          data-test="settings-nav-logs"
          @click.prevent="focusTarget('settings-logs-maintenance')"
        >
          {{ t('settings.navigation.logs') }}
        </a>
      </nav>

      <section
        v-if="dirty"
        class="settings-dirty"
        data-test="settings-dirty-summary"
        aria-live="polite"
      >
        <strong>{{ t('settings.dirtySummary', { count: changedKeys.length }) }}</strong>
        <span>{{ changedKeys.map(settingLabel).join(', ') }}</span>
      </section>

      <section
        v-if="invalidKeys.length > 0"
        class="settings-validation"
        data-test="settings-validation-summary"
        role="alert"
        tabindex="-1"
      >
        <strong>{{ t('settings.validation.title') }}</strong>
        <ul>
          <li v-for="key in invalidKeys" :key="key">
            <a
              :href="`#${settingTarget(key)}`"
              :data-test="`settings-error-link-${key}`"
              @click.prevent="focusTarget(settingTarget(key))"
            >
              {{ settingLabel(key) }}
            </a>
          </li>
        </ul>
      </section>

      <InlineFeedback v-if="failed" tone="danger">{{ t('settings.saveFailed') }}</InlineFeedback>
      <InlineFeedback v-if="reconciling" data-test="settings-reconciling" tone="info">
        {{ t('settings.outcome.reconciling') }}
      </InlineFeedback>
      <div v-else-if="indeterminate" data-test="settings-indeterminate">
        <InlineFeedback tone="warning">{{ t('settings.outcome.indeterminate') }}</InlineFeedback>
        <AppButton data-test="settings-check-result" variant="secondary" @click="checkResult">
          {{ t('settings.outcome.checkResult') }}
        </AppButton>
      </div>
      <InlineFeedback v-if="concurrent" tone="warning">
        {{ conflicts.length > 0 ? t('settings.conflict.blocked') : t('settings.conflict.rebased') }}
      </InlineFeedback>
      <p v-if="savedAt" class="settings-saved" aria-live="polite">
        <time data-test="settings-saved-at" :datetime="savedAt.toISOString()">
          {{ t('settings.savedAt', { time: savedAtLabel }) }}
        </time>
      </p>

      <div class="settings-actions">
        <AppButton
          data-test="settings-discard"
          variant="secondary"
          :disabled="!dirty || operationLocked"
          @click="discard"
        >
          {{ t('settings.discard') }}
        </AppButton>
        <AppButton
          data-test="settings-save-all"
          :busy="pending"
          :disabled="!dirty || !valid || operationLocked"
          @click="saveAll"
        >
          {{ t('settings.save') }}
        </AppButton>
      </div>

      <RequestForwardingSection
        :base="base"
        :draft="draft"
        :disabled="operationLocked"
        :conflicts="conflicts"
        @change="updateDraft"
        @choose-mine="chooseMine"
        @choose-latest="chooseLatest"
        @update:header-rules-valid="headerRulesValid = $event"
      />
      <LogsMaintenanceSection
        :base="base"
        :draft="draft"
        :disabled="operationLocked"
        :conflicts="conflicts"
        @change="updateDraft"
        @choose-mine="chooseMine"
        @choose-latest="chooseLatest"
      />
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
.settings-navigation,
.settings-actions,
.model-prices-entry,
.model-prices-entry__copy,
.model-prices-entry__link {
  display: flex;
  align-items: center;
}
.settings-navigation {
  flex-wrap: wrap;
  gap: var(--space-2);
}
.settings-navigation a {
  min-height: 44px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  padding: var(--space-2) var(--space-3);
  font-weight: 650;
}
.settings-actions {
  justify-content: flex-end;
  gap: var(--space-2);
}
.settings-dirty,
.settings-validation {
  display: grid;
  gap: var(--space-2);
  border-radius: var(--radius-control);
  padding: var(--space-3) var(--space-4);
}
.settings-dirty {
  border: 1px solid var(--color-border-strong);
  background: var(--color-surface-secondary);
}
.settings-dirty span {
  color: var(--color-text-muted);
}
.settings-validation {
  border: 1px solid var(--color-danger);
  background: var(--color-danger-bg);
}
.settings-validation ul {
  display: grid;
  gap: var(--space-1);
  margin: 0;
  padding-left: var(--space-5);
}
.settings-validation a {
  color: var(--color-danger);
  text-decoration: underline;
}
.settings-saved {
  margin: 0;
  color: var(--color-success);
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
  .settings-actions,
  .model-prices-entry {
    align-items: stretch;
    flex-direction: column;
  }
  .settings-actions :deep(.app-button),
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
