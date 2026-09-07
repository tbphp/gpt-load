<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { routeStrategies } from '@/api/control/types'
import type { RuntimeSettingKey, SettingsResource } from '@/app/resources/settings'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'
import SegmentedControl, { type SegmentedControlOption } from '@/components/ui/SegmentedControl.vue'
import { formatInteger } from '@/lib/format'

import SettingRow from '@/components/config/SettingRow.vue'
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
const { locale, t } = useI18n()
const numericKeys = ['affinity_ttl', 'affinity_capacity'] as const
const routeStrategyOptions = computed<SegmentedControlOption[]>(() =>
  routeStrategies.map((value) => ({
    value,
    label: t(`settings.runtime.routeStrategies.${value}`),
    disabled: props.disabled,
  })),
)
const enabledValue = computed(() =>
  props.base.settings.values.affinity_enabled
    ? t('settings.runtime.enabled')
    : t('settings.runtime.disabled'),
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

function setRouteStrategy(value: string): void {
  const strategy = routeStrategies.find((candidate) => candidate === value)
  if (strategy === undefined) return
  const draft = cloneDraft()
  draft.values.route_strategy = strategy
  publish('route_strategy', draft)
}

function setEnabled(value: boolean): void {
  const draft = cloneDraft()
  draft.values.affinity_enabled = value
  publish('affinity_enabled', draft)
}

function setNumber(key: (typeof numericKeys)[number], value: string): void {
  const draft = cloneDraft()
  draft.values[key] = value.trim() === '' ? Number.NaN : Number(value)
  publish(key, draft)
}

function numberFieldError(key: (typeof numericKeys)[number]): string | undefined {
  if (!hasOverride(key)) return undefined
  const valid =
    key === 'affinity_ttl'
      ? isValidTimeout(props.draft.values[key])
      : isValidAffinityCapacity(props.draft.values[key])
  return valid ? undefined : t(`settings.affinity.${key}Error`)
}

function numberFieldValue(key: (typeof numericKeys)[number]): string {
  if (isPendingRestore(key)) return t('settings.runtime.resetPending')
  return t(`settings.affinity.${key}Effective`, {
    value: formatInteger(props.base.settings.values[key], locale.value),
  })
}
</script>

<template>
  <section id="settings-routing" class="settings-section" tabindex="-1">
    <header class="settings-section__heading">
      <h2>{{ t('settings.affinity.title') }}</h2>
      <p>{{ t('settings.affinity.description') }}</p>
    </header>

    <div class="settings-routing__rows">
      <SettingRow
        :label="t('settings.runtime.route_strategy')"
        :value="
          isPendingRestore('route_strategy')
            ? t('settings.runtime.resetPending')
            : t(`settings.runtime.routeStrategies.${base.settings.values.route_strategy}`)
        "
        :help="t('settings.runtime.routeStrategyHelp')"
        :source-label="sourceLabel('route_strategy')"
        :action-label="actionLabel('route_strategy')"
        :overridden="hasOverride('route_strategy')"
        :pending-restore="isPendingRestore('route_strategy')"
        :disabled="disabled"
        @toggle="toggleOverride('route_strategy')"
      >
        <template #control>
          <SegmentedControl
            :model-value="draft.values.route_strategy"
            :options="routeStrategyOptions"
            :label="t('settings.runtime.route_strategy')"
            size="compact"
            @update:model-value="setRouteStrategy"
          />
        </template>
      </SettingRow>

      <SettingRow
        :label="t('settings.affinity.affinity_enabled')"
        :value="
          isPendingRestore('affinity_enabled') ? t('settings.runtime.resetPending') : enabledValue
        "
        :help="t('settings.affinity.enabledHelp')"
        :source-label="sourceLabel('affinity_enabled')"
        :action-label="actionLabel('affinity_enabled')"
        :overridden="hasOverride('affinity_enabled')"
        :pending-restore="isPendingRestore('affinity_enabled')"
        :disabled="disabled"
        @toggle="toggleOverride('affinity_enabled')"
      >
        <template #control>
          <AppSwitch
            :model-value="draft.values.affinity_enabled"
            :disabled="disabled"
            :label="t('settings.affinity.affinity_enabled')"
            @update:model-value="setEnabled"
          />
        </template>
      </SettingRow>

      <SettingRow
        v-for="key in numericKeys"
        :key="key"
        :label="t(`settings.affinity.${key}`)"
        :value="numberFieldValue(key)"
        :source-label="sourceLabel(key)"
        :action-label="actionLabel(key)"
        :overridden="hasOverride(key)"
        :pending-restore="isPendingRestore(key)"
        :divided="key !== 'affinity_capacity'"
        :disabled="disabled"
        @toggle="toggleOverride(key)"
      >
        <template #control>
          <div class="settings-routing__input">
            <CompactFieldError :id="`settings-value-${key}`" :error="numberFieldError(key)">
              <template #default="{ invalid, describedBy }">
                <AppTextInput
                  :id="`settings-value-${key}`"
                  type="number"
                  :model-value="String(draft.values[key])"
                  :label="t('settings.runtime.valueFor', { field: t(`settings.affinity.${key}`) })"
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
      </SettingRow>
    </div>
  </section>
</template>

<style scoped>
.settings-section,
.settings-section__heading,
.settings-routing__rows {
  display: grid;
}

.settings-section {
  gap: var(--space-4);
  scroll-margin-top: 76px;
}

.settings-routing__rows {
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

.settings-routing__input {
  display: inline-grid;
  grid-template-columns: minmax(0, 112px) auto;
  align-items: center;
  gap: var(--space-2);
}
</style>
