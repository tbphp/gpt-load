<script setup lang="ts">
import { Plus, Trash2 } from '@lucide/vue'
import { onScopeDispose, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { redactionPresets, type RedactionRule } from '@modern/api/request-redaction'
import { AppButton, AppIconButton, AppTextField } from '@modern/components/ui'
import { useRedactionValidation } from './use-redaction-validation'

const props = defineProps<{ modelValue: RedactionRule[]; disabled?: boolean }>()
const emit = defineEmits<{
  'update:modelValue': [value: RedactionRule[]]
  invalid: [value: boolean]
}>()
const { t } = useI18n()
const { issues, checking, failed, invalid } = useRedactionValidation(() => props.modelValue)
watch(invalid, (value) => emit('invalid', value), { immediate: true })
onScopeDispose(() => emit('invalid', false))
function add(rule: RedactionRule = { pattern: '', replacement: '[REDACTED]' }) {
  emit('update:modelValue', [
    ...props.modelValue,
    { pattern: rule.pattern, replacement: rule.replacement },
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
  <div class="modern-redaction">
    <div class="modern-redaction-presets">
      <AppButton
        v-for="preset in redactionPresets"
        :key="preset.key"
        variant="outline"
        size="xs"
        class="modern-redaction-tag"
        :disabled="disabled || modelValue.length >= 64"
        @click="add(preset)"
      >
        {{ t('requestRedaction.presets.' + preset.key) }}
      </AppButton>
    </div>
    <p v-if="!modelValue.length" class="modern-redaction-note">{{ t('requestRedaction.empty') }}</p>
    <div v-for="(rule, index) in modelValue" :key="index" class="modern-redaction-row">
      <AppTextField
        :model-value="rule.pattern"
        :label="t('requestRedaction.pattern')"
        :error="error(index)"
        :disabled="disabled"
        :spellcheck="false"
        autocomplete="off"
        @update:model-value="update(index, { pattern: $event })"
      />
      <AppTextField
        :model-value="rule.replacement"
        :label="t('requestRedaction.replacement')"
        :disabled="disabled"
        :spellcheck="false"
        autocomplete="off"
        @update:model-value="update(index, { replacement: $event })"
      />
      <AppIconButton
        :icon="Trash2"
        :label="t('requestRedaction.remove')"
        :disabled="disabled"
        class="modern-redaction-remove"
        @click="
          emit(
            'update:modelValue',
            modelValue.filter((_, i) => i !== index),
          )
        "
      />
      <p
        v-if="issues.some((issue) => issue.index === index && issue.warning)"
        class="modern-redaction-warning"
        role="status"
      >
        {{ t('requestRedaction.broad') }}
      </p>
    </div>
    <div>
      <AppButton
        :icon="Plus"
        size="sm"
        variant="ghost"
        :disabled="disabled || modelValue.length >= 64"
        @click="add()"
        >{{ t('requestRedaction.add') }}</AppButton
      >
    </div>
    <p v-if="failed" class="modern-redaction-error" role="alert">
      {{ t('requestRedaction.failed') }}
    </p>
    <p
      v-else-if="issues.some((issue) => issue.index < 0)"
      class="modern-redaction-error"
      role="alert"
    >
      {{
        t(
          issues.some((issue) => issue.error === 'configuration_too_large')
            ? 'requestRedaction.configLimit'
            : 'requestRedaction.limit',
        )
      }}
    </p>
    <p v-else-if="checking" class="modern-redaction-note" role="status">
      {{ t('requestRedaction.checking') }}
    </p>
    <p class="modern-redaction-note">{{ t('requestRedaction.syntax') }}</p>
  </div>
</template>

<style scoped>
.modern-redaction {
  display: grid;
  gap: var(--modern-space-3);
  min-width: 0;
  width: 100%;
}
.modern-redaction-presets {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}
.modern-redaction-tag {
  border-radius: var(--modern-radius-small);
}
.modern-redaction-row {
  display: grid;
  grid-template-columns: minmax(0, 3fr) minmax(0, 2fr) auto;
  align-items: start;
  gap: var(--modern-space-2);
}
.modern-redaction-remove {
  align-self: end;
}
.modern-redaction-note,
.modern-redaction-warning,
.modern-redaction-error {
  margin: 0;
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
  overflow-wrap: anywhere;
}
.modern-redaction-note {
  color: var(--modern-muted);
}
.modern-redaction-warning {
  color: var(--modern-warning);
  grid-column: 1 / -1;
}
.modern-redaction-error {
  color: var(--modern-danger);
}
@media (max-width: 760px) {
  .modern-redaction-row {
    grid-template-columns: minmax(0, 1fr) auto;
  }
  .modern-redaction-row > :first-child {
    grid-column: 1 / -1;
  }
}
</style>
