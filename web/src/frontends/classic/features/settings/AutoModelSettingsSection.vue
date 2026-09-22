<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { createUUID } from '@shared/uuid'
import {
  defaultAutoModel,
  type AutoModelConfigDto,
  type AutoEntryDto,
  validAutoModel,
} from '@/app/resources/auto-model'
import type { SettingsResource } from '@/app/resources/settings'
import ExperimentalSettingsFields from './ExperimentalSettingsFields.vue'
import SettingRow from '@/components/config/SettingRow.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import DisclosurePanel from '@/components/ui/DisclosurePanel.vue'
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
const controlsDisabled = computed(
  () => props.disabled || props.draft.readOnly.has('auto_model') || !hasOverride(),
)
function editableEntries(models: AutoEntryDto[]) {
  return models.map((entry) => {
    const editable: Partial<AutoEntryDto> = { ...entry }
    delete editable.enabled
    return editable
  })
}
const entries = ref(JSON.stringify(editableEntries(config.value.models), null, 2))
const entriesError = ref(false)
const experimentalInvalid = ref(false)
const jevInvalid = ref(false)
watch([entriesError, experimentalInvalid, jevInvalid], ([a, b, c]) => emit('invalid', a || b || c))
watch(
  () => [props.base, props.revision],
  () => {
    entries.value = JSON.stringify(editableEntries(config.value.models), null, 2)
    entriesError.value = false
    emit('invalid', experimentalInvalid.value || jevInvalid.value)
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
  emit('invalid', experimentalInvalid.value || jevInvalid.value)
  emit('change', {
    key: 'auto_model',
    draft,
  })
}
function setEnabled(enabled: boolean) {
  if (!enabled && entriesError.value) {
    entries.value = JSON.stringify(editableEntries(config.value.models), null, 2)
    entriesError.value = false
    emit('invalid', experimentalInvalid.value || jevInvalid.value)
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
  emit('invalid', entriesError.value || experimentalInvalid.value || jevInvalid.value)
}
function addTemplate() {
  const template = props.base.settings.auto_model_template
  if (!template) return
  const copy = JSON.parse(JSON.stringify(template)) as AutoEntryDto
  copy.id = createUUID()
  copy.enabled = true
  if (config.value.models.some((entry) => entry.name === copy.name)) copy.name = ''
  copy.presets.forEach((preset) => (preset.name = t('autoModel.tiers.' + preset.id)))
  editEntries(JSON.stringify(editableEntries([...config.value.models, copy]), null, 2))
}
</script>

<template>
  <section
    id="settings-experimental"
    class="settings-section auto-model-section"
    aria-labelledby="settings-experimental-title"
    tabindex="-1"
  >
    <header class="auto-model-section__heading">
      <h2 id="settings-experimental-title">{{ t('autoModel.experimentalSection') }}</h2>
      <p>{{ t('autoModel.experimentalSectionHelp') }}</p>
    </header>
    <ExperimentalSettingsFields
      class="auto-model-feature"
      kind="jev"
      :base="base"
      :draft="draft"
      :disabled="disabled"
      :revision="revision"
      @change="emit('change', $event)"
      @invalid="jevInvalid = $event"
    />
    <div class="auto-model-feature">
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
        :locked="draft.readOnly.has('auto_model')"
        :disabled="disabled"
        :divided="false"
        @toggle="toggleOverride"
      >
        <template #control>
          <AppSwitch
            :model-value="config.enabled"
            :label="t('autoModel.enabled')"
            :disabled="controlsDisabled"
            @update:model-value="setEnabled"
          />
        </template>
      </SettingRow>
      <div v-if="config.enabled" class="auto-model-fields">
        <p v-if="!base.settings.decision_models.length" class="auto-model-empty">
          {{ t('autoModel.decisionModelEmpty') }}
        </p>
        <div class="auto-model-heading">
          <h3>{{ t('autoModel.entries') }} · {{ config.models.length }}</h3>
          <AppButton
            variant="secondary"
            size="compact"
            :disabled="controlsDisabled || entriesError || !base.settings.auto_model_template"
            @click="addTemplate"
            >{{ t('autoModel.addTemplate') }}</AppButton
          >
        </div>
        <DisclosurePanel
          class="auto-model-details"
          :summary="t('autoModel.entries') + ' · JSON'"
          :open="entriesError"
        >
          <div class="auto-model-editor">
            <FormField
              id="auto-entries"
              :label="t('autoModel.entries')"
              :error="entriesError ? t('autoModel.invalidJSON') : undefined"
              size="compact"
              label-hidden
            >
              <textarea
                id="auto-entries"
                class="auto-model-json"
                :value="entries"
                rows="8"
                :disabled="controlsDisabled"
                spellcheck="false"
                @input="editEntries(($event.target as HTMLTextAreaElement).value)"
              />
            </FormField>
            <div class="auto-model-help">
              <p>{{ t('autoModel.permissionsHint') }}</p>
              <p>{{ t('autoModel.englishHint') }}</p>
              <p>{{ t('autoModel.overrideHint') }}</p>
            </div>
          </div>
        </DisclosurePanel>
      </div>
    </div>
    <ExperimentalSettingsFields
      class="auto-model-feature"
      kind="request_audit"
      :base="base"
      :draft="draft"
      :disabled="disabled"
      :revision="revision"
      @change="emit('change', $event)"
      @invalid="experimentalInvalid = $event"
    />
  </section>
</template>

<style scoped>
.auto-model-section,
.auto-model-section__heading,
.auto-model-fields,
.auto-model-editor {
  display: grid;
  min-width: 0;
  gap: var(--space-3);
}
.auto-model-section {
  grid-template-columns: minmax(0, 1fr);
  gap: var(--space-4);
  scroll-margin-top: 76px;
}
.auto-model-section__heading {
  gap: var(--space-1);
}
.auto-model-section__heading h2,
.auto-model-section__heading p {
  margin: 0;
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
.auto-model-heading {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}
.auto-model-editor .auto-model-json {
  box-sizing: border-box;
  width: 100%;
  min-height: 160px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  padding: var(--space-3);
  color: var(--color-text);
  background: var(--color-surface);
  font: inherit;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  line-height: 1.6;
  resize: vertical;
}
.auto-model-feature {
  display: grid;
  min-width: 0;
  gap: var(--space-3);
}
.auto-model-feature + .auto-model-feature {
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-3);
}
.auto-model-fields {
  padding-inline: var(--space-3);
}
.auto-model-details {
  border-top: 0;
  padding-top: 0;
}
.auto-model-help {
  display: grid;
  gap: var(--space-1);
}
.auto-model-fields p {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--color-text-muted);
  line-height: 1.5;
}
.auto-model-fields .auto-model-empty {
  color: var(--color-warning);
}
.auto-model-heading h3 {
  margin: 0;
  font-size: var(--text-sm);
}
</style>
