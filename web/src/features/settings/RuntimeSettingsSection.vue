<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import { computed } from 'vue'

import { routeStrategies, type ProxyConfiguredMode, type ProxyViewDto } from '@/api/control/types'
import { proxyOverrideToggleMode } from '@/app/resources/proxy'
import type {
  PolicyCountSettingKey,
  RuntimeSettingKey,
  SettingsResource,
  TimeoutSettingKey,
} from '@/app/resources/settings'
import ProxyOverrideControl from '@/components/config/ProxyOverrideControl.vue'
import RuntimeOverrideRow from '@/components/config/RuntimeOverrideRow.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'

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
  proxy: ProxyViewDto
  proxyMode: ProxyConfiguredMode
  proxyEndpoint: string
}>()
const emit = defineEmits<{
  change: [change: SettingsDraftChange]
  'update:proxyMode': [value: ProxyConfiguredMode]
  'update:proxyEndpoint': [value: string]
}>()
const { t } = useI18n()
const routeStrategyOptions = computed(() =>
  routeStrategies.map((value) => ({
    value,
    label: t(`settings.runtime.routeStrategies.${value}`),
  })),
)

// 代理沿用其它设置项的覆盖语义：inherit 即“未覆盖”，direct/custom 即“显式覆盖”。
const proxyOverridden = computed(() => props.proxyMode !== 'inherit')
const proxyPendingRestore = computed(
  () => props.proxy.configured_mode !== 'inherit' && props.proxyMode === 'inherit',
)
const proxyEffectiveLabel = computed(
  () => props.proxy.display_url ?? t(`common.proxy.mode.${props.proxy.effective_mode}`),
)

function toggleProxyOverride(): void {
  emit('update:proxyMode', proxyOverrideToggleMode(props.proxy, proxyOverridden.value))
  emit('update:proxyEndpoint', '')
}

