<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ProxyConfiguredMode, ProxyViewDto } from '@/api/control/types'
import { proxyOverrideToggleMode } from '@/app/resources/proxy'
import type {
  RuntimeSettingKey,
  SettingsResource,
  TimeoutSettingKey,
} from '@/app/resources/settings'
import ProxyOverrideControl from '@/components/config/ProxyOverrideControl.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'
import { formatInteger } from '@/lib/format'

import SettingRow from '@/components/config/SettingRow.vue'
import {
  createSettingsDraft,
  isValidTimeout,
  setSettingsOverride,
  type SettingsDraft,
} from './settings-patch'
import type { SettingsDraftChange } from './use-settings-controller'

const props = defineProps<{
  base: SettingsResource
  draft: SettingsDraft
  disabled: boolean
  proxy: ProxyViewDto
  proxyMode: ProxyConfiguredMode
  proxyEndpoint: string
}>()
const emit = defineEmits<{
  change: [change: SettingsDraftChange]
  'update:proxyMode': [value: ProxyConfiguredMode]
  'update:proxyEndpoint': [value: string]
}>()
const { locale, t } = useI18n()
const timeoutKeys: TimeoutSettingKey[] = [
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
]

// 代理沿用其它设置项的覆盖语义：inherit 即“未覆盖”，direct/custom 即“显式覆盖”。
const proxyOverridden = computed(() => props.proxyMode !== 'inherit')
const proxyPendingRestore = computed(
  () => props.proxy.configured_mode !== 'inherit' && props.proxyMode === 'inherit',
)
const proxyEffectiveLabel = computed(
  () => props.proxy.display_url ?? t(`common.proxy.mode.${props.proxy.effective_mode}`),
)
const proxyValue = computed(() =>
  proxyOverridden.value
    ? t('settings.runtime.overrideValue')
    : proxyPendingRestore.value
      ? t('settings.runtime.resetPending')
      : proxyEffectiveLabel.value,
)
const proxySourceLabel = computed(() =>
  proxyOverridden.value
    ? t('settings.runtime.overrideSource')
    : proxyPendingRestore.value
      ? t('settings.runtime.pendingRestoreSource')
      : t('settings.runtime.defaultSource'),
)
const proxyActionLabel = computed(() =>
  proxyOverridden.value ? t('settings.runtime.restoreDefault') : t('settings.runtime.override'),
)

function toggleProxyOverride(): void {
  emit('update:proxyMode', proxyOverrideToggleMode(props.proxy, proxyOverridden.value))
  emit('update:proxyEndpoint', '')
}

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

function timeoutValue(key: TimeoutSettingKey): string {
  if (isPendingRestore(key)) return t('settings.runtime.resetPending')
  return t('settings.runtime.effectiveValue', {
    value: formatInteger(props.base.settings.values[key], locale.value),
  })
}

function setTimeoutValue(key: TimeoutSettingKey, value: string): void {
  const draft = cloneDraft()
  draft.values[key] = value.trim() === '' ? Number.NaN : Number(value)
  publish(key, draft)
}

function timeoutError(key: TimeoutSettingKey): string | undefined {
  return hasOverride(key) && !isValidTimeout(props.draft.values[key])
    ? t('settings.runtime.timeoutError')
    : undefined
}
</script>

<template>
  <section id="settings-connection" class="settings-section" tabindex="-1">
    <header class="settings-section__heading">
      <h2>{{ t('settings.runtime.title') }}</h2>
      <p>{{ t('settings.runtime.description') }}</p>
    </header>

    <div class="settings-connection__rows">
      <SettingRow
        :label="t('common.proxy.title')"
        :value="proxyValue"
        :source-label="proxySourceLabel"
        :action-label="proxyActionLabel"
        :overridden="proxyOverridden"
        :pending-restore="proxyPendingRestore"
        :disabled="disabled"
        @toggle="toggleProxyOverride"
      >
        <template #control>
          <ProxyOverrideControl
            :base="proxy"
            :mode="proxyMode"
            :endpoint="proxyEndpoint"
            :disabled="disabled"
            @update:mode="emit('update:proxyMode', $event)"
            @update:endpoint="emit('update:proxyEndpoint', $event)"
          />
        </template>
      </SettingRow>

      <SettingRow
        v-for="key in timeoutKeys"
        :key="key"
        :label="t(`settings.runtime.${key}`)"
        :value="timeoutValue(key)"
        :source-label="sourceLabel(key)"
        :action-label="actionLabel(key)"
        :overridden="hasOverride(key)"
        :pending-restore="isPendingRestore(key)"
        :divided="key !== 'stream_idle_timeout'"
        :disabled="disabled"
        @toggle="toggleOverride(key)"
      >
        <template #control>
          <div class="settings-connection__input">
            <CompactFieldError :id="`settings-value-${key}`" :error="timeoutError(key)">
              <template #default="{ invalid, describedBy }">
                <AppTextInput
                  :id="`settings-value-${key}`"
                  type="number"
                  :model-value="String(draft.values[key])"
                  :label="t('settings.runtime.valueFor', { field: t(`settings.runtime.${key}`) })"
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
                  @update:model-value="setTimeoutValue(key, $event)"
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
.settings-connection__rows {
  display: grid;
}

.settings-section {
  gap: var(--space-4);
  scroll-margin-top: 76px;
}

.settings-connection__rows {
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

.settings-connection__input {
  display: inline-grid;
  grid-template-columns: minmax(0, 112px) auto;
  align-items: center;
  gap: var(--space-2);
}
</style>
