<script setup lang="ts">
import { ChevronDown, ChevronRight, Plus, Trash2 } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { createUUID } from '@shared/uuid'
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
const expandedEntries = ref<string[]>([])
const expandedPresets = ref<string[]>([])
const expandedOverrides = ref<string[]>([])
function toggleExpanded(values: string[], id: string) {
  return values.includes(id) ? values.filter((value) => value !== id) : [...values, id]
}
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
    entry.id = createUUID()
    entry.name = draft.models.some((item) => item.name === 'auto') ? '' : 'auto'
    entry.enabled = true
    expandedEntries.value.push(entry.id)
    entry.presets.forEach((preset) => {
      preset.name = t('autoModel.tiers.' + preset.id)
    })
    draft.models.push(entry)
  })
}
function addPreset(index: number) {
  const id = createUUID()
  expandedPresets.value.push(id)
  update((draft) =>
    draft.models[index]!.presets.push({
      id,
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
    <AppFormSection :title="t('autoModel.entries')" compact>
      <template #actions
        ><AppButton :icon="Plus" size="sm" :disabled="disabled || !template" @click="addEntry">{{
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
          <AppButton
            variant="text"
            size="sm"
            :icon="expandedEntries.includes(entry.id) ? ChevronDown : ChevronRight"
            :aria-expanded="expandedEntries.includes(entry.id)"
            :aria-controls="'auto-entry-' + entry.id"
            @click="expandedEntries = toggleExpanded(expandedEntries, entry.id)"
            >{{ entry.name || t('autoModel.unnamedEntry') }} · {{ entry.presets.length }}</AppButton
          >
          <AppIconButton
            :icon="Trash2"
            :label="t('autoModel.removeEntry')"
            :disabled="disabled"
            @click="update((draft) => draft.models.splice(index, 1))"
          />
        </div>
        <div
          v-if="expandedEntries.includes(entry.id)"
          :id="'auto-entry-' + entry.id"
          class="modern-auto-model-fields"
        >
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
              <AppButton
                variant="text"
                size="sm"
                :icon="expandedPresets.includes(preset.id) ? ChevronDown : ChevronRight"
                :aria-expanded="expandedPresets.includes(preset.id)"
                :aria-controls="'auto-preset-' + preset.id"
                @click="expandedPresets = toggleExpanded(expandedPresets, preset.id)"
                >{{ preset.name || t('autoModel.unnamed')
                }}<span v-if="preset.model" class="modern-auto-model-summary">
                  · {{ preset.model }}</span
                ></AppButton
              ><AppIconButton
                :icon="Trash2"
                :label="t('autoModel.removePreset')"
                :disabled="disabled || entry.presets.length === 1"
                @click="removePreset(index, presetIndex)"
              />
            </div>
            <div
              v-if="expandedPresets.includes(preset.id)"
              :id="'auto-preset-' + preset.id"
              class="modern-auto-model-fields"
            >
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
                :rows="2"
                :disabled="disabled"
                @update:model-value="
                  update(
                    (draft) => (draft.models[index]!.presets[presetIndex]!.description = $event),
                  )
                "
              />
              <AppButton
                variant="text"
                size="xs"
                :icon="expandedOverrides.includes(preset.id) ? ChevronDown : ChevronRight"
                :aria-expanded="expandedOverrides.includes(preset.id)"
                @click="expandedOverrides = toggleExpanded(expandedOverrides, preset.id)"
                >{{ t('autoModel.overrides') }}</AppButton
              >
              <AppTextArea
                v-if="
                  expandedOverrides.includes(preset.id) || !validRules(preset.parameter_overrides)
                "
                :model-value="preset.parameter_overrides"
                :label="t('autoModel.overrides')"
                :description="t('autoModel.overrideHint')"
                :error="
                  validRules(preset.parameter_overrides) ? undefined : t('autoModel.invalidJSON')
                "
                :rows="3"
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
          </div>
          <AppButton size="sm" :icon="Plus" :disabled="disabled" @click="addPreset(index)">{{
            t('autoModel.addPreset')
          }}</AppButton>
        </div>
      </div>
    </AppFormSection>
  </div>
</template>

<style scoped>
.modern-auto-model {
  display: grid;
  gap: var(--modern-space-3);
  width: 100%;
}
.modern-auto-model-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 260px), 1fr));
  gap: var(--modern-space-3);
}
.modern-auto-model-entry,
.modern-auto-model-preset {
  display: grid;
  gap: var(--modern-space-3);
  padding-top: var(--modern-space-2);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-auto-model-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
}
.modern-auto-model-fields {
  display: grid;
  gap: var(--modern-space-3);
  min-width: 0;
}
.modern-auto-model-actions > :first-child {
  min-width: 0;
  white-space: normal;
  text-align: left;
  overflow-wrap: anywhere;
}
.modern-auto-model-summary {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  overflow-wrap: anywhere;
}
</style>
