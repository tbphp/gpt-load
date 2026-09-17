<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { settingNumbers, type SettingNumber } from '@modern/api/settings'
import { AppTextField } from '@modern/components/ui'
import SettingItem from './SettingItem.vue'

defineProps<{
  setting: SettingNumber
  overridden: boolean
  changed: boolean
  resetting: boolean
  locked: boolean
  disabled: boolean
  error?: string
}>()
defineEmits<{ reset: []; undo: [] }>()
const model = defineModel<string>({ required: true })
const { t } = useI18n()
</script>

<template>
  <SettingItem
    :label="t('settingsForm.fields.' + setting)"
    :hint="t('settingsForm.hints.' + setting)"
    :control-id="'settings-' + setting"
    :overridden="overridden"
    :changed="changed"
    :resetting="resetting"
    :locked="locked"
    :disabled="disabled"
    class="modern-settings-number-field"
    stacked
    @reset="$emit('reset')"
    @undo="$emit('undo')"
  >
    <AppTextField
      :id="'settings-' + setting"
      v-model="model"
      :label="t('settingsForm.fields.' + setting)"
      label-hidden
      inputmode="numeric"
      autocomplete="off"
      :disabled="disabled || locked"
      :error="error"
    >
      <template #suffix
        ><span class="modern-settings-number-unit">{{
          t('settingsForm.' + settingNumbers[setting].unit)
        }}</span></template
      >
    </AppTextField>
  </SettingItem>
</template>

<style scoped>
.modern-settings-number-field {
  grid-row: span 2;
  grid-template-rows: subgrid;
  align-items: start;
}
.modern-settings-number-unit {
  flex: none;
  white-space: nowrap;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
</style>
