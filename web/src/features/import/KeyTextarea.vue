<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { analyzeKeys } from './key-analysis'

const props = defineProps<{ modelValue: string; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t } = useI18n()
const analysis = computed(() => analyzeKeys(props.modelValue))
</script>

<template>
  <div class="key-textarea">
    <label for="upstream-keys">{{ t('import.keys.label') }}</label>
    <textarea
      id="upstream-keys"
      data-test="keys"
      :value="modelValue"
      :disabled="disabled"
      autocomplete="off"
      autocapitalize="none"
      spellcheck="false"
      :placeholder="t('import.keys.placeholder')"
      @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
    ></textarea>
    <div class="key-textarea__hints" aria-live="polite">
      <span>{{ t('import.keys.count', { count: analysis.nonEmptyCount }) }}</span>
      <span v-if="analysis.emptyLineCount">{{
        t('import.keys.empty', { count: analysis.emptyLineCount })
      }}</span>
      <span v-if="analysis.duplicateCount" class="warning">{{
        t('import.keys.duplicates', { count: analysis.duplicateCount })
      }}</span>
      <span v-if="analysis.likelyAccessKeyCount" class="warning">{{
        t('import.keys.accessKeyWarning', { count: analysis.likelyAccessKeyCount })
      }}</span>
      <span v-if="analysis.tooManyKeys" class="danger" role="alert">{{
        t('import.keys.tooMany')
      }}</span>
    </div>
  </div>
</template>

<style scoped>
.key-textarea {
  display: grid;
  gap: var(--space-2);
}
label {
  color: var(--color-text-muted);
  font-size: 0.75rem;
  font-weight: 650;
}
textarea {
  width: 100%;
  min-height: 144px;
  resize: vertical;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
  color: var(--color-text);
  padding: var(--space-3);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  line-height: 1.55;
}
.key-textarea__hints {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2) var(--space-4);
  color: var(--color-text-muted);
  font-size: 0.75rem;
}
.warning {
  color: var(--color-warning);
}
.danger {
  color: var(--color-danger);
}
</style>
