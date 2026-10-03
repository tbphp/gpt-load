<script setup lang="ts">
import { RotateCcw } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import {
  modelReasoningLevels,
  type ModelInputModality,
  type ModelProfile,
  type ModelProfileField as Field,
  type ModelReasoningLevel,
} from '@modern/api/models'
import { AppButton, AppIconButton, AppTextField } from '@modern/components/ui'
import ModelProfileField from './ModelProfileField.vue'
import { setModelProfileFieldMode, type ModelProfileDraft } from './model-profile-draft'

const props = defineProps<{
  profile: ModelProfile
  disabled?: boolean
  errors?: Partial<Record<Field, string>>
}>()
const draft = defineModel<ModelProfileDraft>({ required: true })
const { t, n } = useI18n()
function automaticValue(field: Field): string {
  const value = props.profile.automatic[field]
  if (field === 'context_window')
    return value === null ? t('modelManager.profile.unknownContext') : n(value as number)
  if (field === 'input_modalities')
    return (value as string[]).map((item) => t(`modelManager.modality.${item}`)).join(' / ')
  return Array.isArray(value) ? value.join(' / ') : String(value)
}
function reset(field: Field): void {
  if (!props.disabled) setModelProfileFieldMode(props.profile, draft.value, field, false)
}
function text(field: 'display_name' | 'context_window', value: string | number): void {
  if (props.disabled) return
  draft.value.custom[field] = true
  draft.value.values[field] = String(value)
}
function reasoning(level: ModelReasoningLevel): void {
  if (props.disabled) return
  const levels = new Set(draft.value.values.supported_reasoning_levels)
  if (levels.has(level)) levels.delete(level)
  else levels.add(level)
  draft.value.custom.supported_reasoning_levels = true
  draft.value.values.supported_reasoning_levels = modelReasoningLevels.filter((value) =>
    levels.has(value),
  )
}
function modality(value: ModelInputModality): void {
  if (props.disabled || value === 'text') return
  const values = new Set(draft.value.values.input_modalities)
  if (values.has(value)) values.delete(value)
  else values.add(value)
  values.add('text')
  draft.value.custom.input_modalities = true
  draft.value.values.input_modalities = (['text', 'image', 'audio'] as const).filter((value) =>
    values.has(value),
  )
}
</script>

<template>
  <div class="modern-model-profile-fields">
    <AppTextField
      :model-value="draft.values.display_name"
      :label="t('modelManager.profile.fields.displayName')"
      size="sm"
      :disabled="disabled"
      :error="errors?.display_name"
      @update:model-value="text('display_name', $event)"
    >
      <template #label-extra
        ><AppIconButton
          v-if="draft.custom.display_name"
          :icon="RotateCcw"
          :label="t('modelManager.profile.restoreField', { value: automaticValue('display_name') })"
          size="xxs"
          :disabled="disabled"
          @click="reset('display_name')"
        /><span v-else class="modern-model-profile-auto">{{
          t('modelManager.profile.automatic')
        }}</span></template
      >
    </AppTextField>
    <AppTextField
      :model-value="draft.values.context_window"
      :label="t('modelManager.profile.fields.contextWindow')"
      size="sm"
      type="text"
      inputmode="numeric"
      :placeholder="automaticValue('context_window')"
      :disabled="disabled"
      :error="errors?.context_window"
      @update:model-value="text('context_window', $event)"
    >
      <template #label-extra
        ><AppIconButton
          v-if="draft.custom.context_window"
          :icon="RotateCcw"
          :label="
            t('modelManager.profile.restoreField', { value: automaticValue('context_window') })
          "
          size="xxs"
          :disabled="disabled"
          @click="reset('context_window')"
        /><span v-else class="modern-model-profile-auto">{{
          t('modelManager.profile.automatic')
        }}</span></template
      >
    </AppTextField>
    <ModelProfileField
      class="modern-model-profile-wide"
      :label="t('modelManager.profile.fields.supportedReasoningLevels')"
      :automatic="automaticValue('supported_reasoning_levels')"
      :custom="draft.custom.supported_reasoning_levels"
      :disabled="disabled"
      :error="errors?.supported_reasoning_levels"
      @reset="reset('supported_reasoning_levels')"
    >
      <template #default="{ id, describedBy, invalid }"
        ><div
          :id="id"
          class="modern-model-profile-choices"
          role="group"
          :aria-labelledby="`${id}-label`"
          :aria-describedby="describedBy"
          :aria-invalid="invalid || undefined"
          tabindex="-1"
        >
          <AppButton
            v-for="level in modelReasoningLevels"
            :key="level"
            size="xxs"
            :variant="draft.values.supported_reasoning_levels.includes(level) ? 'brand' : 'default'"
            :aria-pressed="draft.values.supported_reasoning_levels.includes(level)"
            :disabled="disabled"
            @click="reasoning(level)"
            >{{ level }}</AppButton
          >
        </div></template
      >
    </ModelProfileField>
    <ModelProfileField
      class="modern-model-profile-wide"
      :label="t('modelManager.profile.fields.inputModalities')"
      :automatic="automaticValue('input_modalities')"
      :custom="draft.custom.input_modalities"
      :disabled="disabled"
      :error="errors?.input_modalities"
      @reset="reset('input_modalities')"
    >
      <template #default="{ id, describedBy }"
        ><div
          :id="id"
          class="modern-model-profile-choices"
          role="group"
          :aria-labelledby="`${id}-label`"
          :aria-describedby="describedBy"
        >
          <AppButton
            v-for="value in ['text', 'image', 'audio'] as const"
            :key="value"
            size="xxs"
            :variant="draft.values.input_modalities.includes(value) ? 'brand' : 'default'"
            :aria-pressed="draft.values.input_modalities.includes(value)"
            :disabled="disabled || value === 'text'"
            @click="modality(value)"
            >{{ t(`modelManager.modality.${value}`) }}</AppButton
          >
        </div></template
      >
    </ModelProfileField>
  </div>
</template>
<style scoped>
.modern-model-profile-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-3) var(--modern-space-5);
}
.modern-model-profile-auto {
  display: inline-flex;
  align-items: center;
  min-height: var(--modern-control-xxs);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-model-profile-wide {
  grid-column: 1 / -1;
}
.modern-model-profile-choices {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-1-5);
}
@media (max-width: 760px) {
  .modern-model-profile-fields {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
