<script setup lang="ts">
import { RotateCcw } from '@lucide/vue'
import { DialogRoot } from 'reka-ui'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  modelReasoningLevels,
  type ModelInputModality,
  type ModelProfileField as ModelProfileFieldName,
} from '@modern/api/models'
import AppDraftGuard from '@modern/components/AppDraftGuard.vue'
import {
  AppBadge,
  AppButton,
  AppCheckbox,
  AppCollectionState,
  AppDialogContent,
  AppDialogHeader,
  AppFormSection,
  AppMultiSelect,
  AppNotice,
  AppSelect,
  AppSwitch,
  AppTextArea,
  AppTextField,
} from '@modern/components/ui'
import { useModelProfileEditor } from './use-model-profile-editor'
import ModelProfileField from './ModelProfileField.vue'

const props = defineProps<{ model: string; editable: boolean }>()
const emit = defineEmits<{ close: [] }>()
const { t, n } = useI18n()
const guard = ref<InstanceType<typeof AppDraftGuard>>()
const {
  query,
  base,
  draft,
  dirty,
  customCount,
  saving,
  saveError,
  fieldErrors,
  setCustom,
  resetAll,
  save,
} = useModelProfileEditor(() => props.model)
const disabled = computed(() => saving.value || !props.editable)
const reasoningOptions = computed(() =>
  modelReasoningLevels.map((value) => ({
    value,
    label: t(`modelManager.profile.reasoningLevels.${value}`),
  })),
)
const defaultReasoningOptions = computed(() => {
  const supported = draft.value?.custom.supported_reasoning_levels
    ? draft.value.values.supported_reasoning_levels
    : []
  return [
    { value: '', label: t('modelManager.profile.noDefault') },
    ...reasoningOptions.value.map((option) => ({
      ...option,
      disabled: !supported.includes(option.value),
    })),
  ]
})

function automaticValue(field: ModelProfileFieldName): string {
  const automatic = base.value?.automatic
  if (!automatic) return ''
  switch (field) {
    case 'display_name':
    case 'description':
      return automatic[field] || t('modelManager.profile.unset')
    case 'context_window':
      return automatic.context_window === null
        ? t('modelManager.profile.unknownContext')
        : n(automatic.context_window)
    case 'supported_reasoning_levels':
      return automatic.supported_reasoning_levels.length
        ? automatic.supported_reasoning_levels
            .map((value) => t(`modelManager.profile.reasoningLevels.${value}`))
            .join(' / ')
        : t('modelManager.profile.none')
    case 'default_reasoning_level':
      return automatic.default_reasoning_level
        ? t(`modelManager.profile.reasoningLevels.${automatic.default_reasoning_level}`)
        : t('modelManager.profile.noDefault')
    case 'input_modalities':
      return automatic.input_modalities
        .map((value) => t(`modelManager.modality.${value}`))
        .join(' / ')
    case 'supports_reasoning_summary':
    case 'support_verbosity':
      return automatic[field]
        ? t('modelManager.profile.enabled')
        : t('modelManager.profile.disabled')
  }
  return ''
}

function toggleInputModality(value: Exclude<ModelInputModality, 'text'>, enabled: boolean): void {
  if (!draft.value) return
  const values = new Set(draft.value.values.input_modalities)
  if (enabled) values.add(value)
  else values.delete(value)
  values.add('text')
  draft.value.values.input_modalities = ['text', 'image', 'audio'].filter((modality) =>
    values.has(modality as ModelInputModality),
  ) as ModelInputModality[]
}

async function close(): Promise<void> {
  if (!saving.value && (await guard.value?.confirm())) emit('close')
}
</script>

