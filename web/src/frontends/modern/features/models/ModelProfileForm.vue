<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  modelReasoningLevels,
  type ModelInputModality,
  type ModelProfile,
  type ModelProfileField as Field,
} from '@modern/api/models'
import { AppCheckbox, AppMultiSelect, AppTextField } from '@modern/components/ui'
import ModelProfileField from './ModelProfileField.vue'
import { setModelProfileFieldMode, type ModelProfileDraft } from './model-profile-draft'

const props = defineProps<{
  profile: ModelProfile
  disabled?: boolean
  errors?: Partial<Record<Field, string>>
}>()
const draft = defineModel<ModelProfileDraft>({ required: true })
const { t, n } = useI18n()
const reasoningOptions = computed(() =>
  modelReasoningLevels.map((value) => ({ value, label: value })),
)
function automaticValue(field: Field): string {
  const automatic = props.profile.automatic
  switch (field) {
    case 'display_name':
      return automatic.display_name
    case 'context_window':
      return automatic.context_window === null
        ? t('modelManager.profile.unknownContext')
        : n(automatic.context_window)
    case 'supported_reasoning_levels':
      return automatic.supported_reasoning_levels.join(' / ')
    case 'input_modalities':
      return automatic.input_modalities
        .map((value) => t(`modelManager.modality.${value}`))
        .join(' / ')
  }
}
function setCustom(field: Field, custom: boolean): void {
  if (!props.disabled) setModelProfileFieldMode(props.profile, draft.value, field, custom)
}
function toggleModality(value: Exclude<ModelInputModality, 'text'>, enabled: boolean): void {
  const values = new Set(draft.value.values.input_modalities)
  if (enabled) values.add(value)
  else values.delete(value)
  values.add('text')
  draft.value.values.input_modalities = (['text', 'image', 'audio'] as const).filter((value) =>
    values.has(value),
  )
}
</script>

<template>
  <div class="modern-model-profile-fields">
    <ModelProfileField
      :label="t('modelManager.profile.fields.displayName')"
      :automatic="automaticValue('display_name')"
      :custom="draft.custom.display_name"
      :disabled="disabled"
      :error="errors?.display_name"
      @update:custom="setCustom('display_name', $event)"
    >
      <template #default="{ disabled: fieldDisabled }">
        <AppTextField
          v-model="draft.values.display_name"
          :label="t('modelManager.profile.fields.displayName')"
          label-hidden
          size="sm"
          :disabled="fieldDisabled"
          :invalid="Boolean(errors?.display_name)"
        />
      </template>
    </ModelProfileField>
    <ModelProfileField
      :label="t('modelManager.profile.fields.contextWindow')"
      :description="t('modelManager.profile.fieldHelp.contextWindow')"
      :automatic="automaticValue('context_window')"
      :custom="draft.custom.context_window"
      :disabled="disabled"
      :error="errors?.context_window"
      @update:custom="setCustom('context_window', $event)"
    >
      <template #default="{ disabled: fieldDisabled }">
        <AppTextField
          v-model="draft.values.context_window"
          :label="t('modelManager.profile.fields.contextWindow')"
          label-hidden
          size="sm"
          type="text"
          inputmode="numeric"
          :placeholder="
            draft.custom.context_window
              ? t('modelManager.profile.contextPlaceholder')
              : automaticValue('context_window')
          "
          :disabled="fieldDisabled"
          :invalid="Boolean(errors?.context_window)"
        />
      </template>
    </ModelProfileField>
    <ModelProfileField
      :label="t('modelManager.profile.fields.supportedReasoningLevels')"
      :automatic="automaticValue('supported_reasoning_levels')"
      :custom="draft.custom.supported_reasoning_levels"
      :disabled="disabled"
      :error="errors?.supported_reasoning_levels"
      @update:custom="setCustom('supported_reasoning_levels', $event)"
    >
      <template #default="{ disabled: fieldDisabled }">
        <AppMultiSelect
          v-model="draft.values.supported_reasoning_levels"
          :label="t('modelManager.profile.fields.supportedReasoningLevels')"
          label-hidden
          :options="reasoningOptions"
          :disabled="fieldDisabled"
          :invalid="Boolean(errors?.supported_reasoning_levels)"
        />
      </template>
    </ModelProfileField>
    <ModelProfileField
      :label="t('modelManager.profile.fields.inputModalities')"
      :description="t('modelManager.profile.fieldHelp.inputModalities')"
      :automatic="automaticValue('input_modalities')"
      :custom="draft.custom.input_modalities"
      :disabled="disabled"
      :error="errors?.input_modalities"
      @update:custom="setCustom('input_modalities', $event)"
    >
      <template #default="{ disabled: fieldDisabled }">
        <div class="modern-model-profile-modalities">
          <AppCheckbox :model-value="true" :label="t('modelManager.modality.text')" disabled />
          <AppCheckbox
            :model-value="draft.values.input_modalities.includes('image')"
            :label="t('modelManager.modality.image')"
            :disabled="fieldDisabled"
            @update:model-value="toggleModality('image', $event)"
          />
          <AppCheckbox
            :model-value="draft.values.input_modalities.includes('audio')"
            :label="t('modelManager.modality.audio')"
            :disabled="fieldDisabled"
            @update:model-value="toggleModality('audio', $event)"
          />
        </div>
      </template>
    </ModelProfileField>
  </div>
</template>

<style scoped>
.modern-model-profile-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-5);
}
.modern-model-profile-fields :deep(.modern-model-profile-field) {
  border: 0;
  padding: 0;
}
.modern-model-profile-modalities {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-3);
  padding-block: var(--modern-space-2);
}
@media (max-width: 760px) {
  .modern-model-profile-fields {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
