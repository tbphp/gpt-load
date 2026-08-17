<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { RuntimeSettingKey, SettingsResource } from '@/app/resources/settings'
import RuntimeOverrideRow from '@/components/config/RuntimeOverrideRow.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'

import {
  createSettingsDraft,
  isValidRetention,
  setSettingsOverride,
  type SettingsDraft,
} from './settings-patch'
import type { SettingsMergeConflict } from './settings-response'
import type { SettingsDraftChange } from './use-settings-controller'

const settingKey = 'request_log_retention_days' as const
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
const retentionInput = ref('')
const lastPublishedValue = ref<number | undefined>()
const conflict = computed(() => props.conflicts.find((candidate) => candidate.key === settingKey))
const owned = computed(() => props.draft.overrides.has(settingKey))
const pendingRestore = computed(
  () => !owned.value && props.base.settings.overrides.includes(settingKey),
)
const error = computed(() =>
  owned.value && !isValidRetention(props.draft.values.request_log_retention_days)
    ? t('settings.logs.retentionError')
    : undefined,
)

watch(
  () => props.draft.values.request_log_retention_days,
  (value) => {
    if (!Object.is(value, lastPublishedValue.value)) retentionInput.value = String(value)
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

function setOwned(enabled: boolean): void {
  emit('change', {
    key: settingKey,
    draft: setSettingsOverride(props.base.settings, props.draft, settingKey, enabled),
  })
}

function setValue(value: string): void {
  retentionInput.value = value
  const draft = cloneDraft()
  const parsed = value.trim() === '' ? Number.NaN : Number(value)
  draft.values.request_log_retention_days = parsed
  lastPublishedValue.value = parsed
  emit('change', { key: settingKey, draft })
}

function conflictValue(side: 'mine' | 'latest'): string {
  const value = conflict.value?.[side]
  if (!value?.is_override) return t('settings.runtime.defaultSource')
  return `${value.normalized_value} ${t('settings.logs.days')}`
}
</script>

<template>
  <section id="settings-logs" class="settings-section" tabindex="-1">
    <header class="settings-section__heading">
      <h2>{{ t('settings.logs.title') }}</h2>
      <p>{{ t('settings.logs.description') }}</p>
    </header>

    <RuntimeOverrideRow
      appearance="ledger"
      :label="t('settings.logs.retention')"
      :detail="
        owned
          ? t('settings.runtime.overrideValue')
          : pendingRestore
            ? t('settings.runtime.resetPending')
            : t('settings.logs.effectiveValue', {
                value: base.settings.values.request_log_retention_days,
              })
      "
      :value-label="owned || pendingRestore ? undefined : t('settings.runtime.currentEffective')"
      :source-label="
        owned ? t('settings.runtime.overrideSource') : t('settings.runtime.defaultSource')
      "
      :action-label="owned ? t('settings.runtime.restoreDefault') : t('settings.runtime.override')"
      :overridden="owned"
      :divided="false"
      :disabled="disabled"
      @toggle="setOwned(!owned)"
    >
      <template v-if="owned" #value>
        <div class="settings-logs__input">
          <CompactFieldError id="settings-value-request_log_retention_days" :error="error">
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
                @update:model-value="setValue"
              />
            </template>
          </CompactFieldError>
          <span aria-hidden="true">{{ t('settings.logs.days') }}</span>
        </div>
      </template>
    </RuntimeOverrideRow>

    <article v-if="conflict" class="settings-logs__conflict" role="alert">
      <strong>{{ t('settings.logs.retention') }}</strong>
      <span>{{ t('settings.conflict.mine') }}: {{ conflictValue('mine') }}</span>
      <span>{{ t('settings.conflict.latest') }}: {{ conflictValue('latest') }}</span>
      <div>
        <AppButton variant="secondary" size="compact" @click="emit('chooseMine', settingKey)">
          {{ t('settings.conflict.useMine') }}
        </AppButton>
        <AppButton variant="ghost" size="compact" @click="emit('chooseLatest', settingKey)">
          {{ t('settings.conflict.useLatest') }}
        </AppButton>
      </div>
    </article>
  </section>
</template>

<style scoped>
.settings-section,
.settings-section__heading,
.settings-logs__input,
.settings-logs__conflict {
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

.settings-logs__input {
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-2);
}

.settings-logs__input > span {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.settings-logs__conflict {
  gap: var(--space-1);
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-control);
  padding: var(--space-3);
  font-size: var(--text-label-xs);
}

.settings-logs__conflict > div {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-1);
}
</style>
