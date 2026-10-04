<script setup lang="ts">
import { RotateCcw } from '@lucide/vue'
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  modelReasoningLevels,
  modelInputModalities,
  modelServiceTiers,
  resolveDefaultReasoningLevel,
  type ModelProfile,
  type ModelProfileField,
  type ModelReasoningLevel,
} from '@/app/resources/client-catalog'
import AppButton from '@/components/ui/AppButton.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import IconButton from '@/components/ui/IconButton.vue'
import {
  resetModelProfileField,
  syncModelProfileReasoningDefault,
  type ModelProfileDraft,
} from './model-profile-draft'
const props = defineProps<{
  profile: ModelProfile
  disabled?: boolean
  errors: Partial<Record<ModelProfileField, string>>
}>()
const draft = defineModel<ModelProfileDraft>({ required: true })
const { t } = useI18n()
const prefix = useId()
const textFields = [
  'display_name',
  'context_window',
  'description',
  'auto_compact_token_limit',
] as const
const labels = {
  display_name: 'displayName',
  context_window: 'contextWindow',
  description: 'description',
  auto_compact_token_limit: 'autoCompactTokenLimit',
  default_reasoning_level: 'defaultReasoningLevel',
  supported_reasoning_levels: 'supportedReasoningLevels',
  input_modalities: 'inputModalities',
  service_tiers: 'serviceTiers',
} as const
const choiceGroups = computed(() => [
  { field: 'supported_reasoning_levels' as const, options: modelReasoningLevels },
  { field: 'input_modalities' as const, options: modelInputModalities },
  { field: 'service_tiers' as const, options: modelServiceTiers },
])
const reasoningOptions = computed(() =>
  draft.value.values.supported_reasoning_levels.map((value) => ({ value, label: value })),
)
function value(field: (typeof textFields)[number]): string {
  if (field !== 'auto_compact_token_limit' || draft.value.custom[field])
    return draft.value.values[field]
  const window = Number(draft.value.values.context_window)
  if (!Number.isSafeInteger(window) || window < 1) return ''
  const maximum = Number((BigInt(window) * 9n) / 10n)
  const preset = props.profile.automatic.auto_compact_token_limit
  return String(preset === null ? maximum : Math.min(preset, maximum))
}
function text(field: (typeof textFields)[number], value: string): void {
  if (props.disabled) return
  draft.value.custom[field] = true
  draft.value.values[field] = value
}
function reset(field: ModelProfileField): void {
  if (!props.disabled) resetModelProfileField(props.profile, draft.value, field)
}
function automatic(field: ModelProfileField): string {
  if (field === 'default_reasoning_level')
    return resolveDefaultReasoningLevel(
      draft.value.values.supported_reasoning_levels,
      props.profile.automatic.default_reasoning_level,
    )
  if (field === 'auto_compact_token_limit') {
    const window = Number(draft.value.values.context_window)
    if (!Number.isSafeInteger(window) || window < 1) return t('models.profile.unknownContext')
    const maximum = Number((BigInt(window) * 9n) / 10n)
    return String(Math.min(props.profile.automatic.auto_compact_token_limit ?? maximum, maximum))
  }
  const value = props.profile.automatic[field]
  return Array.isArray(value)
    ? value.map((item) => optionLabel(field, item)).join(' / ') ||
        t('models.profile.noServiceTiers')
    : String(value ?? '')
}
function optionLabel(field: ModelProfileField, item: string): string {
  return field === 'input_modalities'
    ? t(`models.modality.${item}`)
    : field === 'service_tiers'
      ? t(`models.profile.serviceTierNames.${item}`)
      : item
}
function toggle(
  field: 'supported_reasoning_levels' | 'input_modalities' | 'service_tiers',
  item: string,
): void {
  if (props.disabled || (field === 'input_modalities' && item === 'text')) return
  const values = new Set<string>(draft.value.values[field])
  if (values.has(item)) values.delete(item)
  else values.add(item)
  draft.value.custom[field] = true
  if (field === 'supported_reasoning_levels') {
    draft.value.values[field] = modelReasoningLevels.filter((item) => values.has(item))
    syncModelProfileReasoningDefault(props.profile, draft.value)
  } else if (field === 'input_modalities')
    draft.value.values[field] = modelInputModalities.filter((item) => values.has(item))
  else draft.value.values[field] = modelServiceTiers.filter((item) => values.has(item))
}
function defaultReasoning(value: string): void {
  if (
    props.disabled ||
    !draft.value.values.supported_reasoning_levels.some((level) => level === value)
  )
    return
  draft.value.custom.default_reasoning_level = true
  draft.value.values.default_reasoning_level = value as ModelReasoningLevel
}
</script>
<template>
  <div class="catalog-profile">
    <div
      v-for="field in textFields"
      :key="field"
      class="catalog-profile__field"
      :class="{ 'catalog-profile__wide': field === 'description' }"
    >
      <div class="catalog-profile__heading">
        <AppTooltip
          v-if="field === 'auto_compact_token_limit'"
          :content="t('models.profile.autoCompactHelp')"
          ><label :for="`${prefix}-${field}`">{{
            t(`models.profile.fields.${labels[field]}`)
          }}</label></AppTooltip
        >
        <label v-else :for="`${prefix}-${field}`">{{
          t(`models.profile.fields.${labels[field]}`)
        }}</label>
        <span class="catalog-profile__reset"
          ><AppTooltip
            v-if="draft.custom[field]"
            :content="t('models.profile.restoreField', { value: automatic(field) })"
            ><IconButton
              :label="t('models.profile.restoreField', { value: automatic(field) })"
              size="xs"
              :disabled="disabled"
              @click="reset(field)"
              ><RotateCcw :size="14" /></IconButton></AppTooltip
        ></span>
      </div>
      <AppTextInput
        :id="`${prefix}-${field}`"
        :model-value="value(field)"
        :label="t(`models.profile.fields.${labels[field]}`)"
        size="compact"
        :disabled="disabled"
        :invalid="Boolean(errors[field])"
        :described-by="errors[field] ? `${prefix}-${field}-error` : undefined"
        :inputmode="
          field === 'context_window' || field === 'auto_compact_token_limit' ? 'numeric' : undefined
        "
        @update:model-value="text(field, $event)"
      />
      <p
        v-if="errors[field]"
        :id="`${prefix}-${field}-error`"
        class="catalog-profile__error"
        role="alert"
      >
        {{ errors[field] }}
      </p>
    </div>
    <div class="catalog-profile__field">
      <div class="catalog-profile__heading">
        <label :for="`${prefix}-default`">{{
          t('models.profile.fields.defaultReasoningLevel')
        }}</label
        ><span class="catalog-profile__reset"
          ><AppTooltip
            v-if="draft.custom.default_reasoning_level"
            :content="
              t('models.profile.restoreField', { value: automatic('default_reasoning_level') })
            "
            ><IconButton
              :label="
                t('models.profile.restoreField', { value: automatic('default_reasoning_level') })
              "
              size="xs"
              :disabled="disabled"
              @click="reset('default_reasoning_level')"
              ><RotateCcw :size="14" /></IconButton></AppTooltip
        ></span>
      </div>
      <AppSelect
        :id="`${prefix}-default`"
        :model-value="draft.values.default_reasoning_level"
        :options="reasoningOptions"
        :label="t('models.profile.fields.defaultReasoningLevel')"
        size="compact"
        :disabled="disabled || !reasoningOptions.length"
        @update:model-value="defaultReasoning"
      />
      <p v-if="errors.default_reasoning_level" class="catalog-profile__error" role="alert">
        {{ errors.default_reasoning_level }}
      </p>
    </div>
    <div
      v-for="group in choiceGroups"
      :key="group.field"
      class="catalog-profile__field catalog-profile__wide"
    >
      <div class="catalog-profile__heading">
        <span :id="`${prefix}-${group.field}-label`">{{
          t(`models.profile.fields.${labels[group.field]}`)
        }}</span
        ><span class="catalog-profile__reset"
          ><AppTooltip
            v-if="draft.custom[group.field]"
            :content="t('models.profile.restoreField', { value: automatic(group.field) })"
            ><IconButton
              :label="t('models.profile.restoreField', { value: automatic(group.field) })"
              size="xs"
              :disabled="disabled"
              @click="reset(group.field)"
              ><RotateCcw :size="14" /></IconButton></AppTooltip
        ></span>
      </div>
      <div
        class="catalog-profile__choices"
        role="group"
        :aria-labelledby="`${prefix}-${group.field}-label`"
      >
        <template v-for="item in group.options" :key="item">
          <AppTooltip
            v-if="group.field === 'service_tiers'"
            :content="
              t(
                profile.automatic.service_tiers.includes(item as 'priority' | 'ultrafast')
                  ? 'models.profile.presetTierEnabled'
                  : 'models.profile.presetTierDisabled',
              )
            "
            ><AppButton
              variant="secondary"
              size="compact"
              :tone="
                draft.values[group.field].some((value) => value === item) ? 'action' : 'neutral'
              "
              :aria-pressed="draft.values[group.field].some((value) => value === item)"
              :disabled="disabled"
              @click="toggle(group.field, item)"
              >{{ optionLabel(group.field, item) }}</AppButton
            ></AppTooltip
          >
          <AppButton
            v-else
            variant="secondary"
            size="compact"
            :tone="draft.values[group.field].some((value) => value === item) ? 'action' : 'neutral'"
            :aria-pressed="draft.values[group.field].some((value) => value === item)"
            :disabled="disabled || (group.field === 'input_modalities' && item === 'text')"
            @click="toggle(group.field, item)"
            >{{ optionLabel(group.field, item) }}</AppButton
          >
        </template>
      </div>
      <p v-if="errors[group.field]" class="catalog-profile__error" role="alert">
        {{ errors[group.field] }}
      </p>
    </div>
  </div>
