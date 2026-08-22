<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import type { ProxyMutation } from '@/api/control/types'
import { useStableLoading } from '@/app/loading-state'
import {
  runtimeSettingKeys,
  settingsQueryOptions,
  updateSettings,
  type RuntimeSettingKey,
} from '@/app/resources/settings'
import { controlQueryKeys } from '@/app/query-keys'
import { settingsLocation } from '@/app/route-locations'
import { useTransientFlag } from '@/app/use-transient-flag'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'
import SectionNav from '@/components/ui/SectionNav.vue'
import StickySaveBar from '@/components/ui/StickySaveBar.vue'
import { useSectionNavigation } from '@/composables/use-section-navigation'
import { formatLocalInstant } from '@/lib/format'

import GlobalHeaderRulesSection from './GlobalHeaderRulesSection.vue'
import AffinitySettingsSection from './AffinitySettingsSection.vue'
import LogsMaintenanceSection from './LogsMaintenanceSection.vue'
import RuntimeSettingsSection from './RuntimeSettingsSection.vue'
import SystemInfoSection from './SystemInfoSection.vue'
import { isValidAffinityCapacity, isValidRetention, isValidTimeout } from './settings-patch'
import { useSettingsController } from './use-settings-controller'
import {
  isCanonicalSettingsRouteQuery,
  parseSettingsSection,
  serializeSettingsRouteQuery,
  type SettingsSection,
} from './settings-route'

const client = useApiClient()
const queryClient = useQueryClient()
const route = useRoute()
const router = useRouter()
const { locale, t } = useI18n()
const settingsQuery = useQuery(settingsQueryOptions(client, locale))
const resource = computed(() => settingsQuery.data.value ?? null)
const initialLoading = useStableLoading(
  () => settingsQuery.isPending.value && settingsQuery.data.value === undefined,
)
const settingsRefreshing = computed(
  () => settingsQuery.data.value !== undefined && settingsQuery.isFetching.value,
)
const headerRulesInvalidEdits = ref(false)
const headerRulesEditorRevision = ref(0)
const discardDialogOpen = ref(false)
const proxyPending = ref(false)
const {
  value: savedFeedback,
  clear: clearSavedFeedback,
  show: showSavedFeedback,
} = useTransientFlag(1_600)
const {
  base,
  draft,
  patch,
  dirty: controllerDirty,
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
  discard: discardDraft,
  saveAll,
  checkResult,
} = useSettingsController(resource, { hasLocalEdits: headerRulesInvalidEdits })

const navItems = computed(() => [
  { id: 'settings-forwarding', label: t('settings.navigation.forwarding') },
  { id: 'settings-affinity', label: t('settings.navigation.affinity') },
  { id: 'settings-headers', label: t('settings.navigation.headers') },
  { id: 'settings-logs', label: t('settings.navigation.logs') },
  { id: 'settings-system', label: t('settings.navigation.system') },
])
const routeSection = computed(() => parseSettingsSection(route.query))
const { activeSection, selectSection } = useSectionNavigation({
  ids: computed(() => navItems.value.map(({ id }) => id)),
  initialId: sectionID(routeSection.value),
  topOffset: 76,
})
const headerRulesValid = ref(true)
const pageOperationLocked = computed(() => operationLocked.value || proxyPending.value)
const dirty = computed(() => controllerDirty.value || headerRulesInvalidEdits.value)
const valid = computed(
  () =>
    controllerValid.value &&
    (!draft.value?.overrides.has('header_rules') || headerRulesValid.value),
)
const timeoutKeys = [
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
  'validation_interval',
  'affinity_ttl',
] as const
const changedKeys = computed(() => {
  const changed = runtimeSettingKeys.filter((key) =>
    Object.prototype.hasOwnProperty.call(patch.value, key),
  ) as RuntimeSettingKey[]
  if (headerRulesInvalidEdits.value && !changed.includes('header_rules'))
    changed.push('header_rules')
  return changed
})
const invalidKeys = computed<RuntimeSettingKey[]>(() => {
  const current = draft.value
  if (!current) return []
  return runtimeSettingKeys.filter((key) => {
    if (!current.overrides.has(key)) return false
    if (timeoutKeys.includes(key as (typeof timeoutKeys)[number]))
      return !isValidTimeout(current.values[key as (typeof timeoutKeys)[number]])
    if (key === 'header_rules') return !headerRulesValid.value
    if (key === 'request_log_retention_days')
      return !isValidRetention(current.values.request_log_retention_days)
    if (key === 'affinity_capacity')
      return !isValidAffinityCapacity(current.values.affinity_capacity)
    return false
  })
})
const savedAtLabel = computed(() =>
  savedAt.value ? formatLocalInstant(savedAt.value.getTime(), locale.value) : '',
)
const saveBarError = computed(() => {
  if (pending.value || reconciling.value) return ''
  if (failed.value) return t('settings.saveFailed')
  return indeterminate.value ? t('settings.outcome.indeterminate') : ''
})
const deferredExternalUpdate = computed(
  () =>
    headerRulesInvalidEdits.value &&
    concurrent.value &&
    resource.value !== null &&
    base.value !== null &&
    resource.value.settings_etag !== base.value.settings_etag,
)

