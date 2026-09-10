<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type {
  PolicyCountSettingKey,
  RuntimeSettingKey,
  SettingsResource,
} from '@/app/resources/settings'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'
import { formatInteger } from '@/lib/format'

import SettingRow from '@/components/config/SettingRow.vue'
import {
  createSettingsDraft,
  isValidNonNegativeInteger,
  isValidTimeout,
  setSettingsOverride,
  type SettingsDraft,
} from './settings-patch'
import type { SettingsDraftChange } from './use-settings-controller'

const props = defineProps<{
  base: SettingsResource
  draft: SettingsDraft
  disabled: boolean
}>()
const emit = defineEmits<{
  change: [change: SettingsDraftChange]
}>()
const { locale, t } = useI18n()
const policyRows = [
  { key: 'retry_count', helpKey: 'retryCountHelp' },
  { key: 'blacklist_threshold', helpKey: 'blacklistThresholdHelp' },
] as const

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

function hasOverride(key: RuntimeSettingKey): boolean {
  return props.draft.overrides.has(key)
}

function isPendingRestore(key: RuntimeSettingKey): boolean {
  return !hasOverride(key) && props.base.settings.overrides.includes(key)
}

function toggleOverride(key: RuntimeSettingKey): void {
  publish(key, setSettingsOverride(props.base.settings, props.draft, key, !hasOverride(key)))
}

function sourceLabel(key: RuntimeSettingKey): string {
  if (hasOverride(key)) return t('settings.runtime.overrideSource')
  if (isPendingRestore(key)) return t('settings.runtime.pendingRestoreSource')
  return t('settings.runtime.defaultSource')
}

function actionLabel(key: RuntimeSettingKey): string {
  return hasOverride(key) ? t('settings.runtime.restoreDefault') : t('settings.runtime.override')
}

function policyValue(key: PolicyCountSettingKey): string {
  if (isPendingRestore(key)) return t('settings.runtime.resetPending')
  return t('settings.runtime.effectiveCount', {
    value: formatInteger(props.base.settings.values[key], locale.value),
  })
}

function setPolicyCount(key: PolicyCountSettingKey, value: string): void {
  const draft = cloneDraft()
  draft.values[key] = value.trim() === '' ? Number.NaN : Number(value)
  publish(key, draft)
}

function policyCountError(key: PolicyCountSettingKey): string | undefined {
  return hasOverride(key) && !isValidNonNegativeInteger(props.draft.values[key])
    ? t('settings.runtime.nonNegativeIntegerError')
    : undefined
}

function validationIntervalValue(): string {
  if (isPendingRestore('validation_interval')) return t('settings.runtime.resetPending')
  return t('settings.runtime.effectiveValue', {
    value: formatInteger(props.base.settings.values.validation_interval, locale.value),
  })
}

function setValidationInterval(value: string): void {
  const draft = cloneDraft()
  draft.values.validation_interval = value.trim() === '' ? Number.NaN : Number(value)
  publish('validation_interval', draft)
}

function validationIntervalError(): string | undefined {
  return hasOverride('validation_interval') &&
    !isValidTimeout(props.draft.values.validation_interval)
    ? t('settings.runtime.timeoutError')
    : undefined
}
</script>

<template>
  <section id="settings-reliability" class="settings-section" tabindex="-1">
    <header class="settings-section__heading">
      <h2>{{ t('settings.reliability.title') }}</h2>
      <p>{{ t('settings.reliability.description') }}</p>
    </header>

    <div class="settings-reliability__rows">
      <SettingRow
        v-for="policy in policyRows"
        :key="policy.key"
        :label="t(`settings.runtime.${policy.key}`)"
        :value="policyValue(policy.key)"
        :help="t(`settings.runtime.${policy.helpKey}`)"
        :source-label="sourceLabel(policy.key)"
        :action-label="actionLabel(policy.key)"
        :overridden="hasOverride(policy.key)"
        :pending-restore="isPendingRestore(policy.key)"
        :disabled="disabled"
        @toggle="toggleOverride(policy.key)"
      >
        <template #control>
          <div class="settings-reliability__input">
            <CompactFieldError
              :id="`settings-value-${policy.key}`"
              :error="policyCountError(policy.key)"
            >
              <template #default="{ invalid, describedBy }">
                <AppTextInput
                  :id="`settings-value-${policy.key}`"
                  type="number"
                  :model-value="String(draft.values[policy.key])"
                  :label="
                    t('settings.runtime.valueFor', { field: t(`settings.runtime.${policy.key}`) })
                  "
                  appearance="surface"
                  size="compact"
                  monospace
                  min="0"
                  step="1"
                  inputmode="numeric"
                  :disabled="disabled"
                  :invalid="invalid"
                  :described-by="describedBy"
                  @update:model-value="setPolicyCount(policy.key, $event)"
                />
              </template>
            </CompactFieldError>
            <span aria-hidden="true">{{ t('settings.runtime.countUnit') }}</span>
          </div>
        </template>
      </SettingRow>

      <SettingRow
        :label="t('settings.runtime.validation_interval')"
        :value="validationIntervalValue()"
        :source-label="sourceLabel('validation_interval')"
        :action-label="actionLabel('validation_interval')"
        :overridden="hasOverride('validation_interval')"
        :pending-restore="isPendingRestore('validation_interval')"
        :divided="false"
        :disabled="disabled"
        @toggle="toggleOverride('validation_interval')"
      >
        <template #control>
          <div class="settings-reliability__input">
            <CompactFieldError
              id="settings-value-validation_interval"
              :error="validationIntervalError()"
            >
              <template #default="{ invalid, describedBy }">
                <AppTextInput
                  id="settings-value-validation_interval"
                  type="number"
                  :model-value="String(draft.values.validation_interval)"
                  :label="
                    t('settings.runtime.valueFor', {
                      field: t('settings.runtime.validation_interval'),
                    })
                  "
                  appearance="surface"
                  size="compact"
                  monospace
                  min="1"
                  max="9223372036"
                  step="1"
                  inputmode="numeric"
                  :disabled="disabled"
                  :invalid="invalid"
                  :described-by="describedBy"
                  @update:model-value="setValidationInterval"
                />
              </template>
            </CompactFieldError>
            <span aria-hidden="true">{{ t('settings.runtime.seconds') }}</span>
          </div>
        </template>
      </SettingRow>
    </div>
  </section>
</template>

<style scoped>
.settings-section,
.settings-section__heading,
.settings-reliability__rows {
  display: grid;
}

.settings-section {
  gap: var(--space-4);
  scroll-margin-top: 76px;
}

.settings-reliability__rows {
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

.settings-reliability__input {
  display: inline-grid;
  grid-template-columns: minmax(0, 112px) auto;
  align-items: center;
  gap: var(--space-2);
}
</style>