</template>
<style scoped>
.catalog-profile {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr) var(--space-4) max-content minmax(0, 1fr);
  gap: var(--space-2) 0;
}
.catalog-profile__field {
  display: grid;
  grid-template-columns: subgrid;
  grid-column: span 2;
  align-items: center;
}
.catalog-profile__wide {
  grid-column: 1 / -1;
}
.catalog-profile__field:nth-child(2),
.catalog-profile__field:nth-child(5) {
  grid-column: 4 / -1;
}
.catalog-profile__heading {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  white-space: nowrap;
  font-size: var(--text-sm);
}
.catalog-profile__reset {
  display: inline-flex;
  width: var(--control-compact);
  min-height: var(--control-compact);
  justify-content: center;
}
.catalog-profile__wide > :nth-child(2) {
  grid-column: 2 / -1;
}
.catalog-profile__choices {
  grid-column: 2 / -1;
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}
.catalog-profile__error {
  grid-column: 2 / -1;
  color: var(--color-danger);
  font-size: var(--text-sm);
}
@media (max-width: 680px) {
  .catalog-profile {
    grid-template-columns: max-content minmax(0, 1fr);
  }
  .catalog-profile__field,
  .catalog-profile__field:nth-child(2),
  .catalog-profile__field:nth-child(5) {
    grid-column: 1 / -1;
  }
}
</style>
