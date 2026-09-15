<script setup lang="ts">
import { Plus, Trash2 } from '@lucide/vue'
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { AppButton, AppIconButton, AppSelect, AppTextField } from '@modern/components/ui'
import { newHeader, type HeaderRow, type HeaderSetting } from './settings-draft'

const props = defineProps<{
  setting: HeaderSetting
  disabled: boolean
  errors: Record<string, string>
}>()
const model = defineModel<HeaderRow[]>({ required: true })
const editor = ref<HTMLElement>()
const { t } = useI18n()
const actions = computed(() => [
  { value: 'set', label: t('settingsForm.headers.set') },
  { value: 'remove', label: t('settingsForm.headers.remove') },
])
async function addRule(): Promise<void> {
  if (props.disabled) return
  model.value = [...model.value, newHeader()]
  await nextTick()
  editor.value
    ?.querySelectorAll<HTMLInputElement>('input[data-header-name]')
    .item(model.value.length - 1)
    ?.focus()
}
</script>

<template>
  <div ref="editor" class="modern-settings-headers">
    <div v-if="model.length" class="modern-settings-header-labels" aria-hidden="true">
      <span>{{ t('settingsForm.headers.action') }}</span>
      <span>{{ t('settingsForm.headers.name') }}</span>
      <span>{{ t('settingsForm.headers.value') }}</span>
    </div>
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
        data-header-name
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
    <div class="modern-settings-header-footer" :class="{ 'is-empty': !model.length }">
      <span v-if="!model.length">{{ t('settingsForm.headers.empty') }}</span>
      <AppButton size="sm" :icon="Plus" :disabled="disabled" @click="addRule">{{
        t('settingsForm.headers.add')
      }}</AppButton>
    </div>
  </div>
</template>

<style scoped>
.modern-settings-headers {
  --modern-settings-header-action-size: var(--modern-control-sm);
  --modern-settings-header-columns: 112px minmax(0, 1fr) minmax(0, 1.5fr)
    var(--modern-settings-header-action-size);
  container: modern-settings-headers / inline-size;
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-settings-header-row,
.modern-settings-header-labels {
  display: grid;
  grid-template-columns: var(--modern-settings-header-columns);
  align-items: start;
  gap: var(--modern-space-2);
}
.modern-settings-header-labels {
  padding-block: var(--modern-space-2);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-settings-header-row + .modern-settings-header-row {
  padding-top: var(--modern-space-2);
  border-top: var(--modern-line-width) solid var(--modern-border);
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
.modern-settings-header-footer.is-empty {
  padding: var(--modern-space-3);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
}
@container modern-settings-headers (max-width: 600px) {
  .modern-settings-header-labels {
    display: none;
  }
  .modern-settings-header-row {
    grid-template-columns: 112px minmax(0, 1fr) var(--modern-settings-header-action-size);
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
@media (max-width: 760px) {
  .modern-settings-headers {
    --modern-settings-header-action-size: var(--modern-touch-target);
  }
}
</style>
