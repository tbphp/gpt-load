<script setup lang="ts">
import { Plus, Trash2 } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { autoModelDraft, type AutoEntry, type AutoModelDraft } from '@modern/api/auto-model'
import { getGroupWorkspace, groupQueryKey } from '@modern/api/groups'
import {
  AppButton,
  AppFormSection,
  AppIconButton,
  AppNotice,
  AppSearchSelect,
  AppSelect,
  AppTextArea,
  AppTextField,
} from '@modern/components/ui'
import { useApiClient } from '@shared/http/client-context'

const props = defineProps<{
  modelValue: AutoModelDraft
  template?: AutoEntry
  decisionModels: string[]
  disabled?: boolean
  error?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [value: AutoModelDraft] }>()
const { t } = useI18n()
const client = useApiClient()
const groups = useQuery({
  queryKey: groupQueryKey,
  queryFn: ({ signal }) => getGroupWorkspace(client, signal),
})
const modelOptions = computed(() =>
  [...new Set((groups.data.value?.items ?? []).flatMap((group) => group.modelNames))]
    .sort()
    .map((value) => ({ value, label: value })),
)
const decisionModelOptions = computed(() =>
  props.decisionModels.map((value) => ({ value, label: value })),
)
function update(change: (draft: AutoModelDraft) => void) {
  if (props.disabled) return
  const draft = JSON.parse(JSON.stringify(props.modelValue)) as AutoModelDraft
  change(draft)
  emit('update:modelValue', draft)
}
function addEntry() {
  if (!props.template) return
  update((draft) => {
    const entry = autoModelDraft({
      ...draft,
      timeout_seconds: Number(draft.timeout_seconds),
      models: [props.template!],
    }).models[0]!
    entry.id = crypto.randomUUID()
    entry.name = draft.models.some((item) => item.name === 'auto') ? '' : 'auto'
    entry.enabled = true
    entry.presets.forEach((preset) => {
      preset.name = t('autoModel.tiers.' + preset.id)
    })
    draft.models.push(entry)
  })
}
function addPreset(index: number) {
  update((draft) =>
    draft.models[index]!.presets.push({
      id: crypto.randomUUID(),
      name: '',
      model: '',
      description: '',
      parameter_overrides: '[]',
    }),
  )
}
function removePreset(entryIndex: number, presetIndex: number) {
  update((draft) => {
    const entry = draft.models[entryIndex]!
    const removed = entry.presets.splice(presetIndex, 1)[0]
    if (removed?.id === entry.fallback) entry.fallback = entry.presets[0]?.id ?? ''
  })
}
function validRules(value: string): boolean {
  try {
    return Array.isArray(JSON.parse(value))
  } catch {
    return false
  }
}
</script>