useUnsavedChanges(dirty, {
  blocked: pageOperationLocked,
  allowRouteUpdate: (to, from) => to.name === from.name,
})

watch(
  () => draft.value?.overrides.has('header_rules'),
  (hasOverride) => {
    if (!hasOverride) headerRulesInvalidEdits.value = false
  },
)

watch(savedAt, (value, previous) => {
  if (value && value !== previous) showSavedFeedback()
})

watch(dirty, (isDirty) => {
  if (isDirty) clearSavedFeedback()
})

watch(
  () => route.query,
  (query) => {
    const section = parseSettingsSection(query)
    if (!isCanonicalSettingsRouteQuery(query, section)) {
      void router.replace(settingsLocation(serializeSettingsRouteQuery(section)))
      return
    }
    void nextTick(() => selectSection(sectionID(section)))
  },
  { deep: true, immediate: true },
)

function sectionID(section: SettingsSection): string {
  return `settings-${section}`
}

function sectionFromID(id: string): SettingsSection | undefined {
  const section = id.replace(/^settings-/u, '')
  return section === 'forwarding' ||
    section === 'affinity' ||
    section === 'headers' ||
    section === 'logs' ||
    section === 'system'
    ? section
    : undefined
}

async function navigateSection(id: string): Promise<void> {
  const section = sectionFromID(id)
  if (section === undefined) return
  selectSection(id)
  if (section === routeSection.value) return
  await router.push(settingsLocation(serializeSettingsRouteQuery(section)))
}

function discard(): void {
  discardDraft()
  headerRulesInvalidEdits.value = false
  headerRulesEditorRevision.value += 1
}

function requestDiscard(): void {
  if (!dirty.value || operationLocked.value) return
  discardDialogOpen.value = true
}

function confirmDiscard(): void {
  discard()
  discardDialogOpen.value = false
}

function settingLabel(key: RuntimeSettingKey): string {
  if (key === 'affinity_enabled' || key === 'affinity_ttl' || key === 'affinity_capacity')
    return t(`settings.affinity.${key}`)
  if (key === 'request_log_retention_days') return t('settings.logs.retention')
  if (key === 'header_rules') return t('settings.headers.title')
  return t(`settings.runtime.${key}`)
}

function settingTarget(key: RuntimeSettingKey): string {
  if (key === 'header_rules') return 'settings-headers'
  return `settings-value-${key}`
}

async function focusTarget(key: RuntimeSettingKey): Promise<void> {
  const id = settingTarget(key)
  const section =
    key === 'header_rules'
      ? 'settings-headers'
      : key === 'affinity_enabled' || key === 'affinity_ttl' || key === 'affinity_capacity'
        ? 'settings-affinity'
        : key === 'request_log_retention_days'
          ? 'settings-logs'
          : 'settings-forwarding'
  await navigateSection(section)
  await nextTick()
  const target =
    key === 'header_rules'
      ? (document
          .getElementById('settings-headers')
          ?.querySelector<HTMLElement>('[aria-invalid="true"]') ??
        document.getElementById('settings-headers'))
      : document.getElementById(id)
  target?.focus()
}

