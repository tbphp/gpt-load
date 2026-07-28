<script setup lang="ts">
import { FileClock } from 'lucide-vue-next'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { RuntimeSettingKey } from '@/api/control/settings'
import type { SettingsResource } from '@/app/resources/settings'
import AppButton from '@/components/ui/AppButton.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import SettingOverrideField from './SettingOverrideField.vue'
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
const conflict = computed(() => props.conflicts.find((candidate) => candidate.key === settingKey))
const owned = computed(() => props.draft.overrides.has(settingKey))
const error = computed(() =>
  owned.value && !isValidRetention(props.draft.values.request_log_retention_days)
    ? t('settings.logs.retentionError')
    : undefined,
)

function setOwned(enabled: boolean): void {
  emit('change', {
    key: settingKey,
    draft: setSettingsOverride(props.base.settings, props.draft, settingKey, enabled),
  })
}

function setValue(value: number): void {
  const draft = createSettingsDraft({
    values: props.draft.values,
    overrides: [...props.draft.overrides],
  })
  draft.values.request_log_retention_days = value
  emit('change', { key: settingKey, draft })
}
</script>

<template>
  <SurfaceCard id="settings-logs-maintenance" class="settings-card logs-maintenance" tabindex="-1">
    <header class="settings-card__heading">
      <div class="settings-card__title">
        <span class="settings-card__icon"><FileClock :size="18" aria-hidden="true" /></span>
        <div>
          <h2>{{ t('settings.logs.title') }}</h2>
          <p>{{ t('settings.logs.description') }}</p>
        </div>
      </div>
    </header>

    <section v-if="conflict" class="settings-conflict" data-test="settings-conflicts">
      <strong>{{ t('settings.logs.retention') }}</strong>
      <span>
        {{ t('settings.conflict.mine') }}:
        {{ conflict.mine.normalized_value }}
      </span>
      <span>
        {{ t('settings.conflict.latest') }}:
        {{ conflict.latest.normalized_value }}
      </span>
      <div>
        <AppButton
          :data-test="`settings-conflict-mine-${settingKey}`"
          variant="secondary"
          @click="emit('chooseMine', settingKey)"
        >
          {{ t('settings.conflict.useMine') }}
        </AppButton>
        <AppButton
          :data-test="`settings-conflict-latest-${settingKey}`"
          variant="ghost"
          @click="emit('chooseLatest', settingKey)"
        >
          {{ t('settings.conflict.useLatest') }}
        </AppButton>
      </div>
    </section>

    <SettingOverrideField
      :setting-key="settingKey"
      :label="t('settings.logs.retention')"
      :description="t('settings.logs.retentionDescription')"
      :effective-value="base.settings.values.request_log_retention_days"
      :owned="owned"
      :model-value="draft.values.request_log_retention_days"
      :error="error"
      :min="1"
      :max="365"
      :disabled="disabled"
      @update:owned="setOwned"
      @update:model-value="setValue"
    />
  </SurfaceCard>
</template>

<style scoped>
.settings-card,
.settings-card__title,
.settings-card__heading {
  display: grid;
}
.settings-card {
  gap: var(--space-4);
}
.settings-card__heading {
  gap: var(--space-4);
}
.settings-card__title {
  grid-template-columns: auto minmax(0, 1fr);
  gap: var(--space-3);
}
.settings-card__heading h2,
.settings-card__heading p {
  margin: 0;
}
.settings-card__heading h2 {
  font-size: 1rem;
}
.settings-card__heading p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
}
.settings-card__icon {
  display: inline-flex;
  width: 36px;
  height: 36px;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-control);
  background: var(--color-primary-soft);
  color: var(--color-primary);
}
.settings-conflict {
  display: grid;
  gap: var(--space-2);
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-control);
  padding: var(--space-3);
}
.settings-conflict > div {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
</style>
