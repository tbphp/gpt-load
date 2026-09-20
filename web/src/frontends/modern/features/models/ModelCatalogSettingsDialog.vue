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
  AppButton,
  AppCheckbox,
  AppCollectionState,
  AppDialogContent,
  AppDialogHeader,
  AppFormSection,
  AppMultiSelect,
  AppNotice,
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
    label: value,
  })),
)
function automaticValue(field: ModelProfileFieldName): string {
  const automatic = base.value?.automatic
  if (!automatic) return ''
  switch (field) {
    case 'display_name':
      return automatic.display_name
    case 'context_window':
      return automatic.context_window === null
        ? t('modelManager.profile.unknownContext')
        : n(automatic.context_window)
    case 'supported_reasoning_levels':
      return automatic.supported_reasoning_levels.length
        ? automatic.supported_reasoning_levels.join(' / ')
        : t('modelManager.profile.none')
    case 'input_modalities':
      return automatic.input_modalities
        .map((value) => t(`modelManager.modality.${value}`))
        .join(' / ')
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
                  type="text"
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
              :error="fieldErrors.supported_reasoning_levels"
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
.modern-model-profile-modalities,
.modern-model-profile-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
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