async function saveGlobalProxy(value: ProxyMutation): Promise<void> {
  const current = resource.value
  if (!current || pageOperationLocked.value) throw new Error('SETTINGS_PROXY_UNAVAILABLE')

  proxyPending.value = true
  try {
    const next = await updateSettings(client, { proxy_config: value }, current.settings_etag)
    queryClient.setQueryData(controlQueryKeys.settings(locale.value), next)
    await queryClient.invalidateQueries({
      queryKey: controlQueryKeys.groups.all,
      refetchType: 'none',
    })
  } finally {
    proxyPending.value = false
  }
}

onBeforeUnmount(() => {
  queryClient.removeQueries({ queryKey: controlQueryKeys.settingsAll })
})
</script>

<template>
  <PageFrame>
    <LedgerSheet as="article" class="settings" aria-labelledby="settings-title">
      <PageHeader id="settings-title" :title="t('settings.title')" />

      <AsyncRefreshIndicator :active="settingsRefreshing" :label="t('settings.loading')" />

      <div class="settings__layout">
        <SectionNav
          :model-value="activeSection"
          :items="navItems"
          :label="t('settings.navigation.label')"
          :caption="t('settings.navigation.caption')"
          appearance="ledger"
          @update:model-value="navigateSection"
        />

        <div class="settings__content">
          <SkeletonSurface
            v-if="(settingsQuery.isPending.value && !settingsQuery.data.value) || initialLoading"
            variant="form"
            :concealed="!initialLoading"
            :label="t('settings.loading')"
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

            <section
              v-if="invalidKeys.length"
              class="settings__validation"
              role="alert"
              tabindex="-1"
            >
              <strong>{{ t('settings.validation.title') }}</strong>
              <ul>
                <li v-for="key in invalidKeys" :key="key">
                  <a :href="`#${settingTarget(key)}`" @click.prevent="focusTarget(key)">
                    {{ settingLabel(key) }}
                  </a>
                </li>
              </ul>
            </section>
            <InlineFeedback v-if="concurrent" tone="warning">
              {{
                deferredExternalUpdate
                  ? t('settings.conflict.deferred')
                  : conflicts.length
                    ? t('settings.conflict.blocked')
                    : t('settings.conflict.rebased')
              }}
            </InlineFeedback>

            <RuntimeSettingsSection
              :base="base"
              :draft="draft"
              :disabled="pageOperationLocked"
              :conflicts="conflicts"
              :proxy="resource?.settings.values.proxy_config ?? base.settings.values.proxy_config"
              :save-proxy="saveGlobalProxy"
              @change="updateDraft"
              @choose-mine="chooseMine"
              @choose-latest="chooseLatest"
            />
            <AffinitySettingsSection
              :base="base"
              :draft="draft"
              :disabled="pageOperationLocked"
              :conflicts="conflicts"
              @change="updateDraft"
              @choose-mine="chooseMine"
              @choose-latest="chooseLatest"
            />
            <GlobalHeaderRulesSection
              :base="base"
              :draft="draft"
              :disabled="pageOperationLocked"
              :conflicts="conflicts"
              :reset-key="headerRulesEditorRevision"
              @change="updateDraft"
              @choose-mine="chooseMine"
              @choose-latest="chooseLatest"
              @update:valid="headerRulesValid = $event"
              @update:invalid-edits="headerRulesInvalidEdits = $event"
            />
            <LogsMaintenanceSection
              :base="base"
              :draft="draft"
              :disabled="pageOperationLocked"
              :conflicts="conflicts"
              @change="updateDraft"
              @choose-mine="chooseMine"
              @choose-latest="chooseLatest"
            />
          </template>

          <SystemInfoSection />
        </div>
      </div>

      <AppConfirmDialog
        appearance="ledger"
        tone="danger"
        :open="discardDialogOpen"
        :title="t('settings.discardConfirm.title')"
        :description="t('settings.discardConfirm.description')"
        :close-label="t('settings.discardConfirm.close')"
        :cancel-label="t('settings.discardConfirm.cancel')"
        :confirm-label="t('settings.discardConfirm.confirm')"
        @update:open="discardDialogOpen = $event"
        @confirm="confirmDiscard"
      >
        <ul class="settings__discard-list">
          <li v-for="key in changedKeys" :key="key">{{ settingLabel(key) }}</li>
        </ul>
      </AppConfirmDialog>
      <StickySaveBar
        v-if="base && draft"
        appearance="ledger"
        always-visible
        :dirty="dirty"
        :pending="pending || proxyPending"
        :status="
          failed
            ? 'error'
            : reconciling || indeterminate
              ? 'indeterminate'
              : savedFeedback
                ? 'saved'
                : 'idle'
        "
        :error="saveBarError"
        :error-action-label="indeterminate ? t('settings.outcome.checkResult') : undefined"
        @error-action="checkResult"
      >
        <template #status>
          <div>
            <strong>
              {{
                reconciling
                  ? t('settings.saveState.reconciling')
                  : indeterminate
                    ? t('settings.saveState.indeterminate')
                    : pending
                      ? t('settings.saveState.saving')
                      : dirty
                        ? t('settings.dirtySummary', { count: changedKeys.length })
                        : savedFeedback
                          ? t('settings.saved')
                          : t('settings.saveState.baseline')
              }}
            </strong>
            <span>
              {{
                reconciling
                  ? t('settings.outcome.reconciling')
                  : indeterminate
                    ? t('settings.saveState.indeterminateNote')
                    : pending
                      ? t('settings.saveState.savingNote')
                      : dirty
                        ? changedKeys.map(settingLabel).join(', ')
                        : savedFeedback
                          ? t('settings.savedAt', { time: savedAtLabel })
                          : t('settings.saveState.baselineNote')
              }}
            </span>
          </div>
        </template>
        <template #discard="{ disabled }">
          <AppButton
            variant="ghost"
            size="sm"
            :disabled="disabled || !dirty || pageOperationLocked"
            @click="requestDiscard"
          >
            {{ t('settings.discard') }}
          </AppButton>
        </template>
        <template #save="{ disabled }">
          <AppButton
            size="sm"
            :busy="pending"
            :disabled="disabled || !dirty || !valid || pageOperationLocked"
            @click="saveAll"
          >
            {{ t('settings.save') }}
          </AppButton>
        </template>
      </StickySaveBar>
    </LedgerSheet>
  </PageFrame>