<template>
  <div class="modern-auto-model">
    <AppNotice v-if="error" tone="danger">{{ error }}</AppNotice>
    <AppNotice v-if="!decisionModels.length">{{ t('autoModel.decisionModelEmpty') }}</AppNotice>
    <div class="modern-auto-model-grid">
      <AppSearchSelect
        :model-value="modelValue.model"
        :label="t('autoModel.decisionModel')"
        :options="decisionModelOptions"
        :selected-option="{ value: modelValue.model, label: modelValue.model }"
        :description="t('autoModel.decisionModelHint')"
        :disabled="disabled"
        @update:model-value="update((draft) => (draft.model = $event))"
      />
      <AppTextField
        :model-value="modelValue.timeout_seconds"
        inputmode="numeric"
        :label="t('autoModel.timeout')"
        :disabled="disabled"
        @update:model-value="update((draft) => (draft.timeout_seconds = $event))"
      />
    </div>
    <AppFormSection :title="t('autoModel.entries')" :description="t('autoModel.permissionsHint')">
      <template #actions
        ><AppButton :icon="Plus" :disabled="disabled || !template" @click="addEntry">{{
          t('autoModel.addTemplate')
        }}</AppButton></template
      >
      <AppNotice v-if="!modelValue.models.length">{{ t('autoModel.empty') }}</AppNotice>
      <div
        v-for="(entry, index) in modelValue.models"
        :key="entry.id"
        class="modern-auto-model-entry"
      >
        <div class="modern-auto-model-actions">
          <strong>{{ entry.name || t('autoModel.unnamedEntry') }}</strong>
          <AppIconButton
            :icon="Trash2"
            :label="t('autoModel.removeEntry')"
            :disabled="disabled"
            @click="update((draft) => draft.models.splice(index, 1))"
          />
        </div>
        <div class="modern-auto-model-grid">
          <AppTextField
            :model-value="entry.name"
            :label="t('autoModel.entryName')"
            :description="t('autoModel.nameHint')"
            :disabled="disabled"
            @update:model-value="update((draft) => (draft.models[index]!.name = $event))"
          />
          <AppSelect
            :model-value="entry.fallback"
            :label="t('autoModel.fallback')"
            :options="
              entry.presets.map((preset) => ({
                value: preset.id,
                label: preset.name || t('autoModel.unnamed'),
              }))
            "
            :disabled="disabled"
            @update:model-value="update((draft) => (draft.models[index]!.fallback = $event))"
          />
        </div>
        <div
          v-for="(preset, presetIndex) in entry.presets"
          :key="preset.id"
          class="modern-auto-model-preset"
        >
          <div class="modern-auto-model-actions">
            <span>{{ preset.name || t('autoModel.unnamed') }}</span
            ><AppIconButton
              :icon="Trash2"
              :label="t('autoModel.removePreset')"
              :disabled="disabled || entry.presets.length === 1"
              @click="removePreset(index, presetIndex)"
            />
          </div>
          <div class="modern-auto-model-grid">
            <AppTextField
              :model-value="preset.name"
              :label="t('autoModel.presetName')"
              :disabled="disabled"
              @update:model-value="
                update((draft) => (draft.models[index]!.presets[presetIndex]!.name = $event))
              "
            />
            <AppSearchSelect
              :model-value="preset.model"
              :label="t('autoModel.targetModel')"
              :options="modelOptions"
              :selected-option="{ value: preset.model, label: preset.model }"
              :description="t('autoModel.targetHint')"
              :disabled="disabled"
              @update:model-value="
                update((draft) => (draft.models[index]!.presets[presetIndex]!.model = $event))
              "
            />
          </div>
          <AppTextArea
            :model-value="preset.description"
            :label="t('autoModel.criteria')"
            :description="t('autoModel.englishHint')"
            :rows="3"
            :disabled="disabled"
            @update:model-value="
              update((draft) => (draft.models[index]!.presets[presetIndex]!.description = $event))
            "
          />
          <AppTextArea
            :model-value="preset.parameter_overrides"
            :label="t('autoModel.overrides')"
            :description="t('autoModel.overrideHint')"
            :error="validRules(preset.parameter_overrides) ? undefined : t('autoModel.invalidJSON')"
            :rows="4"
            mono
            :disabled="disabled"
            @update:model-value="
              update(
                (draft) =>
                  (draft.models[index]!.presets[presetIndex]!.parameter_overrides = $event),
              )
            "
          />
        </div>
        <AppButton :icon="Plus" :disabled="disabled" @click="addPreset(index)">{{
          t('autoModel.addPreset')
        }}</AppButton>
      </div>
    </AppFormSection>
  </div>
</template>

<style scoped>
.modern-auto-model {
  display: grid;
  gap: var(--modern-space-4);
  width: 100%;
}
.modern-auto-model-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 260px), 1fr));
  gap: var(--modern-space-4);
}
.modern-auto-model-entry,
.modern-auto-model-preset {
  display: grid;
  gap: var(--modern-space-3);
  padding-block: var(--modern-space-4);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-auto-model-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
}
</style>
