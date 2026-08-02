<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { BadgeDollarSign, ChevronRight } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import {
  runtimeSettingKeys,
  settingsQueryOptions,
  type RuntimeSettingKey,
} from '@/app/resources/settings'
import { controlQueryKeys } from '@/app/query-keys'
import { modelPricesLocation } from '@/app/route-locations'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SectionNav from '@/components/ui/SectionNav.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'
import { useSectionNavigation } from '@/composables/use-section-navigation'
import { formatLocalInstant } from '@/lib/format'

import GlobalHeaderRulesSection from './GlobalHeaderRulesSection.vue'
import LogsMaintenanceSection from './LogsMaintenanceSection.vue'
import RuntimeSettingsSection from './RuntimeSettingsSection.vue'
import SystemInfoSection from './SystemInfoSection.vue'
import { isValidRetention, isValidTimeout } from './settings-patch'
import { useSettingsController } from './use-settings-controller'

const client = useApiClient()
const queryClient = useQueryClient()
const { locale, t } = useI18n()
const settingsQuery = useQuery(settingsQueryOptions(client, locale))
const resource = computed(() => settingsQuery.data.value ?? null)
const headerRulesInvalidEdits = ref(false)
const headerRulesEditorRevision = ref(0)
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
  { id: 'settings-headers', label: t('settings.navigation.headers') },
  { id: 'settings-logs', label: t('settings.navigation.logs') },
  { id: 'settings-model-prices', label: t('settings.navigation.modelPrices') },
  { id: 'settings-system', label: t('settings.navigation.system') },
])
const { activeSection, selectSection } = useSectionNavigation({
  ids: computed(() => navItems.value.map(({ id }) => id)),
  initialId: 'settings-forwarding',
  topOffset: 76,
})
const headerRulesValid = ref(true)
const dirty = computed(() => controllerDirty.value || headerRulesInvalidEdits.value)
const valid = computed(
  () =>
    controllerValid.value &&
    (!draft.value?.overrides.has('header_rules') || headerRulesValid.value),
)
const timeoutKeys = [
  'connect_timeout',
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
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
    return false
  })
})
const savedAtLabel = computed(() =>
  savedAt.value ? formatLocalInstant(savedAt.value.getTime(), locale.value) : '',
)

useUnsavedChanges(dirty, { blocked: operationLocked })

watch(
  () => draft.value?.overrides.has('header_rules'),
  (hasOverride) => {
    if (!hasOverride) headerRulesInvalidEdits.value = false
  },
)

function discard(): void {
  discardDraft()
  headerRulesInvalidEdits.value = false
  headerRulesEditorRevision.value += 1
}

function settingLabel(key: RuntimeSettingKey): string {
  if (key === 'request_log_retention_days') return t('settings.logs.retention')
  if (key === 'header_rules') return t('settings.headers.title')
  return t(`settings.runtime.${key}`)
}

function settingTarget(key: RuntimeSettingKey): string {
  if (key === 'header_rules') return 'settings-headers'
  if (key === 'request_log_retention_days') return 'settings-logs'
  return `settings-value-${key}`
}

async function focusTarget(id: string): Promise<void> {
  selectSection(id === 'settings-headers' || id === 'settings-logs' ? id : 'settings-forwarding')
  await nextTick()
  document.getElementById(id)?.focus()
}

onBeforeUnmount(() => {
  queryClient.removeQueries({ queryKey: controlQueryKeys.settingsAll })
})
</script>

