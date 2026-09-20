<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  defaultAutoModel,
  type AutoModelConfigDto,
  type AutoEntryDto,
  validAutoModel,
} from '@/app/resources/auto-model'
import type { SettingsResource } from '@/app/resources/settings'
import SettingRow from '@/components/config/SettingRow.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppCombobox from '@/components/ui/AppCombobox.vue'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import FormField from '@/components/ui/FormField.vue'
import { createSettingsDraft, setSettingsOverride, type SettingsDraft } from './settings-patch'
import type { SettingsDraftChange } from './use-settings-controller'

const props = defineProps<{
  base: SettingsResource
  draft: SettingsDraft
  disabled: boolean
  revision: number
}>()
const emit = defineEmits<{ change: [value: SettingsDraftChange]; invalid: [value: boolean] }>()
const { t } = useI18n()
const config = computed(() => props.draft.values.auto_model ?? defaultAutoModel())
const decisionModelOptions = computed(() =>
  props.base.settings.decision_models.map((value) => ({ value, label: value })),
)
const controlsDisabled = computed(() => props.disabled || !hasOverride())
function editableEntries(models: AutoEntryDto[]) {
  return models.map((entry) => {
    const editable: Partial<AutoEntryDto> = { ...entry }
    delete editable.enabled
    return editable
  })
}
const entries = ref(JSON.stringify(editableEntries(config.value.models), null, 2))
const entriesError = ref(false)
watch(
  () => [props.base, props.revision],
  () => {
    entries.value = JSON.stringify(editableEntries(config.value.models), null, 2)
    entriesError.value = false
    emit('invalid', false)
  },
)
function update(change: (value: AutoModelConfigDto) => void) {
  if (controlsDisabled.value) return
  const draft = createSettingsDraft({
    values: props.draft.values,
    overrides: [...props.draft.overrides],
    read_only: [...props.draft.readOnly],
  })
  draft.values.auto_model ??= defaultAutoModel()
  change(draft.values.auto_model)
  draft.values.auto_model.models.forEach((entry) => (entry.enabled = true))
  draft.overrides.add('auto_model')
  emit('change', { key: 'auto_model', draft })
}
function hasOverride(): boolean {
  return props.draft.overrides.has('auto_model')
}
function isPendingRestore(): boolean {
  return !hasOverride() && props.base.settings.overrides.includes('auto_model')
}
function sourceLabel(): string {
  if (hasOverride()) return t('settings.runtime.overrideSource')
  if (isPendingRestore()) return t('settings.runtime.pendingRestoreSource')
  return t('settings.runtime.defaultSource')
}
function actionLabel(): string {
  return hasOverride() ? t('settings.runtime.restoreDefault') : t('settings.runtime.override')
}
function toggleOverride() {
  const draft = setSettingsOverride(props.base.settings, props.draft, 'auto_model', !hasOverride())
  entries.value = JSON.stringify(editableEntries(draft.values.auto_model?.models ?? []), null, 2)
  entriesError.value = false
  emit('invalid', false)
  emit('change', {
    key: 'auto_model',
    draft,
  })
}
function setEnabled(enabled: boolean) {
  if (!enabled && entriesError.value) {
    entries.value = JSON.stringify(editableEntries(config.value.models), null, 2)
    entriesError.value = false
    emit('invalid', false)
  }
  if (!enabled && !validAutoModel(config.value)) {
    const fallback = JSON.parse(
      JSON.stringify(props.base.settings.values.auto_model ?? defaultAutoModel()),
    ) as AutoModelConfigDto
    update((draft) => Object.assign(draft, fallback, { enabled: false }))
    return
  }
  update((draft) => (draft.enabled = enabled))
}
function editEntries(value: string) {
  entries.value = value
  try {
    const parsed: unknown = JSON.parse(value)
    if (
      !Array.isArray(parsed) ||
      parsed.some((entry) => typeof entry !== 'object' || entry === null || Array.isArray(entry))
    )
      throw new Error('array required')
    update(
      (draft) =>
        (draft.models = parsed.map((entry) => ({ ...entry, enabled: true })) as AutoEntryDto[]),
    )
    entriesError.value = false
  } catch {
    entriesError.value = true
  }
  emit('invalid', entriesError.value)
}
function addTemplate() {
  const template = props.base.settings.auto_model_template
  if (!template) return
  const copy = JSON.parse(JSON.stringify(template)) as AutoEntryDto
  copy.id = crypto.randomUUID()
  copy.enabled = true
  if (config.value.models.some((entry) => entry.name === copy.name)) copy.name = ''
  copy.presets.forEach((preset) => (preset.name = t('autoModel.tiers.' + preset.id)))
  editEntries(JSON.stringify(editableEntries([...config.value.models, copy]), null, 2))
}
</script>

