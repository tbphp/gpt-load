<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { SettingsResource } from '@/app/resources/settings'
import SettingRow from '@/components/config/SettingRow.vue'
import { createSettingsDraft, setSettingsOverride, type SettingsDraft } from './settings-patch'
import type { SettingsDraftChange } from './use-settings-controller'

const props = defineProps<{ base: SettingsResource; draft: SettingsDraft; disabled: boolean }>()
const emit = defineEmits<{ change: [change: SettingsDraftChange] }>()
const { t } = useI18n()
function update(value: string): void {
  const draft = createSettingsDraft({
    values: props.draft.values,
    overrides: [...props.draft.overrides],
    read_only: [...props.draft.readOnly],
  })
  draft.values.contact_info = value
  emit('change', { key: 'contact_info', draft })
}
function toggle(): void {
  emit('change', {
    key: 'contact_info',
    draft: setSettingsOverride(
      props.base.settings,
      props.draft,
      'contact_info',
      !props.draft.overrides.has('contact_info'),
    ),
  })
}
</script>

<template>
  <SettingRow
    :label="t('settings.contactInfo')"
    :value="base.settings.values.contact_info || '—'"
    :source-label="
      t(
        draft.overrides.has('contact_info')
          ? 'settings.runtime.overrideSource'
          : 'settings.runtime.defaultSource',
      )
    "
    :action-label="
      t(
        draft.overrides.has('contact_info')
          ? 'settings.runtime.restoreDefault'
          : 'settings.runtime.override',
      )
    "
    :overridden="draft.overrides.has('contact_info')"
    :pending-restore="
      !draft.overrides.has('contact_info') && base.settings.overrides.includes('contact_info')
    "
    :disabled="disabled"
    @toggle="toggle"
  >
    <template #control>
      <textarea
        id="settings-value-contact_info"
        class="contact-setting-input"
        :value="draft.values.contact_info"
        :aria-label="t('settings.contactInfo')"
        :placeholder="t('settings.contactInfoHelp')"
        :disabled="disabled"
        maxlength="500"
        rows="3"
        @input="update(($event.target as HTMLTextAreaElement).value)"
      />
    </template>
  </SettingRow>
</template>

<style scoped>
.contact-setting-input {
  width: 100%;
  min-width: 0;
  resize: vertical;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  padding: 8px 10px;
  background: var(--color-surface);
  color: var(--color-text);
  font: inherit;
  font-size: var(--text-sm);
}
</style>
