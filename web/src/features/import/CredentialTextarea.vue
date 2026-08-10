<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import FormField from '@/components/ui/FormField.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import type { ChannelDto } from '@/app/resources/channels'

import { analyzeCredentials } from './credential-analysis'

const props = withDefaults(
  defineProps<{
    modelValue: string
    channel?: ChannelDto | null
    disabled?: boolean
    showHeaderDescription?: boolean
    storageDescription?: string
    duplicateLabel?: string
    showCredentialNotice?: boolean
    rows?: number
  }>(),
  {
    disabled: false,
    channel: null,
    showHeaderDescription: true,
    storageDescription: undefined,
    duplicateLabel: undefined,
    showCredentialNotice: true,
    rows: 6,
  },
)
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t } = useI18n()
const analysis = computed(() => analyzeCredentials(props.modelValue, props.channel?.channel_id))
const structured = computed(
  () =>
    props.channel !== null &&
    props.channel !== undefined &&
    (props.channel.credential_fields.length !== 1 ||
      props.channel.credential_fields[0]?.key !== 'api_key'),
)
const title = computed(() =>
  structured.value ? t('import.credentials.structuredTitle') : t('import.credentials.title'),
)
const description = computed(() =>
  structured.value
    ? t('import.credentials.structuredDescription')
    : t('import.credentials.description'),
)
const label = computed(() =>
  structured.value ? t('import.credentials.structuredLabel') : t('import.credentials.label'),
)
const placeholder = computed(() => {
  switch (props.channel?.channel_id) {
    case 'azure_openai':
      return t('import.credentials.placeholders.azure')
    case 'aws_bedrock':
      return t('import.credentials.placeholders.bedrock')
    case 'google_vertex':
      return t('import.credentials.placeholders.vertex')
    default:
      return t('import.credentials.placeholder')
  }
})
const fieldSummary = computed(
  () =>
    props.channel?.credential_fields.map(({ label: fieldLabel }) => fieldLabel).join(' · ') ?? '',
)
const error = computed(() => {
  if (analysis.value.tooManyCredentials) return t('import.credentials.tooMany')
  return ''
})
const counters = computed(() => [
  { value: analysis.value.nonEmptyCount, label: t('import.credentials.counters.nonEmpty') },
  { value: analysis.value.emptyLineCount, label: t('import.credentials.counters.empty') },
  {
    value: analysis.value.duplicateCount,
    label: props.duplicateLabel ?? t('import.credentials.counters.duplicates'),
  },
  {
    value: analysis.value.likelyAccessKeyCount,
    label: t('import.credentials.counters.accessKeys'),
  },
])
</script>

<template>
  <section class="credential-entry" aria-labelledby="channel-credentials-heading">
    <header>
      <h2 id="channel-credentials-heading">{{ title }}</h2>
      <p v-if="showHeaderDescription">{{ description }}</p>
    </header>

    <FormField
      id="channel-credentials"
      :label="label"
      :description="storageDescription ?? t('import.credentials.storageNotice')"
      :error="error"
      required
      :required-text="t('import.required')"
      size="compact"
    >
      <template #default="field">
        <textarea
          id="channel-credentials"
          :rows="rows"
          :value="modelValue"
          :disabled="disabled"
          :aria-invalid="field.invalid || undefined"
          :aria-describedby="field.describedBy"
          autocomplete="off"
          autocapitalize="none"
          spellcheck="false"
          :placeholder="placeholder"
          @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
        ></textarea>
      </template>
    </FormField>

    <InlineFeedback
      v-if="structured && fieldSummary"
      class="credential-entry__format"
      tone="neutral"
      appearance="hint"
    >
      {{ t('import.credentials.structuredHint', { fields: fieldSummary }) }}
    </InlineFeedback>

    <div
      class="credential-entry__counters"
      :aria-label="t('import.credentials.analysisLabel')"
      aria-live="polite"
    >
      <div v-for="counter in counters" :key="counter.label">
        <strong>{{ counter.value }}</strong>
        <span>{{ counter.label }}</span>
      </div>
    </div>

    <InlineFeedback
      v-if="analysis.likelyAccessKeyCount"
      class="credential-entry__warning"
      tone="warning"
      appearance="hint"
    >
      {{ t('import.credentials.accessKeyWarning', { count: analysis.likelyAccessKeyCount }) }}
    </InlineFeedback>
    <InlineFeedback
      v-if="showCredentialNotice"
      class="credential-entry__note"
      tone="neutral"
      appearance="ledger-hint"
      glyph="i"
    >
      {{ t('import.credentials.channelCredentialNotice') }}
    </InlineFeedback>
  </section>
</template>

<style scoped>
.credential-entry {
  display: grid;
  gap: 0;
  min-width: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 22px 0 var(--space-6);
}

.credential-entry > header h2,
.credential-entry > header p {
  margin: 0;
}

.credential-entry > header h2 {
  font-size: var(--title-section);
  font-weight: 650;
  letter-spacing: -0.01em;
}

.credential-entry > header p {
  margin-top: 3px;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}

.credential-entry > header {
  margin-bottom: var(--space-3);
}

.credential-entry :deep(textarea) {
  min-height: 124px;
  background: var(--color-surface);
  padding: 9px 10px;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  line-height: 1.65;
}

.credential-entry__counters {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  overflow: hidden;
  margin-top: 18px;
  border-block: 1px solid var(--color-border-subtle);
  border-radius: 0;
  gap: 0;
}

.credential-entry__counters > div {
  min-height: 56px;
  background: transparent;
  padding: 9px 10px;
  line-height: 1.55;
}

.credential-entry__counters > div + div {
  border-left: 1px solid var(--color-border-subtle);
}

.credential-entry__counters strong,
.credential-entry__counters span {
  display: block;
}

.credential-entry__counters strong {
  font-family: var(--font-mono);
  font-size: 13px;
  font-weight: 600;
}

.credential-entry__warning {
  margin-top: 18px;
  font-size: 11px;
}

.credential-entry__format {
  margin-top: 12px;
  font-size: 10.8px;
}

.credential-entry__note {
  margin-top: 18px;
  font-size: 10.8px;
}

.credential-entry__counters span {
  color: var(--color-text-faint);
  font-size: 9.5px;
}

@media (max-width: 520px) {
  .credential-entry__counters {
    grid-template-columns: 1fr;
  }

  .credential-entry__counters > div + div {
    border-left: 0;
    border-top: 1px solid var(--color-border-subtle);
  }
}
</style>
