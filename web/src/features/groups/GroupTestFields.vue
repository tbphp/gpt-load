<script setup lang="ts">
import { useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { AccessProtocol } from '@/api/control/types'
import AppSelect from '@/components/ui/AppSelect.vue'

withDefaults(
  defineProps<{
    protocol?: AccessProtocol | null
    protocols: AccessProtocol[]
    model?: string | null
    models: { id: string; alias?: string }[]
    disabled?: boolean
    showHelp?: boolean
  }>(),
  { protocol: undefined, model: undefined, disabled: false, showHelp: false },
)
const emit = defineEmits<{
  'update:protocol': [value: AccessProtocol]
  'update:model': [value: string]
}>()
const { t } = useI18n()
const modelInputId = `${useId()}-test-model`
const modelListId = `${modelInputId}-options`
</script>

<template>
  <div class="group-test-fields">
    <div class="group-test-fields__field">
      <span>{{ t('group.settings.base.validationProtocol') }}</span>
      <AppSelect
        :model-value="protocol ?? undefined"
        :label="t('group.settings.base.validationProtocol')"
        :options="protocols.map((value) => ({ value, label: value }))"
        :disabled="disabled || protocols.length <= 1"
        @update:model-value="emit('update:protocol', $event as AccessProtocol)"
      />
      <small v-if="showHelp">{{ t('group.settings.base.validationProtocolHelp') }}</small>
    </div>
    <div class="group-test-fields__field">
      <label :for="modelInputId">{{
        t(showHelp ? 'group.settings.base.validationModel' : 'group.credentials.test.fields.model')
      }}</label>
      <input
        :id="modelInputId"
        :value="model ?? ''"
        :list="modelListId"
        :placeholder="t('group.settings.base.validationModelPlaceholder')"
        :disabled="disabled"
        autocomplete="off"
        @input="emit('update:model', ($event.target as HTMLInputElement).value)"
      />
      <datalist :id="modelListId">
        <option
          v-for="option in models"
          :key="option.id"
          :value="option.id"
          :label="option.alias || undefined"
        />
      </datalist>
      <small v-if="showHelp">{{ t('group.settings.base.validationModelHelp') }}</small>
    </div>
  </div>
</template>

<style scoped>
.group-test-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 15px 18px;
  font-size: var(--text-body);
}

.group-test-fields__field {
  display: grid;
  min-width: 0;
  align-content: start;
  gap: 6px;
}

.group-test-fields__field > span,
.group-test-fields__field > label {
  color: var(--color-text-muted);
  font-weight: 560;
}

.group-test-fields__field input,
.group-test-fields__field :deep(.app-select__trigger) {
  width: 100%;
  min-width: 0;
  height: var(--setting-control-height);
  min-height: var(--setting-control-height);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 0 var(--space-3);
  font: inherit;
}

.group-test-fields__field small {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: var(--line-normal);
}

@media (max-width: 600px) {
  .group-test-fields {
    grid-template-columns: 1fr;
  }
}
</style>
