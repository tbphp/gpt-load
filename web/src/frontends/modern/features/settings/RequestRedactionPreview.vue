<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import type { RedactionRule } from '@shared/request-redaction'
import { useRedactionPreview } from '@shared/request-redaction-preview'
import { AppButton, AppTextArea, AppTooltip } from '@modern/components/ui'

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
  <div class="modern-redaction-preview">
    <div>
      <AppButton
        variant="outline"
        size="xxs"
        :aria-expanded="open"
        :aria-controls="id"
        @click="toggle"
      >
        {{ t('requestRedaction.preview.title') }}
      </AppButton>
    </div>
    <div v-if="open" :id="id" class="modern-redaction-preview-body">
      <AppTextArea
        v-model="text"
        :label="t('requestRedaction.preview.sample')"
        :description="t('requestRedaction.preview.help')"
        :rows="4"
        mono
        size="sm"
        :spellcheck="false"
        autocomplete="off"
      />
      <div>
        <AppButton size="sm" :loading="pending" :disabled="disabled || pending" @click="preview">
          {{ t('requestRedaction.preview.run') }}
        </AppButton>
      </div>
      <p v-if="error" class="modern-redaction-preview-error" role="alert">
        {{ t('requestRedaction.preview.errors.' + error) }}
      </p>
      <p v-if="stale" class="modern-redaction-preview-stale" role="status">
        {{ t('requestRedaction.preview.stale') }}
      </p>
      <template v-if="result">
        <p role="status">
          {{ t('requestRedaction.preview.count', { count: result.match_count }) }}
        </p>
        <div class="modern-redaction-preview-result">
          <p>{{ t('requestRedaction.preview.matches') }}</p>
          <pre><template v-for="(part, index) in result.segments" :key="index"><AppTooltip v-if="part.kind === 'match'" :label="t('requestRedaction.preview.rule', { rule: part.rule_index + 1, mode: t('requestRedaction.modes.' + part.mode) })"><mark tabindex="0">{{ part.text }}</mark></AppTooltip><template v-else>{{ part.text }}</template></template></pre>
          <p>{{ t('requestRedaction.preview.output') }}</p>
          <pre>{{ output }}</pre>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.modern-redaction-preview,
.modern-redaction-preview-body,
.modern-redaction-preview-result {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-2);
}
.modern-redaction-preview p {
  margin: 0;
  font-size: var(--modern-font-size-secondary);
}
.modern-redaction-preview pre {
  margin: 0;
  max-height: 240px;
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-small);
  padding: var(--modern-space-3);
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-secondary);
  line-height: var(--modern-leading-body);
}
.modern-redaction-preview mark {
  color: var(--modern-accent);
  background: var(--modern-accent-soft);
}
.modern-redaction-preview-error {
  color: var(--modern-danger);
}
.modern-redaction-preview-stale {
  color: var(--modern-warning);
}
</style>