<template>
  <DialogRoot :open="true" @update:open="!$event && close()">
    <AppDialogContent
      placement="editor"
      size="sheet"
      :title="t('modelManager.profile.title')"
      :description="model"
      @escape-key-down="
        (event: Event) => {
          if (saving) event.preventDefault()
        }
      "
      @interact-outside="
        (event: Event) => {
          if (saving) event.preventDefault()
        }
      "
    >
      <AppDialogHeader
        :title="t('modelManager.profile.title')"
        :description="model"
        :close-label="t('ui.close')"
        :close-disabled="saving"
        @close="close"
      />
      <AppCollectionState
        v-if="!base"
        :loading="query.isPending.value"
        :error="query.isError.value"
        :title="
          t(query.isPending.value ? 'modelManager.profile.loading' : 'modelManager.profile.failed')
        "
      >
        <AppButton v-if="query.isError.value" @click="query.refetch()">{{
          t('ui.retry')
        }}</AppButton>
      </AppCollectionState>
      <form v-else-if="draft" class="modern-model-profile-form" novalidate @submit.prevent="save">
        <div class="modern-model-profile-body">
          <div class="modern-model-profile-summary">
            <AppBadge variant="outline" size="xs">{{
              t('modelManager.profile.sourceCount', { count: n(base.sourceCount) })
            }}</AppBadge>
            <AppBadge v-if="base.unknownSourceCount" tone="warning" variant="outline" size="xs">{{
              t('modelManager.profile.unknownSourceCount', { count: n(base.unknownSourceCount) })
            }}</AppBadge>
          </div>
          <AppNotice tone="info">
            <div class="modern-model-profile-notice-copy">
              <p>{{ t('modelManager.profile.scopeNotice') }}</p>
              <p>{{ t('modelManager.profile.capabilityNotice') }}</p>
            </div>
          </AppNotice>
          <AppNotice v-if="!editable" tone="warning">
            {{ t('modelManager.profile.readOnly') }}
          </AppNotice>
          <AppNotice v-if="query.isError.value" tone="warning">
            {{ t('modelManager.profile.stale') }}
          </AppNotice>
          <AppNotice v-if="saveError" tone="danger">{{ saveError }}</AppNotice>
          <AppFormSection
            :title="t('modelManager.profile.sections.metadata')"
            :description="t('modelManager.profile.sections.metadataHelp')"
            compact
          >
            <ModelProfileField
              :label="t('modelManager.profile.fields.displayName')"
              :automatic="automaticValue('display_name')"
              :custom="draft.custom.display_name"
              :disabled="disabled"
              @update:custom="setCustom('display_name', $event)"
            >
              <template #default="{ disabled: fieldDisabled }">
                <AppTextField
                  v-model="draft.values.display_name"
                  :label="t('modelManager.profile.fields.displayName')"
                  label-hidden
                  size="sm"
                  :disabled="fieldDisabled"
                />
              </template>
            </ModelProfileField>
            <ModelProfileField
              :label="t('modelManager.profile.fields.description')"
              :automatic="automaticValue('description')"
              :custom="draft.custom.description"
              :disabled="disabled"
              @update:custom="setCustom('description', $event)"
            >
              <template #default="{ disabled: fieldDisabled }">
                <AppTextArea
                  v-model="draft.values.description"
                  :label="t('modelManager.profile.fields.description')"
                  label-hidden
                  size="sm"
                  :rows="3"
                  :disabled="fieldDisabled"
                />
              </template>
            </ModelProfileField>
            <ModelProfileField
              :label="t('modelManager.profile.fields.contextWindow')"
              :description="t('modelManager.profile.fieldHelp.contextWindow')"
              :automatic="automaticValue('context_window')"
              :custom="draft.custom.context_window"
              :disabled="disabled"
              :error="fieldErrors.context_window"
              @update:custom="setCustom('context_window', $event)"
            >
              <template #default="{ disabled: fieldDisabled }">
                <AppTextField
                  v-model="draft.values.context_window"
                  :label="t('modelManager.profile.fields.contextWindow')"
                  label-hidden
                  size="sm"
                  type="number"
                  min="1"
                  step="1"
                  inputmode="numeric"
                  :placeholder="t('modelManager.profile.contextPlaceholder')"
                  :disabled="fieldDisabled"
                />
              </template>
            </ModelProfileField>
          </AppFormSection>
          <AppFormSection
            :title="t('modelManager.profile.sections.reasoning')"
            :description="t('modelManager.profile.sections.reasoningHelp')"
            compact
          >
            <ModelProfileField
              :label="t('modelManager.profile.fields.supportedReasoningLevels')"
              :automatic="automaticValue('supported_reasoning_levels')"
              :custom="draft.custom.supported_reasoning_levels"
              :disabled="disabled"
              @update:custom="setCustom('supported_reasoning_levels', $event)"
            >
              <template #default="{ disabled: fieldDisabled }">
                <AppMultiSelect
                  v-model="draft.values.supported_reasoning_levels"
                  :label="t('modelManager.profile.fields.supportedReasoningLevels')"
                  label-hidden
                  :options="reasoningOptions"
                  :disabled="fieldDisabled"
                />
              </template>
            </ModelProfileField>
            <ModelProfileField
              :label="t('modelManager.profile.fields.defaultReasoningLevel')"
              :description="t('modelManager.profile.fieldHelp.defaultReasoningLevel')"
              :automatic="automaticValue('default_reasoning_level')"
              :custom="draft.custom.default_reasoning_level"
              :disabled="disabled"
              :error="fieldErrors.default_reasoning_level"
              @update:custom="setCustom('default_reasoning_level', $event)"
            >
              <template #default="{ disabled: fieldDisabled }">
                <AppSelect
                  v-model="draft.values.default_reasoning_level"
                  :label="t('modelManager.profile.fields.defaultReasoningLevel')"
                  label-hidden
                  size="sm"
                  :options="defaultReasoningOptions"
                  :disabled="fieldDisabled"
                />
              </template>
            </ModelProfileField>
            <ModelProfileField
              :label="t('modelManager.profile.fields.supportsReasoningSummary')"
              :automatic="automaticValue('supports_reasoning_summary')"
              :custom="draft.custom.supports_reasoning_summary"
              :disabled="disabled"
              @update:custom="setCustom('supports_reasoning_summary', $event)"
            >
              <template #default="{ disabled: fieldDisabled }">
                <AppSwitch
                  :model-value="draft.values.supports_reasoning_summary"
                  :label="t('modelManager.profile.fields.supportsReasoningSummary')"
                  size="sm"
                  :disabled="fieldDisabled"
                  @update:model-value="draft.values.supports_reasoning_summary = $event"
                />
              </template>
            </ModelProfileField>
            <ModelProfileField
              :label="t('modelManager.profile.fields.supportVerbosity')"
              :automatic="automaticValue('support_verbosity')"
              :custom="draft.custom.support_verbosity"
              :disabled="disabled"
              @update:custom="setCustom('support_verbosity', $event)"
            >
              <template #default="{ disabled: fieldDisabled }">
                <AppSwitch
                  :model-value="draft.values.support_verbosity"
                  :label="t('modelManager.profile.fields.supportVerbosity')"
                  size="sm"
                  :disabled="fieldDisabled"
                  @update:model-value="draft.values.support_verbosity = $event"
                />
              </template>
            </ModelProfileField>
          </AppFormSection>
          <AppFormSection
            :title="t('modelManager.profile.sections.modalities')"
            :description="t('modelManager.profile.sections.modalitiesHelp')"
            compact
          >
            <ModelProfileField
              :label="t('modelManager.profile.fields.inputModalities')"
              :description="t('modelManager.profile.fieldHelp.inputModalities')"
              :automatic="automaticValue('input_modalities')"
              :custom="draft.custom.input_modalities"
              :disabled="disabled"
              :error="fieldErrors.input_modalities"
              @update:custom="setCustom('input_modalities', $event)"
            >
              <template #default="{ disabled: fieldDisabled }">
                <div class="modern-model-profile-modalities">
                  <AppCheckbox
                    :model-value="true"
                    :label="t('modelManager.modality.text')"
                    disabled
                  />
                  <AppCheckbox
                    :model-value="draft.values.input_modalities.includes('image')"
                    :label="t('modelManager.modality.image')"
                    :disabled="fieldDisabled"
                    @update:model-value="toggleInputModality('image', $event)"
                  />
                  <AppCheckbox
                    :model-value="draft.values.input_modalities.includes('audio')"
                    :label="t('modelManager.modality.audio')"
                    :disabled="fieldDisabled"
                    @update:model-value="toggleInputModality('audio', $event)"
                  />
                </div>
              </template>
            </ModelProfileField>
          </AppFormSection>
        </div>
        <footer class="modern-model-profile-footer">
          <AppButton
            variant="text"
            size="sm"
            :icon="RotateCcw"
            :disabled="disabled || !customCount"
            @click="resetAll"
          >
            {{ t('modelManager.profile.resetAll') }}
          </AppButton>
          <div class="modern-model-profile-actions">
            <AppButton size="sm" :disabled="saving" @click="close">{{ t('ui.cancel') }}</AppButton>
            <AppButton
              type="submit"
              variant="primary"
              size="sm"
              :loading="saving"
              :disabled="disabled || !dirty"
              >{{ t('modelManager.profile.save') }}</AppButton
            >
          </div>
        </footer>
      </form>
    </AppDialogContent>
  </DialogRoot>
  <AppDraftGuard ref="guard" :dirty="dirty" :pending="saving" />
</template>

<style scoped>
.modern-model-profile-form {
  display: flex;
  flex: 1;
  min-height: 0;
  flex-direction: column;
}
.modern-model-profile-body {
  display: grid;
  align-content: start;
  gap: var(--modern-space-5);
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: var(--modern-space-5);
}
.modern-model-profile-summary,
.modern-model-profile-modalities,
.modern-model-profile-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}
.modern-model-profile-notice-copy {
  display: grid;
  gap: var(--modern-space-1);
}
.modern-model-profile-footer {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding: var(--modern-space-4) var(--modern-space-5);
}
@media (max-width: 760px) {
  .modern-model-profile-footer {
    align-items: stretch;
    flex-direction: column-reverse;
  }
  .modern-model-profile-actions > * {
    flex: 1;
  }
}
</style>
