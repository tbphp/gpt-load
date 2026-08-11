<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type {
  RuntimeSettingKey,
  SettingsResource,
  TimeoutSettingKey,
} from '@/app/resources/settings'
import RuntimeOverrideRow from '@/components/config/RuntimeOverrideRow.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'

import {
  createSettingsDraft,
  isValidTimeout,
  setSettingsOverride,
  type SettingsDraft,
} from './settings-patch'
import type { SettingsMergeConflict } from './settings-response'
import type { SettingsDraftChange } from './use-settings-controller'

const props = defineProps<{
  base: SettingsResource
  draft: SettingsDraft
  disabled: boolean
  conflicts: SettingsMergeConflict[]
}>()
const emit = defineEmits<{
  change: [change: SettingsDraftChange]
  chooseMine: [key: RuntimeSettingKey]
  chooseLatest: [key: RuntimeSettingKey]
}>()
const { t } = useI18n()

const timeoutKeys: TimeoutSettingKey[] = [
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
  'validation_interval',
]
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

function setTimeoutValue(key: TimeoutSettingKey, value: string): void {
  const draft = cloneDraft()
  draft.values[key] = Number(value)
  publish(key, draft)
}

function setInjectUsage(value: boolean): void {
  const draft = cloneDraft()
  draft.values.inject_usage_options = value
  publish('inject_usage_options', draft)
}

function setModelsDevAutoSync(value: boolean): void {
  const draft = cloneDraft()
  draft.values.models_dev_auto_sync_enabled = value
  publish('models_dev_auto_sync_enabled', draft)
}

function isReadOnly(key: RuntimeSettingKey): boolean {
  return props.draft.readOnly.has(key)
}

function timeoutError(key: TimeoutSettingKey): string | undefined {
  return hasOverride(key) && !isValidTimeout(props.draft.values[key])
    ? t('settings.runtime.timeoutError')
    : undefined
}

function conflictFor(key: RuntimeSettingKey): SettingsMergeConflict | undefined {
  return props.conflicts.find((conflict) => conflict.key === key)
}

function conflictValue(conflict: SettingsMergeConflict, side: 'mine' | 'latest'): string {
  const value = conflict[side]
  if (!value.is_override) return t('settings.runtime.defaultSource')
  if (conflict.key === 'inject_usage_options' || conflict.key === 'models_dev_auto_sync_enabled')
    return value.normalized_value ? t('settings.runtime.enabled') : t('settings.runtime.disabled')
  return `${value.normalized_value} ${t('settings.runtime.seconds')}`
}
</script>

