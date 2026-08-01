<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import FormField from '@/components/ui/FormField.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

import { analyzeKeys } from './key-analysis'

const props = defineProps<{ modelValue: string; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t } = useI18n()
const analysis = computed(() => analyzeKeys(props.modelValue))
const error = computed(() => {
  if (analysis.value.tooManyKeys) return t('import.keys.tooMany')
  return ''
})
const counters = computed(() => [
  { value: analysis.value.nonEmptyCount, label: t('import.keys.counters.nonEmpty') },
  { value: analysis.value.emptyLineCount, label: t('import.keys.counters.empty') },
  { value: analysis.value.duplicateCount, label: t('import.keys.counters.duplicates') },
  { value: analysis.value.likelyAccessKeyCount, label: t('import.keys.counters.accessKeys') },
])
</script>

<template>
  <section class="key-entry" aria-labelledby="upstream-keys-heading">
    <header>
      <h2 id="upstream-keys-heading">{{ t('import.keys.title') }}</h2>
      <p>{{ t('import.keys.description') }}</p>
    </header>

    <FormField
      id="upstream-keys"
      :label="t('import.keys.label')"
      :description="t('import.keys.storageNotice')"
      :error="error"
      required
      :required-text="t('import.required')"
    >
      <template #default="field">
        <textarea
          id="upstream-keys"
          :value="modelValue"
          :disabled="disabled"
          :aria-invalid="field.invalid || undefined"
          :aria-describedby="field.describedBy"
          autocomplete="off"
          autocapitalize="none"
          spellcheck="false"
          :placeholder="t('import.keys.placeholder')"
          @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
        ></textarea>
      </template>
    </FormField>

    <div
      class="key-entry__counters"
      :aria-label="t('import.keys.analysisLabel')"
      aria-live="polite"
    >
      <div v-for="counter in counters" :key="counter.label">
        <strong>{{ counter.value }}</strong>
        <span>{{ counter.label }}</span>
      </div>
    </div>

    <InlineFeedback v-if="analysis.likelyAccessKeyCount" tone="warning" appearance="hint">
      {{ t('import.keys.accessKeyWarning', { count: analysis.likelyAccessKeyCount }) }}
    </InlineFeedback>
    <InlineFeedback tone="info" appearance="hint">
      {{ t('import.keys.upstreamKeyNotice') }}
    </InlineFeedback>
  </section>
</template>

<style scoped>
.key-entry {
  display: grid;
  gap: var(--space-4);
  min-width: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: var(--space-5) 0 var(--space-6);
}

.key-entry > header h2,
.key-entry > header p {
  margin: 0;
}

.key-entry > header h2 {
  font-family: var(--font-serif);
  font-size: var(--title-section);
  font-weight: 500;
}

.key-entry > header p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.key-entry :deep(textarea) {
  min-height: calc(var(--space-6) * 6);
  background: var(--color-surface-sunken);
  font-family: var(--font-mono);
  line-height: var(--line-normal);
}

.key-entry__counters {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  gap: 0;
}

.key-entry__counters > div {
  background: var(--color-surface-sunken);
  padding: var(--space-2) var(--space-3);
}

.key-entry__counters > div + div {
  border-left: 1px solid var(--color-border-subtle);
}

.key-entry__counters strong,
.key-entry__counters span {
  display: block;
}

.key-entry__counters strong {
  font-family: var(--font-mono);
  font-size: var(--text-body);
}

.key-entry__counters span {
  margin-top: var(--space-1);
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

@media (max-width: 560px) {
  .key-entry__counters {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .key-entry__counters > div:nth-child(odd) {
    border-left: 0;
  }

  .key-entry__counters > div:nth-child(n + 3) {
    border-top: 1px solid var(--color-border-subtle);
  }
}
</style>