<template>
  <PageFrame>
    <LedgerSheet as="article" class="settings" aria-labelledby="settings-title">
      <PageHeader id="settings-title" :title="t('settings.title')" />

      <div class="settings__layout">
        <SectionNav
          v-model="activeSection"
          :items="navItems"
          :label="t('settings.navigation.label')"
          :caption="t('settings.navigation.caption')"
          appearance="ledger"
          @update:model-value="selectSection"
        />

        <div class="settings__content">
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

            <section
              v-if="invalidKeys.length"
              class="settings__validation"
              role="alert"
              tabindex="-1"
            >
              <strong>{{ t('settings.validation.title') }}</strong>
              <ul>
                <li v-for="key in invalidKeys" :key="key">
                  <a
                    :href="`#${settingTarget(key)}`"
                    @click.prevent="focusTarget(settingTarget(key))"
                  >
                    {{ settingLabel(key) }}
                  </a>
                </li>
              </ul>
            </section>
            <InlineFeedback v-if="failed" tone="danger">{{
              t('settings.saveFailed')
            }}</InlineFeedback>
            <InlineFeedback v-if="reconciling" tone="info">{{
              t('settings.outcome.reconciling')
            }}</InlineFeedback>
            <div v-else-if="indeterminate" class="settings__outcome">
              <InlineFeedback tone="warning">{{
                t('settings.outcome.indeterminate')
              }}</InlineFeedback>
              <AppButton variant="secondary" size="compact" @click="checkResult">
                {{ t('settings.outcome.checkResult') }}
              </AppButton>
            </div>
            <InlineFeedback v-if="concurrent" tone="warning">
              {{
                conflicts.length ? t('settings.conflict.blocked') : t('settings.conflict.rebased')
              }}
            </InlineFeedback>

            <RuntimeSettingsSection
              :base="base"
              :draft="draft"
              :disabled="operationLocked"
              :conflicts="conflicts"
              @change="updateDraft"
              @choose-mine="chooseMine"
              @choose-latest="chooseLatest"
            />
            <GlobalHeaderRulesSection
              :base="base"
              :draft="draft"
              :disabled="operationLocked"
              :conflicts="conflicts"
              :reset-key="headerRulesEditorRevision"
              @change="updateDraft"
              @choose-mine="chooseMine"
              @choose-latest="chooseLatest"
              @update:valid="headerRulesValid = $event"
              @update:invalid-edits="headerRulesInvalidEdits = $event"
            />
            <section id="settings-logs" class="settings__section" tabindex="-1">
              <LogsMaintenanceSection
                :base="base"
                :draft="draft"
                :disabled="operationLocked"
                :conflicts="conflicts"
                @change="updateDraft"
                @choose-mine="chooseMine"
                @choose-latest="chooseLatest"
              />
            </section>
          </template>

          <section id="settings-model-prices" class="settings__section" tabindex="-1">
            <SurfaceCard class="settings-card model-prices-entry">
              <div class="model-prices-entry__copy">
                <span class="model-prices-entry__icon"
                  ><BadgeDollarSign :size="18" aria-hidden="true"
                /></span>
                <div>
                  <h2>{{ t('modelPrices.settingsEntry.title') }}</h2>
                  <p>{{ t('modelPrices.settingsEntry.description') }}</p>
                </div>
              </div>
              <RouterLink class="model-prices-entry__link" :to="modelPricesLocation()">
                {{ t('modelPrices.settingsEntry.open')
                }}<ChevronRight :size="16" aria-hidden="true" />
              </RouterLink>
            </SurfaceCard>
          </section>

          <section id="settings-system" class="settings__section" tabindex="-1">
            <SystemInfoSection />
          </section>
        </div>
      </div>

      <footer v-if="base && draft" class="settings__save" aria-live="polite">
        <div>
          <strong v-if="dirty">{{
            t('settings.dirtySummary', { count: changedKeys.length })
          }}</strong>
          <span v-if="dirty">{{ changedKeys.map(settingLabel).join(', ') }}</span>
          <time v-else-if="savedAt" :datetime="savedAt.toISOString()">{{
            t('settings.savedAt', { time: savedAtLabel })
          }}</time>
        </div>
        <div class="settings__save-actions">
          <AppButton variant="secondary" :disabled="!dirty || operationLocked" @click="discard">{{
            t('settings.discard')
          }}</AppButton>
          <AppButton
            :busy="pending"
            :disabled="!dirty || !valid || operationLocked"
            @click="saveAll"
            >{{ t('settings.save') }}</AppButton
          >
        </div>
      </footer>
    </LedgerSheet>
  </PageFrame>
</template>

<style scoped>
.settings,
.settings__layout,
.settings__content,
.settings__section,
.settings__validation,
.settings__outcome {
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

.settings__content > :deep(.settings-section),
.settings__section {
  border-top: 1px solid var(--color-border-subtle);
  padding-top: 17px;
  scroll-margin-top: 76px;
}

.settings__content > :deep(.settings-section):first-child {
  border-top: 0;
  padding-top: 0;
}

.settings__section :deep(.settings-card) {
  min-width: 0;
  padding: 0;
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

.settings__outcome {
  justify-items: start;
  gap: var(--space-2);
}

.settings__save,
.settings__save > div,
.settings__save-actions,
.model-prices-entry,
.model-prices-entry__copy,
.model-prices-entry__link {
  display: flex;
  align-items: center;
}

.settings__save {
  position: sticky;
  bottom: var(--space-3);
  min-height: 58px;
  justify-content: space-between;
  gap: var(--space-4);
  margin-left: 210px;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  box-shadow: var(--shadow-sheet);
  padding: var(--space-3) var(--space-4);
}

.settings__save > div:first-child {
  min-width: 0;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-1);
}

.settings__save span,
.settings__save time {
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}

.settings__save-actions {
  flex: 0 0 auto;
  gap: var(--space-2);
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
  background: var(--color-action-soft);
  color: var(--color-action);
}

.model-prices-entry__copy h2,
.model-prices-entry__copy p {
  margin: 0;
}

.model-prices-entry__copy h2 {
  font-size: var(--text-sm);
}

.model-prices-entry__copy p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}

.model-prices-entry__link {
  min-height: 34px;
  flex: 0 0 auto;
  justify-content: center;
  gap: var(--space-1);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-control);
  padding: var(--space-1) var(--space-3);
  font-size: var(--text-label-xs);
  font-weight: 650;
}

@media (max-width: 860px) {
  .settings__layout {
    grid-template-columns: 1fr;
  }

  .settings__save {
    margin-left: 0;
  }
}

@media (max-width: 560px) {
  .settings__save,
  .model-prices-entry {
    align-items: stretch;
    flex-direction: column;
  }

  .settings__save-actions {
    justify-content: stretch;
  }

  .settings__save-actions :deep(.app-button),
  .model-prices-entry__link {
    width: 100%;
  }
}
</style>
