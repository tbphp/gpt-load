<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { RuntimeSettingKey, SettingsResource } from '@/app/resources/settings'
import RuntimeOverrideRow from '@/components/config/RuntimeOverrideRow.vue'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'

import {
  createSettingsDraft,
  isValidAffinityCapacity,
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
const { t } = useI18n()
const numericKeys = ['affinity_ttl', 'affinity_capacity'] as const

function cloneDraft(): SettingsDraft {
  return createSettingsDraft({
    values: props.draft.values,
    overrides: [...props.draft.overrides],
    read_only: [...props.draft.readOnly],
  })
}

function hasOverride(key: RuntimeSettingKey): boolean {
  return props.draft.overrides.has(key)
}

function pendingRestore(key: RuntimeSettingKey): boolean {
  return !hasOverride(key) && props.base.settings.overrides.includes(key)
}

function toggleOverride(key: RuntimeSettingKey): void {
  emit('change', {
    key,
    draft: setSettingsOverride(props.base.settings, props.draft, key, !hasOverride(key)),
  })
}

function setEnabled(value: boolean): void {
  const draft = cloneDraft()
  draft.values.affinity_enabled = value
  emit('change', { key: 'affinity_enabled', draft })
}

function setNumber(key: (typeof numericKeys)[number], value: string): void {
  const draft = cloneDraft()
  draft.values[key] = value.trim() === '' ? Number.NaN : Number(value)
  emit('change', { key, draft })
}

function fieldError(key: (typeof numericKeys)[number]): string | undefined {
  if (!hasOverride(key)) return undefined
  const valid =
    key === 'affinity_ttl'
      ? isValidTimeout(props.draft.values[key])
      : isValidAffinityCapacity(props.draft.values[key])
  return valid ? undefined : t(`settings.affinity.${key}Error`)
}

const enabledValue = computed(() =>
  props.draft.values.affinity_enabled
    ? t('settings.runtime.enabled')
    : t('settings.runtime.disabled'),
)
</script>

<template>
  <section id="settings-affinity" class="settings-section" tabindex="-1">
    <header class="settings-section__heading">
      <h2>{{ t('settings.affinity.title') }}</h2>
      <p>{{ t('settings.affinity.description') }}</p>
    </header>

    <div class="settings-affinity__rows">
      <div class="settings-affinity__entry">
        <RuntimeOverrideRow
          appearance="ledger"
          :label="t('settings.affinity.affinity_enabled')"
          :detail="t('settings.affinity.enabledHelp')"
          :source-label="
            hasOverride('affinity_enabled')
              ? t('settings.runtime.overrideSource')
              : pendingRestore('affinity_enabled')
                ? t('settings.runtime.pendingRestoreSource')
                : t('settings.runtime.defaultSource')
          "
          :action-label="
            hasOverride('affinity_enabled')
              ? t('settings.runtime.restoreDefault')
              : t('settings.runtime.override')
          "
          :overridden="hasOverride('affinity_enabled')"
          :pending-restore="pendingRestore('affinity_enabled')"
          :disabled="disabled"
          @toggle="toggleOverride('affinity_enabled')"
        >
          <template #value>
            <div class="settings-affinity__boolean">
              <AppSwitch
                v-if="hasOverride('affinity_enabled')"
                :model-value="draft.values.affinity_enabled"
                :disabled="disabled"
                :label="t('settings.affinity.affinity_enabled')"
                @update:model-value="setEnabled"
              />
              <strong v-else-if="pendingRestore('affinity_enabled')">{{
                t('settings.runtime.resetPending')
              }}</strong>
              <strong v-else>{{ enabledValue }}</strong>
              <small>{{ t('settings.affinity.enabledHelp') }}</small>
            </div>
          </template>
        </RuntimeOverrideRow>
      </div>

      <div v-for="key in numericKeys" :key="key" class="settings-affinity__entry">
        <RuntimeOverrideRow
          appearance="ledger"
          :label="t(`settings.affinity.${key}`)"
          :detail="
            hasOverride(key)
              ? t('settings.runtime.overrideValue')
              : pendingRestore(key)
                ? t('settings.runtime.resetPending')
                : t(`settings.affinity.${key}Effective`, { value: base.settings.values[key] })
          "
          :value-label="
            hasOverride(key) || pendingRestore(key)
              ? undefined
              : t('settings.runtime.currentEffective')
          "
          :source-label="
            hasOverride(key)
              ? t('settings.runtime.overrideSource')
              : pendingRestore(key)
                ? t('settings.runtime.pendingRestoreSource')
                : t('settings.runtime.defaultSource')
          "
          :action-label="
            hasOverride(key) ? t('settings.runtime.restoreDefault') : t('settings.runtime.override')
          "
          :overridden="hasOverride(key)"
          :pending-restore="pendingRestore(key)"
          :divided="key !== 'affinity_capacity'"
          :disabled="disabled"
          @toggle="toggleOverride(key)"
        >
          <template v-if="hasOverride(key)" #value>
            <div class="settings-affinity__input">
              <CompactFieldError :id="`settings-value-${key}`" :error="fieldError(key)">
                <template #default="{ invalid, describedBy }">
                  <AppTextInput
                    :id="`settings-value-${key}`"
                    type="number"
                    :model-value="String(draft.values[key])"
                    :label="
                      t('settings.runtime.valueFor', { field: t(`settings.affinity.${key}`) })
                    "
                    appearance="surface"
                    size="compact"
                    monospace
                    min="1"
                    :max="key === 'affinity_ttl' ? '9223372036' : '1000000'"
                    step="1"
                    inputmode="numeric"
                    :disabled="disabled"
                    :invalid="invalid"
                    :described-by="describedBy"
                    @update:model-value="setNumber(key, $event)"
                  />
                </template>
              </CompactFieldError>
              <span aria-hidden="true">
                {{
                  key === 'affinity_ttl'
                    ? t('settings.runtime.seconds')
                    : t('settings.affinity.entries')
                }}
              </span>
            </div>
          </template>
        </RuntimeOverrideRow>
      </div>
    </div>
  </section>
</template>

<style scoped>
.settings-section,
.settings-section__heading,
.settings-affinity__rows,
.settings-affinity__boolean {
  display: grid;
}

.settings-section {
  gap: var(--space-4);
}

.settings-section__heading h2,
.settings-section__heading p {
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

.settings-affinity__entry {
  min-width: 0;
}

.settings-affinity__boolean {
  gap: 2px;
}

.settings-affinity__input {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-2);
}

.settings-affinity__input > span {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
</style>
