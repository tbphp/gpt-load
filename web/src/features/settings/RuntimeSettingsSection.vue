<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type {
  RuntimeSettingKey,
  SettingsResource,
  TimeoutSettingKey,
} from '@/app/resources/settings'
import RuntimeOverrideRow from '@/components/config/RuntimeOverrideRow.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppSwitch from '@/components/ui/AppSwitch.vue'

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
  'connect_timeout',
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
]
const runtimeKeys: RuntimeSettingKey[] = [...timeoutKeys, 'inject_usage_options']
const relevantConflicts = computed(() =>
  props.conflicts.filter((conflict) => runtimeKeys.includes(conflict.key)),
)

function cloneDraft(): SettingsDraft {
  return createSettingsDraft({ values: props.draft.values, overrides: [...props.draft.overrides] })
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

function setTimeoutValue(key: TimeoutSettingKey, event: Event): void {
  const draft = cloneDraft()
  draft.values[key] = Number((event.target as HTMLInputElement).value)
  publish(key, draft)
}

function setInjectUsage(value: boolean): void {
  const draft = cloneDraft()
  draft.values.inject_usage_options = value
  publish('inject_usage_options', draft)
}

function timeoutError(key: TimeoutSettingKey): string | undefined {
  return hasOverride(key) && !isValidTimeout(props.draft.values[key])
    ? t('settings.runtime.timeoutError')
    : undefined
}

function conflictValue(conflict: SettingsMergeConflict, side: 'mine' | 'latest'): string {
  const value = conflict[side]
  if (!value.is_override) return t('settings.runtime.defaultSource')
  if (conflict.key === 'inject_usage_options')
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

    <div v-if="relevantConflicts.length" class="settings-section__conflicts" role="alert">
      <article v-for="conflict in relevantConflicts" :key="conflict.key">
        <strong>{{ t(`settings.runtime.${conflict.key}`) }}</strong>
        <span>{{ t('settings.conflict.mine') }}: {{ conflictValue(conflict, 'mine') }}</span>
        <span>{{ t('settings.conflict.latest') }}: {{ conflictValue(conflict, 'latest') }}</span>
        <div>
          <AppButton variant="secondary" size="compact" @click="emit('chooseMine', conflict.key)">
            {{ t('settings.conflict.useMine') }}
          </AppButton>
          <AppButton variant="ghost" size="compact" @click="emit('chooseLatest', conflict.key)">
            {{ t('settings.conflict.useLatest') }}
          </AppButton>
        </div>
      </article>
    </div>

    <div class="settings-runtime__rows">
      <RuntimeOverrideRow
        v-for="key in timeoutKeys"
        :key="key"
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
          <label class="settings-runtime__input">
            <span class="sr-only">{{
              t('settings.runtime.valueFor', { field: t(`settings.runtime.${key}`) })
            }}</span>
            <input
              :id="`settings-value-${key}`"
              type="number"
              min="1"
              max="9223372036"
              step="1"
              inputmode="numeric"
              :value="draft.values[key]"
              :disabled="disabled"
              :aria-invalid="Boolean(timeoutError(key)) || undefined"
              :aria-describedby="timeoutError(key) ? `settings-error-${key}` : undefined"
              @input="setTimeoutValue(key, $event)"
            />
            <span aria-hidden="true">{{ t('settings.runtime.seconds') }}</span>
          </label>
          <small v-if="timeoutError(key)" :id="`settings-error-${key}`" role="alert">
            {{ timeoutError(key) }}
          </small>
        </template>
      </RuntimeOverrideRow>

      <RuntimeOverrideRow
        appearance="ledger"
        :label="t('settings.runtime.inject_usage_options')"
        :detail="t('settings.runtime.injectUsageHelp')"
        :value-label="
          hasOverride('inject_usage_options') || isPendingRestore('inject_usage_options')
            ? undefined
            : t('settings.runtime.currentEffective')
        "
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
          <AppSwitch
            v-if="hasOverride('inject_usage_options')"
            :model-value="draft.values.inject_usage_options"
            :disabled="disabled"
            :label="
              t('settings.runtime.valueFor', { field: t('settings.runtime.inject_usage_options') })
            "
            @update:model-value="setInjectUsage"
          />
          <template v-else>
            <strong v-if="isPendingRestore('inject_usage_options')">{{
              t('settings.runtime.resetPending')
            }}</strong>
            <template v-else>
              <strong>{{
                base.settings.values.inject_usage_options
                  ? t('settings.runtime.enabled')
                  : t('settings.runtime.disabled')
              }}</strong>
              <small>{{ t('settings.runtime.currentEffective') }}</small>
            </template>
          </template>
        </template>
      </RuntimeOverrideRow>
    </div>
  </section>
</template>

<style scoped>
.settings-section,
.settings-section__heading,
.settings-section__conflicts,
.settings-section__conflicts article,
.settings-runtime__rows {
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

.settings-section__conflicts {
  gap: var(--space-2);
}

.settings-section__conflicts article {
  gap: var(--space-1);
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-control);
  padding: var(--space-3);
  font-size: var(--text-label-xs);
}

.settings-section__conflicts article > div {
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

.settings-runtime__input input {
  width: 100%;
  min-height: 32px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text);
  padding: var(--space-1) var(--space-2);
  font: inherit;
  font-family: var(--font-mono);
}

.settings-runtime__input input:focus-visible {
  outline: 2px solid var(--color-focus-ring);
  outline-offset: 1px;
}

.settings-runtime__input input[aria-invalid='true'] {
  border-color: var(--color-danger);
}
</style>