<template>
  <section
    id="settings-experimental"
    class="auto-model-section"
    aria-labelledby="settings-experimental-title"
    tabindex="-1"
  >
    <header class="auto-model-section__heading">
      <h2 id="settings-experimental-title">{{ t('autoModel.experimentalSection') }}</h2>
      <p>{{ t('autoModel.experimentalSectionHelp') }}</p>
    </header>
    <SettingRow
      :label="t('autoModel.title')"
      :value="
        isPendingRestore()
          ? t('settings.runtime.resetPending')
          : t(config.enabled ? 'settings.runtime.enabled' : 'settings.runtime.disabled')
      "
      :help="t('autoModel.experimental')"
      :source-label="sourceLabel()"
      :action-label="actionLabel()"
      :overridden="hasOverride()"
      :pending-restore="isPendingRestore()"
      :disabled="disabled"
      :divided="false"
      @toggle="toggleOverride"
    >
      <template #control>
        <AppSwitch
          :model-value="config.enabled"
          :label="t('autoModel.enabled')"
          :disabled="disabled"
          @update:model-value="setEnabled"
        />
      </template>
    </SettingRow>
    <div v-if="config.enabled" class="auto-model-fields">
      <p v-if="!base.settings.decision_models.length" class="auto-model-empty">
        {{ t('autoModel.decisionModelEmpty') }}
      </p>
      <div class="auto-model-grid">
        <FormField
          id="auto-model"
          :label="t('autoModel.decisionModel')"
          :description="t('autoModel.decisionModelHint')"
          ><AppCombobox
            id="auto-model"
            :label="t('autoModel.decisionModel')"
            :model-value="config.model"
            :options="decisionModelOptions"
            :empty-text="t('autoModel.decisionModelEmpty')"
            :disabled="controlsDisabled"
            @update:model-value="update((value) => (value.model = $event))"
        /></FormField>
        <FormField id="auto-timeout" :label="t('autoModel.timeout')"
          ><AppTextInput
            id="auto-timeout"
            :label="t('autoModel.timeout')"
            :model-value="String(config.timeout_seconds)"
            inputmode="numeric"
            :disabled="controlsDisabled"
            @update:model-value="update((value) => (value.timeout_seconds = Number($event)))"
        /></FormField>
      </div>
      <div class="auto-model-heading">
        <h3>{{ t('autoModel.entries') }}</h3>
        <AppButton
          variant="secondary"
          :disabled="controlsDisabled || entriesError || !base.settings.auto_model_template"
          @click="addTemplate"
          >{{ t('autoModel.addTemplate') }}</AppButton
        >
      </div>
      <p>{{ t('autoModel.permissionsHint') }}</p>
      <p>{{ t('autoModel.englishHint') }}</p>
      <p>{{ t('autoModel.overrideHint') }}</p>
      <FormField
        id="auto-entries"
        :label="t('autoModel.entries')"
        :error="entriesError ? t('autoModel.invalidJSON') : undefined"
      >
        <textarea
          id="auto-entries"
          class="auto-model-json"
          :value="entries"
          rows="18"
          :disabled="controlsDisabled"
          @input="editEntries(($event.target as HTMLTextAreaElement).value)"
        />
      </FormField>
    </div>
  </section>
</template>

<style scoped>
.auto-model-section,
.auto-model-section__heading,
.auto-model-fields {
  display: grid;
  gap: var(--space-4);
}
.auto-model-section {
  scroll-margin-top: 76px;
}
.auto-model-section__heading h2,
.auto-model-section__heading p {
  margin: 0;
}
.auto-model-empty {
  margin: 0;
  color: var(--color-warning);
  font-size: var(--text-sm);
}
.auto-model-section__heading h2 {
  font-size: var(--title-section);
  font-weight: 650;
}
.auto-model-section__heading p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}
.auto-model-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 260px), 1fr));
  gap: var(--space-4);
}
.auto-model-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}
.auto-model-json {
  width: 100%;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  padding: var(--space-3);
  color: var(--color-text);
  background: var(--color-surface);
  font: inherit;
  font-family: var(--font-mono);
  resize: vertical;
}
</style>
