<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ChannelParamsDto } from '@/api/control/types'
import type { ChannelFieldDto } from '@/app/resources/channels'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'

const props = defineProps<{
  section: 'general' | 'routing'
  channelId: string
  channelName: string
  paramFields: ChannelFieldDto[]
  params: ChannelParamsDto
  name: string
  validationModel: string | null
  weightManual: number | null
  enabled: boolean
  pending: boolean
  paramsDisabled?: boolean
  nameError: string
  paramErrors: Record<string, string>
}>()
const emit = defineEmits<{
  'update:param': [key: string, value: string | null]
  'update:name': [value: string]
  'update:validationModel': [value: string | null]
  'update:weightManual': [value: number | null]
  'update:enabled': [value: boolean]
}>()
const { t } = useI18n()
const weightMode = computed(() => (props.weightManual === null ? 'auto' : 'manual'))
const weightModes = computed(() => [
  { value: 'auto', label: t('group.settings.base.auto'), disabled: props.pending },
  { value: 'manual', label: t('group.settings.base.manual'), disabled: props.pending },
])
const weightValid = computed(
  () =>
    props.weightManual === null ||
    (Number.isInteger(props.weightManual) && props.weightManual >= 1 && props.weightManual <= 100),
)
const baseUrlOverrideEnabled = ref(false)

watch(
  () => props.channelId,
  () => {
    baseUrlOverrideEnabled.value = Boolean(props.params.base_url?.trim())
  },
  { immediate: true },
)

watch(
  () => props.params.base_url,
  (value) => {
    if (value?.trim()) baseUrlOverrideEnabled.value = true
  },
)

function isOptionalBaseURL(field: ChannelFieldDto): boolean {
  return field.key === 'base_url' && !field.required
}

function setBaseURLOverride(enabled: boolean): void {
  baseUrlOverrideEnabled.value = enabled
  if (!enabled) emit('update:param', 'base_url', null)
}

function updateParam(field: ChannelFieldDto, value: string): void {
  if (isOptionalBaseURL(field) && !value.trim()) {
    baseUrlOverrideEnabled.value = false
    emit('update:param', field.key, null)
    return
  }
  emit('update:param', field.key, value)
}

function setWeightMode(value: string): void {
  if (props.pending) return
  emit('update:weightManual', value === 'auto' ? null : (props.weightManual ?? 50))
}
</script>

<template>
  <section v-if="section === 'general'" id="settings-general" class="group-settings__section">
    <header class="group-settings__section-heading">
      <h3>{{ t('group.settings.sections.general') }}</h3>
      <p>{{ t('group.settings.base.description') }}</p>
    </header>
    <div class="group-settings__grid">
      <div class="group-settings__field">
        <span>{{ t('group.settings.base.channel') }}</span>
        <div class="group-settings__readonly" :aria-label="t('group.settings.base.channel')">
          <strong>{{ channelName }}</strong>
          <code>{{ channelId }}</code>
        </div>
        <small>{{ t('group.settings.base.channelHelp') }}</small>
      </div>
      <label class="group-settings__field">
        <span>{{ t('group.settings.base.name') }}</span>
        <input
          :value="name"
          :disabled="pending"
          :aria-invalid="nameError ? 'true' : undefined"
          @input="emit('update:name', ($event.target as HTMLInputElement).value)"
        />
        <small v-if="nameError" role="alert">{{ nameError }}</small>
      </label>
      <label class="group-settings__field">
        <span>{{ t('group.settings.base.validationModel') }}</span>
        <input
          class="group-settings__mono"
          :value="validationModel ?? ''"
          :disabled="pending"
          @input="emit('update:validationModel', ($event.target as HTMLInputElement).value || null)"
        />
      </label>
      <template v-for="field in paramFields" :key="field.key">
        <div v-if="isOptionalBaseURL(field)" class="group-settings__field group-settings__wide">
          <span>{{ t('group.settings.base.customUrl') }}</span>
          <div class="group-settings__base-url-switch">
            <small>{{ t('group.settings.base.customUrlHelp') }}</small>
            <AppSwitch
              :model-value="baseUrlOverrideEnabled"
              :disabled="pending || paramsDisabled"
              :label="t('group.settings.base.customUrl')"
              @update:model-value="setBaseURLOverride"
            />
          </div>
        </div>
        <label
          v-if="!isOptionalBaseURL(field) || baseUrlOverrideEnabled"
          class="group-settings__field group-settings__wide"
        >
          <span>{{
            field.key === 'base_url' ? t('group.settings.base.upstreamUrl') : field.label
          }}</span>
          <input
            class="group-settings__mono"
            :type="field.input_kind === 'url' ? 'url' : 'text'"
            :value="params[field.key] ?? ''"
            :disabled="pending || paramsDisabled"
            :required="field.required || (field.key === 'base_url' && baseUrlOverrideEnabled)"
            :aria-invalid="paramErrors[field.key] ? 'true' : undefined"
            @input="updateParam(field, ($event.target as HTMLInputElement).value)"
          />
          <small v-if="paramErrors[field.key]" role="alert">{{ paramErrors[field.key] }}</small>
          <small v-else-if="field.input_kind === 'url'">{{
            t('group.settings.base.urlWarning')
          }}</small>
        </label>
      </template>
    </div>
    <label class="group-settings__switch-row">
      <span class="group-settings__switch-copy">
        <strong>{{ t('group.settings.base.enabled') }}</strong>
        <small>{{ t('group.settings.base.enabledHelp') }}</small>
      </span>
      <span class="group-settings__switch">
        <input
          type="checkbox"
          :checked="enabled"
          :disabled="pending"
          @change="emit('update:enabled', ($event.target as HTMLInputElement).checked)"
        />
        <span aria-hidden="true"></span>
      </span>
    </label>
  </section>

  <section v-else id="settings-routing" class="group-settings__section">
    <header class="group-settings__section-heading">
      <h3>{{ t('group.settings.sections.routing') }}</h3>
      <p>{{ t('group.settings.routing.description') }}</p>
    </header>
    <div class="group-settings__field group-settings__wide">
      <span>{{ t('group.settings.base.weight') }}</span>
      <div class="group-settings__weight-editor">
        <SegmentedControl
          :model-value="weightMode"
          :label="t('group.settings.base.weight')"
          :options="weightModes"
          size="compact"
          @update:model-value="setWeightMode"
        />
        <input
          v-if="weightManual !== null"
          class="group-settings__mono"
          type="number"
          min="1"
          max="100"
          step="1"
          inputmode="numeric"
          :value="weightManual"
          :disabled="pending"
          :aria-label="t('group.settings.base.weight')"
          :aria-invalid="!weightValid || undefined"
          @input="emit('update:weightManual', Number(($event.target as HTMLInputElement).value))"
        />
      </div>
      <small>{{ t('group.settings.routing.weightHelp') }}</small>
      <small v-if="!weightValid" role="alert">{{ t('group.settings.base.weightError') }}</small>
    </div>
  </section>
