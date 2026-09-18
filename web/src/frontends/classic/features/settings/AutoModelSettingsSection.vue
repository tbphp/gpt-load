<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  defaultAutoModel,
  type AutoModelConfigDto,
  type AutoEntryDto,
} from '@/app/resources/auto-model'
import type { SettingsResource } from '@/app/resources/settings'
import AppButton from '@/components/ui/AppButton.vue'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import FormField from '@/components/ui/FormField.vue'
import { createSettingsDraft, type SettingsDraft } from './settings-patch'
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
const entries = ref(JSON.stringify(config.value.models, null, 2))
const entriesError = ref(false)
watch(
  () => [props.base, props.revision],
  () => {
    entries.value = JSON.stringify(config.value.models, null, 2)
    entriesError.value = false
    emit('invalid', false)
  },
)
function update(change: (value: AutoModelConfigDto) => void) {
  if (props.disabled) return
  const draft = createSettingsDraft({
    values: props.draft.values,
    overrides: [...props.draft.overrides],
    read_only: [...props.draft.readOnly],
  })
  draft.values.auto_model ??= defaultAutoModel()
  change(draft.values.auto_model)
  draft.overrides.add('auto_model')
  emit('change', { key: 'auto_model', draft })
}
function provider(value: string) {
  update((draft) => {
    draft.provider = value === 'openrouter' ? 'openrouter' : 'typesafe'
    draft.model = draft.provider === 'openrouter' ? '~typesafe/jev-latest' : 'jev-latest'
    draft.api_key = ''
    draft.api_key_configured = false
    draft.input_price = '0.042'
    draft.output_price = '0'
  })
}
function editEntries(value: string) {
  entries.value = value
  try {
    const parsed: unknown = JSON.parse(value)
    if (!Array.isArray(parsed)) throw new Error('array required')
    update((draft) => (draft.models = parsed as AutoEntryDto[]))
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
  if (config.value.models.some((entry) => entry.name === copy.name)) copy.name = ''
  copy.presets.forEach((preset) => (preset.name = t('autoModel.tiers.' + preset.id)))
  editEntries(JSON.stringify([...config.value.models, copy], null, 2))
}
</script>

<template>
  <section id="settings-auto-models" class="settings-section" tabindex="-1">
    <header class="settings-section__heading">
      <h2>{{ t('autoModel.title') }}</h2>
      <p>{{ t('autoModel.experimental') }}</p>
    </header>
    <div class="auto-model-fields">
      <AppSwitch
        :model-value="config.enabled"
        :label="t('autoModel.enabled')"
        :disabled="disabled"
        @update:model-value="update((value) => (value.enabled = $event))"
      />
      <div class="auto-model-grid">
        <FormField id="auto-provider" :label="t('autoModel.provider')"
          ><AppSelect
            id="auto-provider"
            :model-value="config.provider"
            :label="t('autoModel.provider')"
            :options="[
              { value: 'typesafe', label: t('autoModel.official') },
              { value: 'openrouter', label: 'OpenRouter' },
            ]"
            :disabled="disabled"
            @update:model-value="provider"
        /></FormField>
        <FormField id="auto-model" :label="t('autoModel.decisionModel')"
          ><AppTextInput
            id="auto-model"
            :label="t('autoModel.decisionModel')"
            :model-value="config.model"
            :disabled="disabled"
            @update:model-value="update((value) => (value.model = $event))"
        /></FormField>
        <FormField
          id="auto-key"
          :label="t('autoModel.apiKey')"
          :description="t('autoModel.keyHint')"
          ><AppTextInput
            id="auto-key"
            :label="t('autoModel.apiKey')"
            :model-value="config.api_key"
            type="password"
            autocomplete="new-password"
            :placeholder="config.api_key_configured ? t('autoModel.keepKey') : ''"
            :disabled="disabled"
            @update:model-value="update((value) => (value.api_key = $event))"
        /></FormField>
        <FormField id="auto-timeout" :label="t('autoModel.timeout')"
          ><AppTextInput
            id="auto-timeout"
            :label="t('autoModel.timeout')"
            :model-value="String(config.timeout_seconds)"
            inputmode="numeric"
            :disabled="disabled"
            @update:model-value="update((value) => (value.timeout_seconds = Number($event)))"
        /></FormField>
        <FormField
          id="auto-confidence"
          :label="t('autoModel.confidence')"
          :description="t('autoModel.confidenceHint')"
          ><AppTextInput
            id="auto-confidence"
            :label="t('autoModel.confidence')"
            :model-value="String(config.min_confidence)"
            inputmode="decimal"
            :disabled="disabled"
            @update:model-value="update((value) => (value.min_confidence = Number($event)))"
        /></FormField>
      </div>
      <h3>{{ t('autoModel.pricing') }}</h3>
      <p>{{ t('autoModel.pricingHint') }}</p>
      <div class="auto-model-grid">
        <FormField id="auto-input-price" :label="t('autoModel.inputPrice')"
          ><AppTextInput
            id="auto-input-price"
            :label="t('autoModel.inputPrice')"
            :model-value="config.input_price"
            inputmode="decimal"
            :disabled="disabled"
            @update:model-value="update((value) => (value.input_price = $event))"
        /></FormField>
        <FormField id="auto-output-price" :label="t('autoModel.outputPrice')"
          ><AppTextInput
            id="auto-output-price"
            :label="t('autoModel.outputPrice')"
            :model-value="config.output_price"
            inputmode="decimal"
            :disabled="disabled"
            @update:model-value="update((value) => (value.output_price = $event))"
        /></FormField>
      </div>
      <div class="auto-model-heading">
        <h3>{{ t('autoModel.entries') }}</h3>
        <AppButton
          variant="secondary"
          :disabled="disabled || entriesError || !base.settings.auto_model_template"
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
          :disabled="disabled"
          @input="editEntries(($event.target as HTMLTextAreaElement).value)"
        />
      </FormField>
    </div>
  </section>
</template>

<style scoped>
.auto-model-fields {
  display: grid;
  gap: var(--space-4);
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