</template>

<style scoped>
.settings,
.settings__layout,
.settings__content,
.settings__validation {
  display: grid;
}

.settings {
  gap: var(--space-5);
}

.settings__layout {
  grid-template-columns: 176px minmax(0, 1fr);
  align-items: start;
  gap: 34px;
}

.settings__content {
  min-width: 0;
  gap: 28px;
}

.settings__content > :deep(.settings-section) {
  border-top: 1px solid var(--color-border-subtle);
  padding-top: 17px;
  scroll-margin-top: 76px;
}

.settings__content > :deep(.settings-section):first-child {
  border-top: 0;
  padding-top: 0;
}

.settings__validation {
  gap: var(--space-2);
  border: 1px solid var(--color-danger);
  border-radius: var(--radius-control);
  background: var(--color-danger-bg);
  padding: var(--space-3) var(--space-4);
}

.settings__validation ul {
  display: grid;
  gap: var(--space-1);
  margin: 0;
  padding-left: var(--space-5);
}

.settings__validation a {
  color: var(--color-danger);
  text-decoration: underline;
}

.settings__discard-list {
  display: grid;
  gap: var(--space-1);
  margin: 0;
  padding-left: var(--space-5);
}

.settings :deep(.sticky-save-bar--ledger) {
  margin-left: 210px;
}

@media (max-width: 860px) {
  .settings__layout {
    grid-template-columns: 1fr;
  }

  .settings :deep(.sticky-save-bar--ledger) {
    margin-left: 0;
  }
}
</style>