</template>

<style scoped>
.group-settings__section {
  display: grid;
  gap: 15px;
  scroll-margin-top: 76px;
  border-top: 1px solid var(--color-border-control);
  padding-top: 17px;
}

.group-settings__section:first-child {
  border-top: 0;
  padding-top: 0;
}

.group-settings__section-heading h3,
.group-settings__section-heading p {
  margin: 0;
}

.group-settings__section-heading h3 {
  font-size: 14px;
  font-weight: 650;
}

.group-settings__section-heading p {
  max-width: 580px;
  margin-top: 3px;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}

.group-settings__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 15px 18px;
}

.group-settings__wide {
  grid-column: 1 / -1;
}

.group-settings__field {
  display: grid;
  align-content: start;
  gap: 6px;
}

.group-settings__field > span,
.group-settings__field > legend {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  font-weight: 560;
}

.group-settings__field small {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: var(--line-normal);
}

.group-settings__field small[role='alert'] {
  color: var(--color-danger);
}

.group-settings__field input:not([type='checkbox']) {
  width: 100%;
  min-height: var(--touch-target);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 0 var(--space-3);
  font: inherit;
}

.group-settings__field input:not([type='checkbox']):focus-visible {
  border-color: var(--color-action);
  outline: 2px solid color-mix(in srgb, var(--color-action) 26%, transparent);
  outline-offset: 1px;
}

.group-settings__readonly {
  display: flex;
  min-height: var(--touch-target);
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: 8px var(--space-3);
}

.group-settings__readonly strong {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.group-settings__readonly code {
  flex: none;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.group-settings__mono,
.group-settings__field code {
  font-family: var(--font-mono);
}

.group-settings__base-url-switch {
  display: flex;
  min-height: var(--control-xs);
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}

.group-settings__base-url-switch small {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: 1.55;
}

fieldset {
  margin: 0;
  border: 0;
  padding: 0;
}

.group-settings__switch-row {
  display: flex;
  min-height: 48px;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  border-top: 1px solid var(--color-border-subtle);
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 8px 2px;
}

.group-settings__switch-copy {
  display: grid;
}

.group-settings__switch-copy strong {
  font-size: 12.5px;
}

.group-settings__switch-copy small {
  color: var(--color-text-faint);
  font-size: 11px;
}

.group-settings__switch {
  position: relative;
  display: inline-flex;
  width: 42px;
  height: 24px;
  flex: none;
  cursor: pointer;
}

.group-settings__switch input {
  position: absolute;
  opacity: 0;
  pointer-events: none;
}

.group-settings__switch > span {
  width: 100%;
  border: 1px solid var(--color-border-control);
  border-radius: 999px;
  background: var(--color-surface-sunken);
  transition:
    background-color var(--duration-fast) var(--easing-standard),
    border-color var(--duration-fast) var(--easing-standard);
}

.group-settings__switch > span::after {
  position: absolute;
  top: 4px;
  left: 4px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: var(--color-text-faint);
  content: '';
  transition:
    transform var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard);
}

.group-settings__switch input:checked + span {
  border-color: var(--color-action);
  background: var(--color-action);
}

.group-settings__switch input:checked + span::after {
  transform: translateX(18px);
  background: var(--color-action-ink);
}

.group-settings__switch input:disabled + span {
  cursor: not-allowed;
  opacity: 0.55;
}

.group-settings__switch input:focus-visible + span {
  outline: 2px solid var(--color-action);
  outline-offset: 2px;
}

.group-settings__weight-editor {
  display: flex;
  align-items: center;
  gap: 10px;
}

.group-settings__field .group-settings__weight-editor > input {
  width: 90px !important;
  min-height: var(--touch-target);
  flex: 0 0 90px;
}

@media (max-width: 800px) {
  .group-settings__grid {
    grid-template-columns: 1fr;
  }

  .group-settings__wide {
    grid-column: auto;
  }

  .group-settings__field input:not([type='checkbox']) {
    font-size: 16px;
  }

  .group-settings__weight-editor :deep(.segmented-control__trigger) {
    min-height: var(--touch-target);
  }

  .group-settings__field .group-settings__weight-editor > input {
    min-height: var(--touch-target);
  }

  .group-settings__switch {
    width: var(--touch-target);
    height: var(--touch-target);
  }

  .group-settings__switch > span {
    position: absolute;
    top: 10px;
    right: 0;
    width: 42px;
    height: 24px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .group-settings__switch > span,
  .group-settings__switch > span::after {
    transition: none;
  }
}
</style>
