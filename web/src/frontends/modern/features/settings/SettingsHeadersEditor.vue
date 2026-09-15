<script setup lang="ts">
import { Plus, Trash2 } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { AppButton, AppIconButton, AppSelect, AppTextField } from '@modern/components/ui'
import { newHeader, type HeaderRow, type HeaderSetting } from './settings-draft'

defineProps<{ setting: HeaderSetting; disabled: boolean; errors: Record<string, string> }>()
const model = defineModel<HeaderRow[]>({ required: true })
const { t } = useI18n()
const actions = computed(() => [
  { value: 'set', label: t('settingsForm.headers.set') },
  { value: 'remove', label: t('settingsForm.headers.remove') },
])
</script>

<template>
  <div class="modern-settings-headers">
    <div v-for="row in model" :key="row.id" class="modern-settings-header-row">
      <AppSelect
        :model-value="row.action"
        :options="actions"
        :label="t('settingsForm.headers.action')"
        label-hidden
        size="sm"
        :disabled="disabled"
        @update:model-value="row.action = $event === 'remove' ? 'remove' : 'set'"
      />
      <AppTextField
        v-model="row.name"
        :label="t('settingsForm.headers.name')"
        :placeholder="t('settingsForm.headers.name')"
        :error="errors[setting + '.' + row.id + '.name']"
        label-hidden
        size="sm"
        :disabled="disabled"
        autocomplete="off"
        spellcheck="false"
      />
      <AppTextField
        v-if="row.action === 'set'"
        v-model="row.value"
        :label="t('settingsForm.headers.value')"
        :placeholder="t('settingsForm.headers.value')"
        :error="errors[setting + '.' + row.id + '.value']"
        label-hidden
        size="sm"
        :disabled="disabled"
        autocomplete="off"
        spellcheck="false"
      />
      <span v-else class="modern-settings-header-remove">{{
        t('settingsForm.headers.removeHint')
      }}</span>
      <AppIconButton
        :icon="Trash2"
        :label="t('settingsForm.headers.delete')"
        size="sm"
        :disabled="disabled"
        @click="model = model.filter((value) => value.id !== row.id)"
      />
    </div>
    <div class="modern-settings-header-footer">
      <span v-if="!model.length">{{ t('settingsForm.headers.empty') }}</span>
      <AppButton
        variant="text"
        size="sm"
        :icon="Plus"
        :disabled="disabled"
        @click="model = [...model, newHeader()]"
        >{{ t('settingsForm.headers.add') }}</AppButton
      >
    </div>
  </div>
</template>

<style scoped>
.modern-settings-headers {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-settings-header-row {
  display: grid;
  grid-template-columns: 100px minmax(0, 1fr) minmax(0, 1.6fr) auto;
  align-items: start;
  gap: var(--modern-space-2);
}
.modern-settings-header-remove {
  align-self: center;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-settings-header-footer {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
@media (max-width: 760px) {
  .modern-settings-header-row {
    grid-template-columns: 100px minmax(0, 1fr) auto;
  }
  .modern-settings-header-row > :nth-child(3) {
    grid-column: 1 / 3;
    grid-row: 2;
  }
  .modern-settings-header-row > :last-child {
    grid-column: 3;
    grid-row: 1;
  }
}
</style>
