<script setup lang="ts">
import { Info, RotateCcw } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  modelReasoningLevels,
  modelServiceTiers,
  resolveDefaultReasoningLevel,
  type ModelInputModality,
  type ModelProfile,
  type ModelProfileField as Field,
  type ModelReasoningLevel,
  type ModelServiceTier,
} from '@modern/api/models'
import {
  AppButton,
  AppIcon,
  AppIconButton,
  AppSelect,
  AppTextField,
  AppTooltip,
} from '@modern/components/ui'
import ModelProfileField from './ModelProfileField.vue'
import {
  resetModelProfileField,
  syncModelProfileReasoningDefault,
  type ModelProfileDraft,
} from './model-profile-draft'

const props = defineProps<{
  profile: ModelProfile
  disabled?: boolean
  errors?: Partial<Record<Field, string>>
}>()
const draft = defineModel<ModelProfileDraft>({ required: true })
const { t, n } = useI18n()
const automaticCompactionLimit = computed(() => {
  const contextWindow = Number(draft.value.values.context_window)
  if (!Number.isSafeInteger(contextWindow) || contextWindow < 1) return null
  const maximum = Number((BigInt(contextWindow) * 9n) / 10n)
  const preset = props.profile.automatic.auto_compact_token_limit
  return preset === null ? maximum : Math.min(preset, maximum)
})
const compactionValue = computed(() =>
  draft.value.custom.auto_compact_token_limit
    ? draft.value.values.auto_compact_token_limit
    : automaticCompactionLimit.value === null
      ? ''
      : String(automaticCompactionLimit.value),
)
const defaultReasoningOptions = computed(() =>
  draft.value.values.supported_reasoning_levels.map((level) => ({
    value: level,
    label: level,
  })),
)
function tierLabel(tier: ModelServiceTier): string {
  return t(`modelManager.profile.serviceTierNames.${tier}`)
}
function automaticValue(field: Field): string {
  const value = props.profile.automatic[field]
  if (field === 'auto_compact_token_limit')
    return automaticCompactionLimit.value === null
      ? t('modelManager.profile.unknownContext')
      : n(automaticCompactionLimit.value)
  if (field === 'description' && !value) return t('modelManager.profile.emptyDescription')
  if (field === 'service_tiers')
    return (
      (value as ModelServiceTier[]).map(tierLabel).join(' / ') ||
      t('modelManager.profile.noServiceTiers')
    )
  if (field === 'default_reasoning_level')
    return resolveDefaultReasoningLevel(
      draft.value.values.supported_reasoning_levels,
      props.profile.automatic.default_reasoning_level,
    )
  if (field === 'context_window')
    return value === null ? t('modelManager.profile.unknownContext') : n(value as number)
  if (field === 'input_modalities')
    return (value as string[]).map((item) => t(`modelManager.modality.${item}`)).join(' / ')
  return Array.isArray(value) ? value.join(' / ') : String(value)
}
function reset(field: Field): void {
  if (props.disabled) return
  resetModelProfileField(props.profile, draft.value, field)
}
function text(
  field: 'display_name' | 'description' | 'context_window' | 'auto_compact_token_limit',
  value: string | number,
): void {
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
  syncModelProfileReasoningDefault(props.profile, draft.value)
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
function serviceTier(tier: ModelServiceTier): void {
  if (props.disabled) return
  const values = new Set(draft.value.values.service_tiers)
  if (values.has(tier)) values.delete(tier)
  else values.add(tier)
  draft.value.custom.service_tiers = true
  draft.value.values.service_tiers = modelServiceTiers.filter((tier) => values.has(tier))
}
function defaultReasoning(value: string): void {
  if (props.disabled) return
  if (!draft.value.values.supported_reasoning_levels.some((level) => level === value)) return
  draft.value.custom.default_reasoning_level = true
  draft.value.values.default_reasoning_level = value as ModelReasoningLevel
}
</script>

<template>
  <div class="modern-model-profile-fields">
    <AppTextField
      class="modern-model-profile-start"
      :model-value="draft.values.display_name"
      :label="t('modelManager.profile.fields.displayName')"
      size="xxs"
      inline="subgrid"
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
      /></template>
    </AppTextField>
    <AppTextField
      class="modern-model-profile-end"
      :model-value="draft.values.context_window"
      :label="t('modelManager.profile.fields.contextWindow')"
      size="xxs"
      inline="subgrid"
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
      /></template>
    </AppTextField>
    <AppTextField
      class="modern-model-profile-wide"
      :model-value="draft.values.description"
      :label="t('modelManager.profile.fields.description')"
      size="xxs"
      inline="subgrid"
      :disabled="disabled"
      :error="errors?.description"
      @update:model-value="text('description', $event)"
    >
      <template #label-extra
        ><AppIconButton
          v-if="draft.custom.description"
          :icon="RotateCcw"
          :label="t('modelManager.profile.restoreField', { value: automaticValue('description') })"
          size="xxs"
          :disabled="disabled"
          @click="reset('description')"
      /></template>
    </AppTextField>
    <AppTextField
      class="modern-model-profile-start"
      :model-value="compactionValue"
      :label="t('modelManager.profile.fields.autoCompactTokenLimit')"
      size="xxs"
      inline="subgrid"
      type="text"
      inputmode="numeric"
      :placeholder="automaticValue('auto_compact_token_limit')"
      :disabled="disabled"
      :error="errors?.auto_compact_token_limit"
      @update:model-value="text('auto_compact_token_limit', $event)"
    >
      <template #label-extra
        ><AppIconButton
          v-if="draft.custom.auto_compact_token_limit"
          :icon="RotateCcw"
          :label="
            t('modelManager.profile.restoreField', {
              value: automaticValue('auto_compact_token_limit'),
            })
          "
          size="xxs"
          :disabled="disabled"
          @click="reset('auto_compact_token_limit')"
      /></template>
      <template #suffix
        ><AppIcon
          :icon="Info"
          size="xs"
          :label="t('modelManager.profile.autoCompactHelp')"
          class="modern-help-trigger"
      /></template>
    </AppTextField>
    <AppSelect
      class="modern-model-profile-end"
      :model-value="draft.values.default_reasoning_level"
      :label="t('modelManager.profile.fields.defaultReasoningLevel')"
      :options="defaultReasoningOptions"
      size="xxs"
      inline="subgrid"
      :tooltip="false"
      :disabled="disabled || !draft.values.supported_reasoning_levels.length"
      :error="errors?.default_reasoning_level"
      @update:model-value="defaultReasoning"
    >
      <template #label-extra
        ><AppIconButton
          v-if="draft.custom.default_reasoning_level"
          :icon="RotateCcw"
          :label="
            t('modelManager.profile.restoreField', {
              value: automaticValue('default_reasoning_level'),
            })
          "
          size="xxs"
          :disabled="disabled"
          @click="reset('default_reasoning_level')"
      /></template>
    </AppSelect>
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
            :variant="
              draft.values.supported_reasoning_levels.includes(level) ? 'outline' : 'default'
            "
            :aria-pressed="draft.values.supported_reasoning_levels.includes(level)"
            :disabled="disabled"
            @click="reasoning(level)"
            >{{ level }}</AppButton
          >
        </div></template
      >
    </ModelProfileField>
    <ModelProfileField
      class="modern-model-profile-start"
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
            :variant="draft.values.input_modalities.includes(value) ? 'outline' : 'default'"
            :aria-pressed="draft.values.input_modalities.includes(value)"
            :disabled="disabled || value === 'text'"
            @click="modality(value)"
            >{{ t(`modelManager.modality.${value}`) }}</AppButton
          >
        </div></template
      >
    </ModelProfileField>
    <ModelProfileField
      class="modern-model-profile-end"
      :label="t('modelManager.profile.fields.serviceTiers')"
      :automatic="automaticValue('service_tiers')"
      :custom="draft.custom.service_tiers"
      :disabled="disabled"
      :error="errors?.service_tiers"
      @reset="reset('service_tiers')"
    >
      <template #default="{ id, describedBy, invalid }"
        ><div
          :id="id"
          class="modern-model-profile-choices"
          role="group"
          :aria-labelledby="`${id}-label`"
          :aria-describedby="describedBy"
          :aria-invalid="invalid || undefined"
        >
          <AppTooltip
            v-for="tier in modelServiceTiers"
            :key="tier"
            :label="
              t(
                profile.automatic.service_tiers.includes(tier)
                  ? 'modelManager.profile.presetTierEnabled'
                  : 'modelManager.profile.presetTierDisabled',
              )
            "
          >
            <AppButton
              size="xxs"
              :variant="draft.values.service_tiers.includes(tier) ? 'outline' : 'default'"
              :aria-pressed="draft.values.service_tiers.includes(tier)"
              :disabled="disabled"
              @click="serviceTier(tier)"
              >{{ tierLabel(tier) }}</AppButton
            >
          </AppTooltip>
        </div></template
      >
    </ModelProfileField>
  </div>
</template>
<style scoped>
.modern-model-profile-fields {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr) var(--modern-space-4) max-content minmax(
      0,
      1fr
    );
  row-gap: var(--modern-space-2);
}
.modern-model-profile-start {
  grid-column: 1 / span 2;
}
.modern-model-profile-end {
  grid-column: 4 / -1;
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
    grid-template-columns: max-content minmax(0, 1fr);
  }
  .modern-model-profile-end {
    grid-column: 1 / -1;
  }
}
</style>
