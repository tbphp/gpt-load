<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import type { RedactionRule } from '@shared/request-redaction'
import { useRedactionPreview } from '@shared/request-redaction-preview'
import AppButton from '@/components/ui/AppButton.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import FormField from '@/components/ui/FormField.vue'

const props = defineProps<{ rules: RedactionRule[]; disabled?: boolean }>()
const { t } = useI18n()
const open = ref(false)
const id = useId()
const { text, result, pending, stale, error, preview, clear } = useRedactionPreview(
  () => props.rules,
)
const output = computed(
  () =>
    result.value?.segments
      .map((part) =>
        part.kind === 'text'
          ? part.text
          : part.mode === 'replace'
            ? part.replacement
            : t('requestRedaction.preview.encrypted', { rule: part.rule_index + 1 }),
      )
      .join('') ?? '',
)
function toggle() {
  open.value = !open.value
  if (!open.value) clear()
}
</script>

<template>
  <div class="redaction-preview">
    <div>
      <AppButton
        variant="secondary"
        size="compact"
        :aria-expanded="open"
        :aria-controls="id"
        @click="toggle"
      >
        {{ t('requestRedaction.preview.title') }}
      </AppButton>
    </div>
    <div v-if="open" :id="id" class="redaction-preview-body">
      <FormField
        :id="id + '-text'"
        :label="t('requestRedaction.preview.sample')"
        :description="t('requestRedaction.preview.help')"
        size="compact"
      >
        <template #default="{ describedBy }">
          <textarea
            :id="id + '-text'"
            v-model="text"
            :aria-describedby="describedBy"
            rows="4"
            spellcheck="false"
            autocomplete="off"
            autocapitalize="none"
            autocorrect="off"
          />
        </template>
      </FormField>
      <div>
        <AppButton size="sm" :busy="pending" :disabled="disabled" @click="preview">
          {{ t('requestRedaction.preview.run') }}
        </AppButton>
      </div>
      <p v-if="error" class="redaction-preview-error" role="alert">
        {{ t('requestRedaction.preview.errors.' + error) }}
      </p>
      <p v-if="stale" class="redaction-preview-stale" role="status">
        {{ t('requestRedaction.preview.stale') }}
      </p>
      <template v-if="result">
        <p role="status">
          {{ t('requestRedaction.preview.count', { count: result.match_count }) }}
        </p>
        <div class="redaction-preview-result">
          <p>{{ t('requestRedaction.preview.matches') }}</p>
          <pre><template v-for="(part, index) in result.segments" :key="index"><AppTooltip v-if="part.kind === 'match'" :content="t('requestRedaction.preview.rule', { rule: part.rule_index + 1, mode: t('requestRedaction.modes.' + part.mode) })"><mark tabindex="0">{{ part.text }}</mark></AppTooltip><template v-else>{{ part.text }}</template></template></pre>
          <p>{{ t('requestRedaction.preview.output') }}</p>
          <pre>{{ output }}</pre>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.redaction-preview,
.redaction-preview-body,
.redaction-preview-result {
  display: grid;
  min-width: 0;
  gap: var(--space-2);
}
.redaction-preview p {
  margin: 0;
  font-size: var(--text-sm);
}
.redaction-preview textarea,
.redaction-preview pre {
  box-sizing: border-box;
  width: 100%;
  margin: 0;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  padding: var(--space-3);
  color: var(--color-text);
  background: var(--color-surface);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  line-height: 1.6;
}
.redaction-preview textarea {
  resize: vertical;
}
.redaction-preview pre {
  max-height: 240px;
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.redaction-preview mark {
  color: var(--color-action);
  background: var(--color-action-soft);
}
.redaction-preview-error {
  color: var(--color-danger);
}
.redaction-preview-stale {
  color: var(--color-warning);
}
</style>
