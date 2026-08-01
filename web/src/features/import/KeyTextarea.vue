<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import FormField from '@/components/ui/FormField.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

import { analyzeKeys } from './key-analysis'

const props = withDefaults(
  defineProps<{
    modelValue: string
    disabled?: boolean
    showHeaderDescription?: boolean
    storageDescription?: string
    duplicateLabel?: string
    showUpstreamNotice?: boolean
    rows?: number
  }>(),
  {
    disabled: false,
    showHeaderDescription: true,
    storageDescription: undefined,
    duplicateLabel: undefined,
    showUpstreamNotice: true,
    rows: 6,
  },
)
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
  {
    value: analysis.value.duplicateCount,
    label: props.duplicateLabel ?? t('import.keys.counters.duplicates'),
  },
  { value: analysis.value.likelyAccessKeyCount, label: t('import.keys.counters.accessKeys') },
])
</script>

<template>
  <section class="key-entry" aria-labelledby="upstream-keys-heading">
    <header>
      <h2 id="upstream-keys-heading">{{ t('import.keys.title') }}</h2>
      <p v-if="showHeaderDescription">{{ t('import.keys.description') }}</p>
    </header>

    <FormField
      id="upstream-keys"
      :label="t('import.keys.label')"
      :description="storageDescription ?? t('import.keys.storageNotice')"
      :error="error"
      required
      :required-text="t('import.required')"
      size="compact"
    >
      <template #default="field">
        <textarea
          id="upstream-keys"
          :rows="rows"
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

    <InlineFeedback
      v-if="analysis.likelyAccessKeyCount"
      class="key-entry__warning"
      tone="warning"
      appearance="hint"
    >
      {{ t('import.keys.accessKeyWarning', { count: analysis.likelyAccessKeyCount }) }}
    </InlineFeedback>
    <InlineFeedback
      v-if="showUpstreamNotice"
      class="key-entry__note"
      tone="neutral"
      appearance="ledger-hint"
      glyph="i"
    >
      {{ t('import.keys.upstreamKeyNotice') }}
    </InlineFeedback>
  </section>
</template>

<style scoped>
.key-entry {
  display: grid;
  gap: 0;
  min-width: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 22px 0 var(--space-6);
}

.key-entry > header h2,
.key-entry > header p {
  margin: 0;
}

.key-entry > header h2 {
  font-size: var(--title-section);
  font-weight: 650;
  letter-spacing: -0.01em;
}

.key-entry > header p {
  margin-top: 3px;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}

.key-entry > header {
  margin-bottom: var(--space-3);
}

.key-entry :deep(textarea) {
  min-height: 124px;
  background: var(--color-surface);
  padding: 9px 10px;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  line-height: 1.65;
}

.key-entry__counters {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  overflow: hidden;
  margin-top: 18px;
  border-block: 1px solid var(--color-border-subtle);
  border-radius: 0;
  gap: 0;
}

.key-entry__counters > div {
  min-height: 56px;
  background: transparent;
  padding: 9px 10px;
  line-height: 1.55;
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
  font-size: 13px;
  font-weight: 600;
}

.key-entry__warning {
  margin-top: 18px;
  font-size: 11px;
}

.key-entry__note {
  margin-top: 18px;
  font-size: 10.8px;
}

.key-entry__counters span {
  color: var(--color-text-faint);
  font-size: 9.5px;
}

@media (max-width: 520px) {
  .key-entry__counters {
    grid-template-columns: 1fr;
  }

  .key-entry__counters > div + div {
    border-left: 0;
    border-top: 1px solid var(--color-border-subtle);
  }
}
</style>
