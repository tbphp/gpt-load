<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { RuntimeSettingKey, SettingsResource } from '@/app/resources/settings'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'
import { formatInteger } from '@/lib/format'

import SettingRow from '@/components/config/SettingRow.vue'
import {
  createSettingsDraft,
  isValidRetention,
  setSettingsOverride,
  type SettingsDraft,
} from './settings-patch'
import type { SettingsDraftChange } from './use-settings-controller'

const retentionKey = 'request_log_retention_days' as const
const props = defineProps<{
  base: SettingsResource
  draft: SettingsDraft
  disabled: boolean
}>()
const emit = defineEmits<{
  change: [change: SettingsDraftChange]
}>()
const { locale, t } = useI18n()
const retentionInput = ref('')
const lastPublishedRetention = ref<number | undefined>()
const retentionOwned = computed(() => props.draft.overrides.has(retentionKey))
const retentionPendingRestore = computed(
  () => !retentionOwned.value && props.base.settings.overrides.includes(retentionKey),
)
const retentionError = computed(() =>
  retentionOwned.value && !isValidRetention(props.draft.values.request_log_retention_days)
    ? t('settings.logs.retentionError')
    : undefined,
)
const retentionValue = computed(() => {
  if (retentionPendingRestore.value) return t('settings.runtime.resetPending')
  return t('settings.logs.effectiveValue', {
    value: formatInteger(props.base.settings.values.request_log_retention_days, locale.value),
  })
})

watch(
  () => props.draft.values.request_log_retention_days,
  (value) => {
    if (!Object.is(value, lastPublishedRetention.value)) retentionInput.value = String(value)
  },
  { immediate: true },
)

function cloneDraft(): SettingsDraft {
  return createSettingsDraft({
    values: props.draft.values,
    overrides: [...props.draft.overrides],
    read_only: [...props.draft.readOnly],
  })
}

function publish(key: RuntimeSettingKey, draft: SettingsDraft): void {
  emit('change', { key, draft })
}

function toggleRetentionOverride(): void {
  publish(
    retentionKey,
    setSettingsOverride(props.base.settings, props.draft, retentionKey, !retentionOwned.value),
  )
}

function setRetentionValue(value: string): void {
  retentionInput.value = value
  const draft = cloneDraft()
  const parsed = value.trim() === '' ? Number.NaN : Number(value)
  draft.values.request_log_retention_days = parsed
  lastPublishedRetention.value = parsed
  publish(retentionKey, draft)
}

function hasOverride(key: RuntimeSettingKey): boolean {
  return props.draft.overrides.has(key)
}

function isPendingRestore(key: RuntimeSettingKey): boolean {
  return !hasOverride(key) && props.base.settings.overrides.includes(key)
}

function isReadOnly(key: RuntimeSettingKey): boolean {
  return props.draft.readOnly.has(key)
}

function toggleOverride(key: RuntimeSettingKey): void {
  publish(key, setSettingsOverride(props.base.settings, props.draft, key, !hasOverride(key)))
}

const syncLocked = computed(() => isReadOnly('models_dev_auto_sync_enabled'))
const syncSourceLabel = computed(() => {
  if (syncLocked.value) return t('settings.runtime.environmentSource')
  if (hasOverride('models_dev_auto_sync_enabled')) return t('settings.runtime.overrideSource')
  if (isPendingRestore('models_dev_auto_sync_enabled'))
    return t('settings.runtime.pendingRestoreSource')
  return t('settings.runtime.defaultSource')
})
const syncActionLabel = computed(() =>
  hasOverride('models_dev_auto_sync_enabled')
    ? t('settings.runtime.restoreDefault')
    : t('settings.runtime.override'),
)
const syncValue = computed(() => {
  if (isPendingRestore('models_dev_auto_sync_enabled')) return t('settings.runtime.resetPending')
  return props.base.settings.values.models_dev_auto_sync_enabled
    ? t('settings.runtime.enabled')
    : t('settings.runtime.disabled')
})

function setModelsDevAutoSync(value: boolean): void {
  const draft = cloneDraft()
  draft.values.models_dev_auto_sync_enabled = value
  publish('models_dev_auto_sync_enabled', draft)
}
</script>

<template>
  <section id="settings-data-maintenance" class="settings-section" tabindex="-1">
    <header class="settings-section__heading">
      <h2>{{ t('settings.logs.title') }}</h2>
      <p>{{ t('settings.logs.description') }}</p>
    </header>

    <div class="settings-data-maintenance__rows">
      <SettingRow
        :label="t('settings.logs.retention')"
        :value="retentionValue"
        :source-label="
          retentionOwned
            ? t('settings.runtime.overrideSource')
            : retentionPendingRestore
              ? t('settings.runtime.pendingRestoreSource')
              : t('settings.runtime.defaultSource')
        "
        :action-label="
          retentionOwned ? t('settings.runtime.restoreDefault') : t('settings.runtime.override')
        "
        :overridden="retentionOwned"
        :pending-restore="retentionPendingRestore"
        :disabled="disabled"
        @toggle="toggleRetentionOverride"
      >
        <template #control>
          <div class="settings-data-maintenance__input">
            <CompactFieldError
              id="settings-value-request_log_retention_days"
              :error="retentionError"
            >
              <template #default="{ invalid, describedBy }">
                <AppTextInput
                  id="settings-value-request_log_retention_days"
                  type="number"
                  :model-value="retentionInput"
                  :label="t('settings.runtime.valueFor', { field: t('settings.logs.retention') })"
                  appearance="surface"
                  size="compact"
                  monospace
                  min="1"
                  max="365"
                  step="1"
                  inputmode="numeric"
                  :disabled="disabled"
                  :invalid="invalid"
                  :described-by="describedBy"
                  @update:model-value="setRetentionValue"
                />
              </template>
            </CompactFieldError>
            <span aria-hidden="true">{{ t('settings.logs.days') }}</span>
          </div>
        </template>
      </SettingRow>

      <SettingRow
        :label="t('settings.runtime.models_dev_auto_sync_enabled')"
        :value="syncValue"
        :help="
          syncLocked
            ? t('settings.runtime.environmentManaged')
            : t('settings.runtime.modelsDevAutoSyncHelp')
        "
        :source-label="syncSourceLabel"
        :action-label="syncActionLabel"
        :overridden="hasOverride('models_dev_auto_sync_enabled')"
        :pending-restore="!syncLocked && isPendingRestore('models_dev_auto_sync_enabled')"
        :locked="syncLocked"
        :divided="false"
        :disabled="disabled || syncLocked"
        @toggle="toggleOverride('models_dev_auto_sync_enabled')"
      >
        <template #control>
          <AppSwitch
            :model-value="draft.values.models_dev_auto_sync_enabled"
            :disabled="disabled || syncLocked"
            :label="t('settings.runtime.models_dev_auto_sync_enabled')"
            @update:model-value="setModelsDevAutoSync"
          />
        </template>
      </SettingRow>
    </div>
  </section>
</template>

<style scoped>
.settings-section,
.settings-section__heading,
.settings-data-maintenance__rows {
  display: grid;
}

.settings-section {
  gap: var(--space-4);
  scroll-margin-top: 76px;
}

.settings-data-maintenance__rows {
  gap: var(--space-1);
}

.settings-section__heading h2,
.settings-section__heading p {
  margin: 0;
}

.settings-section__heading h2 {
  font-size: var(--title-section);
  font-weight: 650;
}

.settings-section__heading p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.settings-data-maintenance__input {
  display: inline-grid;
  grid-template-columns: minmax(0, 112px) auto;
  align-items: center;
  gap: var(--space-2);
}
</style>
