<script setup lang="ts">
import { CircleHelp, Trash2 } from '@lucide/vue'
import { computed, onScopeDispose, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { redactionPresets, type RedactionRule } from '@/app/resources/request-redaction'
import AppButton from '@/components/ui/AppButton.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import IconButton from '@/components/ui/IconButton.vue'
import { useRedactionValidation } from './use-redaction-validation'

const props = defineProps<{ modelValue: RedactionRule[]; disabled?: boolean }>()
const emit = defineEmits<{
  'update:modelValue': [value: RedactionRule[]]
  invalid: [value: boolean]
}>()
const { t } = useI18n()
const modeOptions = computed(() => [
  { value: 'encrypt', label: t('requestRedaction.modes.encrypt') },
  { value: 'replace', label: t('requestRedaction.modes.replace') },
])
const { issues, failed, invalid } = useRedactionValidation(() => props.modelValue)
watch(invalid, (value) => emit('invalid', value), { immediate: true })
onScopeDispose(() => emit('invalid', false))
function add(rule: RedactionRule = { pattern: '', replacement: '[REDACTED]' }) {
  emit('update:modelValue', [
    ...props.modelValue,
    {
      pattern: rule.pattern,
      replacement: rule.replacement,
      mode: rule.mode ?? 'encrypt',
    },
  ])
}
function update(index: number, patch: Partial<RedactionRule>) {
  emit(
    'update:modelValue',
    props.modelValue.map((rule, i) => (i === index ? { ...rule, ...patch } : rule)),
  )
}
function error(index: number): string | undefined {
  const issue = issues.value.find((item) => item.index === index && item.error)
  if (!issue?.error) return undefined
  if (issue.error === 'empty_pattern') return t('requestRedaction.emptyPattern')
  if (issue.error === 'invalid_length') return t('requestRedaction.length')
  return t('requestRedaction.invalid', { reason: issue.error })
}
</script>

<template>
  <section id="settings-redaction" class="settings-section redaction">
    <header>
      <h2>{{ t('requestRedaction.title') }}</h2>
      <p>{{ t('requestRedaction.help') }}</p>
    </header>
    <h3 class="redaction-quick-title">{{ t('requestRedaction.quickAdd') }}</h3>
    <div class="redaction-presets">
      <AppButton
        v-for="preset in redactionPresets"
        :key="preset.key"
        variant="secondary"
        size="compact"
        class="redaction-tag"
        :disabled="disabled || modelValue.length >= 64"
        @click="add(preset)"
        >{{ t('requestRedaction.presets.' + preset.key) }}</AppButton
      >
    </div>
    <p v-if="!modelValue.length" class="redaction-note">{{ t('requestRedaction.empty') }}</p>
    <div v-if="modelValue.length" class="redaction-heading">
      <span>{{ t('requestRedaction.pattern') }}</span>
      <span class="redaction-mode-label">
        {{ t('requestRedaction.mode') }}
        <AppTooltip :content="t('requestRedaction.modeHelp')">
          <button
            type="button"
            class="redaction-mode-help"
            :aria-label="t('requestRedaction.modeHelp')"
          >
            <CircleHelp :size="13" aria-hidden="true" />
          </button>
        </AppTooltip>
      </span>
      <span>{{ t('requestRedaction.replacement') }}</span>
    </div>
    <div v-for="(rule, index) in modelValue" :key="index" class="redaction-row">
      <div class="redaction-field">
        <span class="redaction-mobile-label">{{ t('requestRedaction.pattern') }}</span>
        <AppTextInput
          :model-value="rule.pattern"
          :label="t('requestRedaction.pattern')"
          :invalid="Boolean(error(index))"
          :disabled="disabled"
          :spellcheck="false"
          monospace
          size="sm"
          @update:model-value="update(index, { pattern: $event })"
        />
        <p v-if="error(index)" class="redaction-error" role="alert">{{ error(index) }}</p>
        <p
          v-if="issues.some((issue) => issue.index === index && issue.warning)"
          class="redaction-warning"
          role="status"
        >
          {{ t('requestRedaction.broad') }}
        </p>
      </div>
      <div class="redaction-field redaction-mode">
        <span class="redaction-mobile-label">
          <span class="redaction-mode-label">
            {{ t('requestRedaction.mode') }}
            <AppTooltip :content="t('requestRedaction.modeHelp')">
              <button
                type="button"
                class="redaction-mode-help"
                :aria-label="t('requestRedaction.modeHelp')"
              >
                <CircleHelp :size="13" aria-hidden="true" />
              </button>
            </AppTooltip>
          </span>
        </span>
        <AppSelect
          :model-value="rule.mode ?? 'replace'"
          :options="modeOptions"
          :label="t('requestRedaction.mode')"
          size="sm"
          :disabled="disabled"
          @update:model-value="
            update(index, { mode: $event === 'encrypt' ? 'encrypt' : 'replace' })
          "
        />
      </div>
      <div
        v-if="(rule.mode ?? 'replace') === 'replace'"
        class="redaction-field redaction-replacement"
      >
        <span class="redaction-mobile-label">{{ t('requestRedaction.replacement') }}</span>
        <AppTextInput
          :model-value="rule.replacement"
          :label="t('requestRedaction.replacement')"
          :disabled="disabled"
          :spellcheck="false"
          size="sm"
          @update:model-value="update(index, { replacement: $event })"
        />
      </div>
      <IconButton
        variant="ghost"
        size="compact"
        :label="t('requestRedaction.remove')"
        class="redaction-remove"
        :disabled="disabled"
        @click="
          emit(
            'update:modelValue',
            modelValue.filter((_, i) => i !== index),
          )
        "
      >
        <Trash2 :size="16" />
      </IconButton>
    </div>
    <div>
      <AppButton
        variant="secondary"
        size="sm"
        :disabled="disabled || modelValue.length >= 64"
        @click="add()"
        >{{ t('requestRedaction.add') }}</AppButton
      >
    </div>
    <p v-if="failed" class="redaction-error" role="alert">{{ t('requestRedaction.failed') }}</p>
    <p v-else-if="issues.some((issue) => issue.index < 0)" class="redaction-error" role="alert">
      {{
        t(
          issues.some((issue) => issue.error === 'configuration_too_large')
            ? 'requestRedaction.configLimit'
            : 'requestRedaction.limit',
        )
      }}
    </p>
    <p class="redaction-note">{{ t('requestRedaction.syntax') }}</p>
  </section>
</template>

<style scoped>
.redaction,
.redaction-field {
  display: grid;
  gap: var(--space-2);
  min-width: 0;
}
.redaction {
  gap: var(--space-3);
}
.redaction header h2 {
  margin: 0;
  font-size: var(--text-lg);
}
.redaction header p,
.redaction-note {
  color: var(--color-text-muted);
  margin: 0;
  font-size: var(--text-sm);
}
.redaction-quick-title {
  margin: 0;
  color: var(--color-text);
  font-size: var(--text-sm);
}
.redaction-presets {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.redaction-tag {
  border-radius: var(--radius-tag);
}
.redaction-heading,
.redaction-row {
  display: grid;
  grid-template-columns: minmax(0, 3fr) 126px minmax(0, 2fr) var(--control-compact);
  align-items: start;
  gap: var(--space-2);
}
.redaction-heading {
  color: var(--color-text);
  font-size: var(--text-sm);
}
.redaction-mobile-label {
  display: none;
}
.redaction-mode-label {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}
.redaction-mode-help {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border: 0;
  border-radius: var(--radius-tag);
  background: transparent;
  color: var(--color-text-faint);
  padding: 0;
  cursor: help;
}
.redaction-mode-help:hover {
  background: var(--color-surface-sunken);
  color: var(--color-text);
}
.redaction-mode-help:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}
.redaction-remove {
  grid-column: 4;
  align-self: start;
  margin-top: var(--space-0-5);
}
.redaction-error,
.redaction-warning {
  margin: 0;
  font-size: var(--text-sm);
  overflow-wrap: anywhere;
}
.redaction-error {
  color: var(--color-danger);
}
.redaction-warning {
  color: var(--color-warning);
}
@media (max-width: 640px) {
  .redaction-heading {
    display: none;
  }
  .redaction-row {
    grid-template-columns: minmax(0, 1fr) var(--control-compact);
  }
  .redaction-field:first-child {
    grid-column: 1 / -1;
  }
  .redaction-mode {
    grid-column: 1;
    grid-row: 2;
  }
  .redaction-replacement {
    grid-column: 1 / -1;
    grid-row: 3;
  }
  .redaction-mobile-label {
    display: block;
    color: var(--color-text);
    font-size: var(--text-sm);
  }
  .redaction-remove {
    grid-column: 2;
    grid-row: 2;
    margin-top: var(--space-6);
  }
}
</style>