<template>
  <section id="settings-forwarding" class="settings-section" tabindex="-1">
    <header class="settings-section__heading">
      <h2>{{ t('settings.runtime.title') }}</h2>
      <p>{{ t('settings.runtime.description') }}</p>
    </header>

    <div class="settings-runtime__rows">
      <div v-for="key in timeoutKeys" :key="key" class="settings-runtime__entry">
        <RuntimeOverrideRow
          appearance="ledger"
          :label="t(`settings.runtime.${key}`)"
          :detail="
            hasOverride(key)
              ? t('settings.runtime.overrideValue')
              : isPendingRestore(key)
                ? t('settings.runtime.resetPending')
                : t('settings.runtime.effectiveValue', { value: base.settings.values[key] })
          "
          :value-label="
            hasOverride(key) || isPendingRestore(key)
              ? undefined
              : t('settings.runtime.currentEffective')
          "
          :source-label="
            hasOverride(key)
              ? t('settings.runtime.overrideSource')
              : t('settings.runtime.defaultSource')
          "
          :action-label="
            hasOverride(key) ? t('settings.runtime.restoreDefault') : t('settings.runtime.override')
          "
          :overridden="hasOverride(key)"
          :disabled="disabled"
          @toggle="toggleOverride(key)"
        >
          <template v-if="hasOverride(key)" #value>
            <div class="settings-runtime__input">
              <CompactFieldError :id="`settings-value-${key}`" :error="timeoutError(key)">
                <template #default="{ invalid, describedBy }">
                  <AppTextInput
                    :id="`settings-value-${key}`"
                    type="number"
                    :model-value="String(draft.values[key])"
                    :label="t('settings.runtime.valueFor', { field: t(`settings.runtime.${key}`) })"
                    appearance="sunken"
                    size="compact"
                    monospace
                    min="1"
                    max="9223372036"
                    step="1"
                    inputmode="numeric"
                    :disabled="disabled"
                    :invalid="invalid"
                    :described-by="describedBy"
                    @update:model-value="setTimeoutValue(key, $event)"
                  />
                </template>
              </CompactFieldError>
              <span aria-hidden="true">{{ t('settings.runtime.seconds') }}</span>
            </div>
          </template>
        </RuntimeOverrideRow>
        <article v-if="conflictFor(key)" class="settings-runtime__conflict" role="alert">
          <strong>{{ t(`settings.runtime.${key}`) }}</strong>
          <span>
            {{ t('settings.conflict.mine') }}:
            {{ conflictValue(conflictFor(key)!, 'mine') }}
          </span>
          <span>
            {{ t('settings.conflict.latest') }}:
            {{ conflictValue(conflictFor(key)!, 'latest') }}
          </span>
          <div>
            <AppButton variant="secondary" size="compact" @click="emit('chooseMine', key)">
              {{ t('settings.conflict.useMine') }}
            </AppButton>
            <AppButton variant="ghost" size="compact" @click="emit('chooseLatest', key)">
              {{ t('settings.conflict.useLatest') }}
            </AppButton>
          </div>
        </article>
      </div>

      <div class="settings-runtime__entry">
        <RuntimeOverrideRow
          appearance="ledger"
          :label="t('settings.runtime.inject_usage_options')"
          :detail="t('settings.runtime.injectUsageHelp')"
          :source-label="
            hasOverride('inject_usage_options')
              ? t('settings.runtime.overrideSource')
              : t('settings.runtime.defaultSource')
          "
          :action-label="
            hasOverride('inject_usage_options')
              ? t('settings.runtime.restoreDefault')
              : t('settings.runtime.override')
          "
          :overridden="hasOverride('inject_usage_options')"
          :disabled="disabled"
          @toggle="toggleOverride('inject_usage_options')"
        >
          <template #value>
            <div class="settings-runtime__boolean">
              <AppSwitch
                v-if="hasOverride('inject_usage_options')"
                :model-value="draft.values.inject_usage_options"
                :disabled="disabled"
                :label="
                  t('settings.runtime.valueFor', {
                    field: t('settings.runtime.inject_usage_options'),
                  })
                "
                @update:model-value="setInjectUsage"
              />
              <strong v-else-if="isPendingRestore('inject_usage_options')">{{
                t('settings.runtime.resetPending')
              }}</strong>
              <strong v-else>{{
                base.settings.values.inject_usage_options
                  ? t('settings.runtime.enabled')
                  : t('settings.runtime.disabled')
              }}</strong>
              <small
                v-if="
                  !hasOverride('inject_usage_options') && !isPendingRestore('inject_usage_options')
                "
              >
                {{ t('settings.runtime.currentEffective') }}
              </small>
              <small>{{ t('settings.runtime.injectUsageHelp') }}</small>
            </div>
          </template>
        </RuntimeOverrideRow>
        <article
          v-if="conflictFor('inject_usage_options')"
          class="settings-runtime__conflict"
          role="alert"
        >
          <strong>{{ t('settings.runtime.inject_usage_options') }}</strong>
          <span>
            {{ t('settings.conflict.mine') }}:
            {{ conflictValue(conflictFor('inject_usage_options')!, 'mine') }}
          </span>
          <span>
            {{ t('settings.conflict.latest') }}:
            {{ conflictValue(conflictFor('inject_usage_options')!, 'latest') }}
          </span>
          <div>
            <AppButton
              variant="secondary"
              size="compact"
              @click="emit('chooseMine', 'inject_usage_options')"
            >
              {{ t('settings.conflict.useMine') }}
            </AppButton>
            <AppButton
              variant="ghost"
              size="compact"
              @click="emit('chooseLatest', 'inject_usage_options')"
            >
              {{ t('settings.conflict.useLatest') }}
            </AppButton>
          </div>
        </article>
      </div>

      <div class="settings-runtime__entry">
        <RuntimeOverrideRow
          appearance="ledger"
          :label="t('settings.runtime.models_dev_auto_sync_enabled')"
          :detail="
            isReadOnly('models_dev_auto_sync_enabled')
              ? t('settings.runtime.environmentManaged')
              : t('settings.runtime.modelsDevAutoSyncHelp')
          "
          :source-label="
            isReadOnly('models_dev_auto_sync_enabled')
              ? t('settings.runtime.environmentSource')
              : hasOverride('models_dev_auto_sync_enabled')
                ? t('settings.runtime.overrideSource')
                : t('settings.runtime.defaultSource')
          "
          :action-label="
            hasOverride('models_dev_auto_sync_enabled')
              ? t('settings.runtime.restoreDefault')
              : t('settings.runtime.override')
          "
          :overridden="hasOverride('models_dev_auto_sync_enabled')"
          :divided="false"
          :disabled="disabled || isReadOnly('models_dev_auto_sync_enabled')"
          @toggle="toggleOverride('models_dev_auto_sync_enabled')"
        >
          <template #value>
            <div class="settings-runtime__boolean">
              <AppSwitch
                v-if="hasOverride('models_dev_auto_sync_enabled')"
                :model-value="draft.values.models_dev_auto_sync_enabled"
                :disabled="disabled || isReadOnly('models_dev_auto_sync_enabled')"
                :label="
                  t('settings.runtime.valueFor', {
                    field: t('settings.runtime.models_dev_auto_sync_enabled'),
                  })
                "
                @update:model-value="setModelsDevAutoSync"
              />
              <strong v-else-if="isPendingRestore('models_dev_auto_sync_enabled')">{{
                t('settings.runtime.resetPending')
              }}</strong>
              <strong v-else>{{
                draft.values.models_dev_auto_sync_enabled
                  ? t('settings.runtime.enabled')
                  : t('settings.runtime.disabled')
              }}</strong>
              <small
                v-if="
                  !hasOverride('models_dev_auto_sync_enabled') &&
                  !isPendingRestore('models_dev_auto_sync_enabled')
                "
              >
                {{ t('settings.runtime.currentEffective') }}
              </small>
              <small v-if="!isReadOnly('models_dev_auto_sync_enabled')">
                {{ t('settings.runtime.modelsDevAutoSyncHelp') }}
              </small>
            </div>
          </template>
        </RuntimeOverrideRow>
        <article
          v-if="conflictFor('models_dev_auto_sync_enabled')"
          class="settings-runtime__conflict"
          role="alert"
        >
          <strong>{{ t('settings.runtime.models_dev_auto_sync_enabled') }}</strong>
          <span>
            {{ t('settings.conflict.mine') }}:
            {{ conflictValue(conflictFor('models_dev_auto_sync_enabled')!, 'mine') }}
          </span>
          <span>
            {{ t('settings.conflict.latest') }}:
            {{ conflictValue(conflictFor('models_dev_auto_sync_enabled')!, 'latest') }}
          </span>
          <div>
            <AppButton
              variant="secondary"
              size="compact"
              @click="emit('chooseMine', 'models_dev_auto_sync_enabled')"
            >
              {{ t('settings.conflict.useMine') }}
            </AppButton>
            <AppButton
              variant="ghost"
              size="compact"
              @click="emit('chooseLatest', 'models_dev_auto_sync_enabled')"
            >
              {{ t('settings.conflict.useLatest') }}
            </AppButton>
          </div>
        </article>
      </div>
    </div>
  </section>
</template>

<style scoped>
.settings-section,
.settings-section__heading,
.settings-runtime__rows,
.settings-runtime__entry,
.settings-runtime__conflict,
.settings-runtime__boolean {
  display: grid;
}

.settings-section {
  gap: var(--space-4);
  scroll-margin-top: 76px;
}

.settings-section__heading h2,
.settings-section__heading p {
  margin: 0;
}

.settings-section__heading h2 {
  font-size: var(--text-sm);
  font-weight: 650;
}

.settings-section__heading p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}

.settings-runtime__conflict {
  gap: var(--space-1);
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-control);
  padding: var(--space-3);
  font-size: var(--text-label-xs);
}

.settings-runtime__conflict > div {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-1);
}

.settings-runtime__input {
  display: inline-grid;
  grid-template-columns: minmax(0, 112px) auto;
  align-items: center;
  gap: var(--space-2);
}

.settings-runtime__boolean {
  justify-items: start;
  gap: var(--space-1);
}
</style>