const timeoutKeys: TimeoutSettingKey[] = [
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
  'validation_interval',
]
const policyRows = [
  {
    key: 'retry_count',
    helpKey: 'retryCountHelp',
  },
  {
    key: 'blacklist_threshold',
    helpKey: 'blacklistThresholdHelp',
  },
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

function setRouteStrategy(value: string): void {
  const strategy = routeStrategies.find((candidate) => candidate === value)
  if (strategy === undefined) return
  const draft = cloneDraft()
  draft.values.route_strategy = strategy
  publish('route_strategy', draft)
}

function setPolicyCount(key: PolicyCountSettingKey, value: string): void {
  const draft = cloneDraft()
  draft.values[key] = Number(value)
  publish(key, draft)
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

function policyCountError(key: PolicyCountSettingKey): string | undefined {
  return hasOverride(key) && !isValidNonNegativeInteger(props.draft.values[key])
    ? t('settings.runtime.nonNegativeIntegerError')
    : undefined
}
</script>

<template>
  <section id="settings-forwarding" class="settings-section" tabindex="-1">
    <header class="settings-section__heading">
      <h2>{{ t('settings.runtime.title') }}</h2>
      <p>{{ t('settings.runtime.description') }}</p>
    </header>

    <div class="settings-runtime__rows">
      <div class="settings-runtime__entry">
        <RuntimeOverrideRow
          appearance="ledger"
          :label="t('settings.runtime.route_strategy')"
          :detail="t('settings.runtime.routeStrategyHelp')"
          :source-label="
            hasOverride('route_strategy')
              ? t('settings.runtime.overrideSource')
              : isPendingRestore('route_strategy')
                ? t('settings.runtime.pendingRestoreSource')
                : t('settings.runtime.defaultSource')
          "
          :action-label="
            hasOverride('route_strategy')
              ? t('settings.runtime.restoreDefault')
              : t('settings.runtime.override')
          "
          :overridden="hasOverride('route_strategy')"
          :pending-restore="isPendingRestore('route_strategy')"
          :disabled="disabled"
          @toggle="toggleOverride('route_strategy')"
        >
          <template #value>
            <div class="settings-runtime__choice">
              <AppSelect
                v-if="hasOverride('route_strategy')"
                :model-value="draft.values.route_strategy"
                :options="routeStrategyOptions"
                :label="t('settings.runtime.route_strategy')"
                :disabled="disabled"
                size="compact"
                @update:model-value="setRouteStrategy"
              />
              <strong v-else-if="isPendingRestore('route_strategy')">{{
                t('settings.runtime.resetPending')
              }}</strong>
              <strong v-else>{{
                t(`settings.runtime.routeStrategies.${base.settings.values.route_strategy}`)
              }}</strong>
              <small>{{ t('settings.runtime.routeStrategyHelp') }}</small>
            </div>
          </template>
        </RuntimeOverrideRow>
      </div>
      <div class="settings-runtime__entry">
        <RuntimeOverrideRow
          appearance="ledger"
          :label="t('common.proxy.title')"
          :detail="
            proxyOverridden
              ? t('settings.runtime.overrideValue')
              : proxyPendingRestore
                ? t('settings.runtime.resetPending')
                : proxyEffectiveLabel
          "
          :value-label="
            proxyOverridden || proxyPendingRestore
              ? undefined
              : t('settings.runtime.currentEffective')
          "
          :source-label="
            proxyOverridden
              ? t('settings.runtime.overrideSource')
              : proxyPendingRestore
                ? t('settings.runtime.pendingRestoreSource')
                : t('settings.runtime.defaultSource')
          "
          :action-label="
            proxyOverridden ? t('settings.runtime.restoreDefault') : t('settings.runtime.override')
          "
          :overridden="proxyOverridden"
          :pending-restore="proxyPendingRestore"
          :disabled="disabled"
          @toggle="toggleProxyOverride"
        >
          <template v-if="proxyOverridden" #value>
            <ProxyOverrideControl
              :base="proxy"
              :mode="proxyMode"
              :endpoint="proxyEndpoint"
              :disabled="disabled"
              @update:mode="emit('update:proxyMode', $event)"
              @update:endpoint="emit('update:proxyEndpoint', $event)"
            />
          </template>
        </RuntimeOverrideRow>
      </div>
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
              : isPendingRestore(key)
                ? t('settings.runtime.pendingRestoreSource')
                : t('settings.runtime.defaultSource')
          "
          :action-label="
            hasOverride(key) ? t('settings.runtime.restoreDefault') : t('settings.runtime.override')
          "
          :overridden="hasOverride(key)"
          :pending-restore="isPendingRestore(key)"
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
        </RuntimeOverrideRow>
      </div>

      <div v-for="policy in policyRows" :key="policy.key" class="settings-runtime__entry">
        <RuntimeOverrideRow
          appearance="ledger"
          :label="t(`settings.runtime.${policy.key}`)"
          :detail="
            hasOverride(policy.key)
              ? t('settings.runtime.overrideValue')
              : isPendingRestore(policy.key)
                ? t('settings.runtime.resetPending')
                : t('settings.runtime.effectiveCount', {
                    value: base.settings.values[policy.key],
                  })
          "
          :value-label="t(`settings.runtime.${policy.helpKey}`)"
          :source-label="
            hasOverride(policy.key)
              ? t('settings.runtime.overrideSource')
              : isPendingRestore(policy.key)
                ? t('settings.runtime.pendingRestoreSource')
                : t('settings.runtime.defaultSource')
          "
          :action-label="
            hasOverride(policy.key)
              ? t('settings.runtime.restoreDefault')
              : t('settings.runtime.override')
          "
          :overridden="hasOverride(policy.key)"
          :pending-restore="isPendingRestore(policy.key)"
          :disabled="disabled"
          @toggle="toggleOverride(policy.key)"
        >
          <template v-if="hasOverride(policy.key)" #value>
            <div class="settings-runtime__input">
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
                      t('settings.runtime.valueFor', {
                        field: t(`settings.runtime.${policy.key}`),
                      })
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
        </RuntimeOverrideRow>
      </div>

      <div class="settings-runtime__entry">
        <RuntimeOverrideRow
          appearance="ledger"
          :label="t('settings.runtime.inject_usage_options')"
          :detail="t('settings.runtime.injectUsageHelp')"
          :source-label="
            hasOverride('inject_usage_options')
              ? t('settings.runtime.overrideSource')
              : isPendingRestore('inject_usage_options')
                ? t('settings.runtime.pendingRestoreSource')
                : t('settings.runtime.defaultSource')
          "
          :action-label="
            hasOverride('inject_usage_options')
              ? t('settings.runtime.restoreDefault')
              : t('settings.runtime.override')
          "
          :overridden="hasOverride('inject_usage_options')"
          :pending-restore="isPendingRestore('inject_usage_options')"
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
                : isPendingRestore('models_dev_auto_sync_enabled')
                  ? t('settings.runtime.pendingRestoreSource')
                  : t('settings.runtime.defaultSource')
          "
          :action-label="
            hasOverride('models_dev_auto_sync_enabled')
              ? t('settings.runtime.restoreDefault')
              : t('settings.runtime.override')
          "
          :overridden="hasOverride('models_dev_auto_sync_enabled')"
          :pending-restore="
            !isReadOnly('models_dev_auto_sync_enabled') &&
            isPendingRestore('models_dev_auto_sync_enabled')
          "
          :locked="isReadOnly('models_dev_auto_sync_enabled')"
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
      </div>
    </div>
  </section>
</template>

<style scoped>
.settings-section,
.settings-section__heading,
.settings-runtime__rows,
.settings-runtime__entry,
.settings-runtime__choice,
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
  font-size: var(--text-body);
  font-weight: 650;
}

.settings-section__heading p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.settings-runtime__input {
  display: inline-grid;
  grid-template-columns: minmax(0, 112px) auto;
  align-items: center;
  gap: var(--space-2);
}

.settings-runtime__boolean,
.settings-runtime__choice {
  justify-items: start;
  gap: var(--space-1);
}
</style>
